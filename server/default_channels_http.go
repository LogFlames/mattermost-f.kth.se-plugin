package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Only system admins may inspect or edit defaults across teams, including private channels.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	if r.URL.Path != "/default-channels" {
		fail(http.StatusNotFound, "Not found.")
		return
	}
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		fail(http.StatusUnauthorized, "Sign in to manage default channels.")
		return
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		fail(http.StatusForbidden, "Only system admins can manage defaults across teams.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	// Serialize backfill queueing with commands and membership work.
	lock, err := cluster.NewMutex(p.API, "default_channels")
	if err != nil {
		fail(http.StatusInternalServerError, "Could not lock default channels.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := lock.LockWithContext(ctx); err != nil {
		fail(http.StatusServiceUnavailable, "Default channels are busy. Please try again.")
		return
	}
	defer lock.Unlock()
	data, err := json.Marshal(p.API.GetPluginConfig())
	var config configuration
	if err != nil || json.Unmarshal(data, &config) != nil {
		fail(http.StatusInternalServerError, "Could not read default channel configuration.")
		return
	}
	if r.Method == http.MethodPost {
		var change struct {
			SaveID             string   `json:"save_id"`
			ExpectedChannelIDs []string `json:"expected_channel_ids"`
			AddedChannelIDs    []string `json:"added_channel_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&change); err != nil || change.SaveID == "" || len(change.SaveID) > 64 || change.ExpectedChannelIDs == nil || change.AddedChannelIDs == nil {
			fail(http.StatusBadRequest, "Specify the saved channel list and newly added channels.")
			return
		}
		// Mattermost invokes custom save actions even when its config PATCH fails.
		// Never enqueue a backfill until the saved selection matches the draft.
		saved := config.defaultChannelIDs()
		slices.Sort(saved)
		slices.Sort(change.ExpectedChannelIDs)
		if !slices.Equal(saved, change.ExpectedChannelIDs) {
			fail(http.StatusConflict, "The default channel list was not saved or changed elsewhere. Press Save to retry, or reload the settings.")
			return
		}
		var channels []*model.Channel
		for _, id := range change.AddedChannelIDs {
			if !slices.Contains(saved, id) {
				fail(http.StatusBadRequest, "Only saved default channels can add current members.")
				return
			}
			channel, appErr := p.API.GetChannel(id)
			if appErr != nil {
				fail(appErr.StatusCode, "Could not load a channel. Press Save to retry.")
				return
			}
			if channel == nil || channel.TeamId == "" || !eligibleDefaultChannel(channel, channel.TeamId) || channel.Name == model.DefaultChannelName {
				fail(http.StatusBadRequest, "Choose active public or private channels other than Town Square.")
				return
			}
			channels = append(channels, channel)
		}
		for _, channel := range channels {
			completed, appErr := p.API.KVGet(defaultChannelCompletedSavePrefix + channel.Id)
			if appErr != nil {
				fail(appErr.StatusCode, "Could not read completed additions. Press Save to retry.")
				return
			}
			if string(completed) == change.SaveID {
				continue
			}
			key := defaultChannelJobPrefix + "bulk_" + channel.Id
			pending, appErr := p.API.KVGet(key)
			if appErr != nil {
				fail(appErr.StatusCode, "Could not read pending additions. Press Save to retry.")
				return
			}
			if len(pending) != 0 {
				var job defaultChannelJob
				if err := json.Unmarshal(pending, &job); err != nil {
					fail(http.StatusInternalServerError, "Could not read pending additions.")
					return
				}
				if job.ConsoleSaveID == change.SaveID {
					continue // Retrying must not reset progress; a new save gets a new pass.
				}
			}
			if appErr := p.saveDefaultChannelJob(key, &defaultChannelJob{TeamID: channel.TeamId, ChannelID: channel.Id, ConsoleSaveID: change.SaveID}); appErr != nil {
				fail(appErr.StatusCode, "Defaults were saved, but adding current members could not be queued. Press Save to retry.")
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
		return
	}
	channels, appErr := p.defaultChannelsForConsole(&config)
	if appErr != nil {
		fail(appErr.StatusCode, "Could not load the hierarchy. Refresh to see the saved defaults.")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"channels": channels, "enabled": config.DefaultChannels_OnOffBool,
		"value": config.DefaultChannels_Custom,
	})
}

func (p *Plugin) defaultChannelsForConsole(config *configuration) ([]*model.ChannelWithTeamData, *model.AppError) {
	channels := []*model.ChannelWithTeamData{}
	teams := map[string]*model.Team{}
	for _, id := range config.defaultChannelIDs() {
		channel, err := p.API.GetChannel(id)
		if err != nil {
			if err.StatusCode == http.StatusNotFound {
				continue
			}
			return nil, err
		}
		if channel == nil || channel.TeamId == "" {
			continue
		}
		team := teams[channel.TeamId]
		if team == nil {
			var err *model.AppError
			team, err = p.API.GetTeam(channel.TeamId)
			if err != nil {
				return nil, err
			}
			teams[channel.TeamId] = team
		}
		if channel.Name != model.DefaultChannelName {
			channels = append(channels, &model.ChannelWithTeamData{Channel: *channel, TeamDisplayName: team.DisplayName, TeamName: team.Name})
		}
	}
	for id, team := range teams {
		channel, err := p.API.GetChannelByName(id, model.DefaultChannelName, false)
		if err != nil && err.StatusCode != http.StatusNotFound {
			return nil, err
		}
		if channel != nil {
			channels = append(channels, &model.ChannelWithTeamData{Channel: *channel, TeamDisplayName: team.DisplayName, TeamName: team.Name})
		}
	}
	return channels, nil
}
