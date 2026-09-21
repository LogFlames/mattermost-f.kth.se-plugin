package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
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

	// Use the command/worker lock, so two console edits cannot lose one another.
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
	settings := maps.Clone(p.API.GetPluginConfig())
	data, err := json.Marshal(settings)
	var config configuration
	if err != nil || json.Unmarshal(data, &config) != nil {
		fail(http.StatusInternalServerError, "Could not read default channel configuration.")
		return
	}
	message := ""
	if r.Method == http.MethodPost {
		var change struct {
			ChannelID string `json:"channel_id"`
			Action    string `json:"action"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&change); err != nil || change.ChannelID == "" || (change.Action != "set" && change.Action != "unset") {
			fail(http.StatusBadRequest, "Specify a channel and set or unset.")
			return
		}
		if !config.DefaultChannels_OnOffBool {
			fail(http.StatusConflict, "Enable and save the Default Channels module first.")
			return
		}
		channel, appErr := p.API.GetChannel(change.ChannelID)
		if appErr != nil {
			fail(appErr.StatusCode, "Could not load the channel.")
			return
		}
		if channel == nil || channel.TeamId == "" || (change.Action == "set" && !eligibleDefaultChannel(channel, channel.TeamId)) {
			fail(http.StatusBadRequest, "Choose an active public or private team channel.")
			return
		}
		if channel.Name == model.DefaultChannelName {
			fail(http.StatusBadRequest, "Town Square is managed by Mattermost, not this plugin.")
			return
		}
		// No requester/reply channel: console edits do not send ephemeral command replies.
		response, appErr := p.setDefaultChannel(&model.CommandArgs{TeamId: channel.TeamId, ChannelId: channel.Id}, change.Action == "set", settings, &config)
		if appErr != nil {
			fail(appErr.StatusCode, "Could not save default channels. Please try again.")
			return
		}
		message = response.Text
		// Return the committed value even if a later metadata lookup would fail.
		_ = json.NewEncoder(w).Encode(map[string]any{"value": config.DefaultChannels_Custom, "message": message})
		return
	}
	channels, appErr := p.defaultChannelsForConsole(&config)
	if appErr != nil {
		fail(appErr.StatusCode, "Could not load the hierarchy. Refresh to see the saved defaults.")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"channels": channels, "enabled": config.DefaultChannels_OnOffBool,
		"value": config.DefaultChannels_Custom, "message": message,
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
