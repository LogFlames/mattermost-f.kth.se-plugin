package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type googleGroupSyncConsoleEntry struct {
	googleGroupSync
	TeamName       string `json:"team_name"`
	ChannelName    string `json:"channel_name"`
	ChannelDisplay string `json:"channel_display_name"`
	ChannelDeleted bool   `json:"channel_deleted"`
	CreatorName    string `json:"creator_name"`
	NextSyncAt     int64  `json:"next_sync_at"`
	SyncMode       string `json:"sync_mode,omitempty"`
}

type googleGroupSyncOverview struct {
	Syncs []googleGroupSyncConsoleEntry `json:"syncs"`
	Cycle string                        `json:"cycle"`
}

func (p *Plugin) serveGoogleGroupSyncOverview(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		fail(http.StatusUnauthorized, "Sign in to view Google Group syncs.")
		return
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		fail(http.StatusForbidden, "Only system admins can view Google Group syncs across teams.")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	overview, err := p.googleGroupSyncsForConsole()
	if err != nil {
		p.API.LogError("Load Google Group sync overview failed", "error", err.Error())
		fail(http.StatusInternalServerError, "Could not load Google Group syncs. Check server logs and try again.")
		return
	}
	_ = json.NewEncoder(w).Encode(overview)
}

func (p *Plugin) googleGroupSyncsForConsole() (*googleGroupSyncOverview, error) {
	records, err := p.googleGroupSyncRecords()
	if err != nil {
		return nil, err
	}
	teams := make(map[string]*model.Team)
	channels := make(map[string]*model.Channel)
	users := make(map[string]*model.User)
	entries := make([]googleGroupSyncConsoleEntry, 0, len(records))
	now := time.Now()
	config := p.getConfiguration()
	cycle, _, err := googleGroupSyncCycleDescription(records, config, now)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		job := record.Job
		if _, ok := teams[job.TeamID]; !ok && job.TeamID != "" {
			team, appErr := p.API.GetTeam(job.TeamID)
			if appErr != nil && appErr.StatusCode != http.StatusNotFound {
				return nil, fmt.Errorf("load team %q: %s", job.TeamID, appErr.Error())
			}
			if appErr == nil && team == nil {
				return nil, fmt.Errorf("load team %q: empty response", job.TeamID)
			}
			teams[job.TeamID] = team
		}
		if _, ok := channels[job.ChannelID]; !ok && job.ChannelID != "" {
			channel, appErr := p.API.GetChannel(job.ChannelID)
			if appErr != nil && appErr.StatusCode != http.StatusNotFound {
				return nil, fmt.Errorf("load channel %q: %s", job.ChannelID, appErr.Error())
			}
			if appErr == nil && channel == nil {
				return nil, fmt.Errorf("load channel %q: empty response", job.ChannelID)
			}
			channels[job.ChannelID] = channel
		}
		if _, ok := users[job.CreatedBy]; !ok && job.CreatedBy != "" {
			user, appErr := p.API.GetUser(job.CreatedBy)
			if appErr != nil && appErr.StatusCode != http.StatusNotFound {
				return nil, fmt.Errorf("load sync creator %q: %s", job.CreatedBy, appErr.Error())
			}
			if appErr == nil && user == nil {
				return nil, fmt.Errorf("load sync creator %q: empty response", job.CreatedBy)
			}
			users[job.CreatedBy] = user
		}
		entry := googleGroupSyncConsoleEntry{
			googleGroupSync: job,
		}
		if config.GoogleGroupsSyncMode == "continuous" {
			entry.SyncMode = "continuous"
		} else {
			entry.NextSyncAt = googleGroupNextSyncAt(job, now)
		}
		if team := teams[job.TeamID]; team != nil {
			entry.TeamName = team.DisplayName
		}
		if channel := channels[job.ChannelID]; channel != nil {
			entry.ChannelName = channel.Name
			entry.ChannelDisplay = channel.DisplayName
			entry.ChannelDeleted = channel.DeleteAt != 0
		}
		if user := users[job.CreatedBy]; user != nil {
			entry.CreatorName = user.Username
		}
		entries = append(entries, entry)
	}
	return &googleGroupSyncOverview{Syncs: entries, Cycle: cycle}, nil
}
