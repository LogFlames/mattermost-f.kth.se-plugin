package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type defaultChannelsHTTPTestAPI struct {
	*defaultChannelTestAPI
	failTeam bool
}

func (a *defaultChannelsHTTPTestAPI) HasPermissionTo(userID string, permission *model.Permission) bool {
	return userID == "system-admin" && permission == model.PermissionManageSystem
}

func (a *defaultChannelsHTTPTestAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	if a.failTeam {
		return nil, testAppError(http.StatusServiceUnavailable)
	}
	return &model.Team{Id: id, Name: id, DisplayName: "Display " + id}, nil
}

func consoleRequest(p *Plugin, method, user, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/default-channels", strings.NewReader(body))
	r.Header.Set("Mattermost-User-Id", user)
	w := httptest.NewRecorder()
	p.ServeHTTP(nil, w, r)
	return w
}

func TestDefaultChannelsConsoleAuthorizationAndValidation(t *testing.T) {
	for _, scenario := range []string{"anonymous", "team-admin", "member", "method", "malformed", "action", "disabled", "town-square", "archived", "direct", "save-config", "save-job"} {
		t.Run(scenario, func(t *testing.T) {
			p, base := newDefaultChannelTestPlugin(t)
			p.SetAPI(&defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base})
			user, method, body := "system-admin", http.MethodPost, `{"channel_id":"channel","action":"set"}`
			want := http.StatusBadRequest
			switch scenario {
			case "anonymous":
				user, want = "", http.StatusUnauthorized
			case "team-admin":
				user, want = "admin", http.StatusForbidden
			case "member":
				user, method, want = "member", http.MethodGet, http.StatusForbidden
			case "method":
				method, want = http.MethodDelete, http.StatusMethodNotAllowed
			case "malformed":
				body = `{`
			case "action":
				body = `{"channel_id":"channel","action":"other"}`
			case "disabled":
				base.settings["defaultchannels_onoffbool"] = false
				want = http.StatusConflict
			case "town-square":
				base.channel.Name = model.DefaultChannelName
			case "archived":
				base.channel.DeleteAt = 1
			case "direct":
				base.channel.Type = model.ChannelTypeDirect
			case "save-config":
				base.failConfig, want = true, http.StatusServiceUnavailable
			case "save-job":
				base.failSave, want = true, http.StatusInternalServerError
			}
			w := consoleRequest(p, method, user, body)
			if w.Code != want || base.saves != 0 || len(base.added) != 0 || len(base.kv) != 0 {
				t.Fatalf("status=%d body=%s saves=%d jobs=%d", w.Code, w.Body, base.saves, len(base.kv))
			}
		})
	}
}

func TestDefaultChannelsConsoleSharesCommandJobs(t *testing.T) {
	p, base := newDefaultChannelTestPlugin(t)
	api := &defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base}
	p.SetAPI(api)
	base.users = []*model.User{{Id: "alice"}, {Id: "bob"}}
	base.channel.Type = model.ChannelTypePrivate
	base.settings["defaultchannels_custom"] = []defaultChannelEntry{{String1: "Old category", ChannelIDs: []string{"other"}}}
	for i := 0; i < 2; i++ {
		w := consoleRequest(p, http.MethodPost, "system-admin", `{"channel_id":"channel","action":"set"}`)
		if w.Code != http.StatusOK || base.saves != 1 || len(base.added) != 0 {
			t.Fatalf("add was not queued/idempotent: %d %s", w.Code, w.Body)
		}
	}
	if got := p.getConfiguration().defaultChannelIDs(); !slices.Equal(got, []string{"other", "channel"}) {
		t.Fatalf("overwrote another team's defaults: %v", got)
	}
	p.runDefaultChannelJobs(context.Background())
	if !slices.Equal(base.added, []string{"channel:alice", "channel:bob"}) || len(base.kv) != 0 || len(base.reports) != 0 {
		t.Fatalf("console backfill failed: added=%v jobs=%d reports=%d", base.added, len(base.kv), len(base.reports))
	}
	// Removing an archived default must still work, without removing its members.
	base.channel.DeleteAt = 1
	api.failTeam = true // A metadata outage must not turn a committed edit into an apparent failure.
	w := consoleRequest(p, http.MethodPost, "system-admin", `{"channel_id":"channel","action":"unset"}`)
	if w.Code != http.StatusOK || !slices.Equal(p.getConfiguration().defaultChannelIDs(), []string{"other"}) || len(base.added) != 2 {
		t.Fatalf("unset failed: %d %s", w.Code, w.Body)
	}
	if base.settings["UnrelatedSetting"] != "keep" || p.getConfiguration().DefaultChannels_Custom[0].String1 != "Old category" {
		t.Fatal("damaged unrelated or legacy configuration")
	}
}

func TestDefaultChannelsConsoleUsesLiveTeamAndCategory(t *testing.T) {
	p, base := newDefaultChannelTestPlugin(t)
	p.SetAPI(&defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base})
	base.channels["b"] = model.Channel{Id: "b", TeamId: "other", Name: "private", Type: model.ChannelTypePrivate}
	base.channels["town"] = model.Channel{Id: "town", TeamId: "team", Name: model.DefaultChannelName, Type: model.ChannelTypeOpen}
	base.settings["defaultchannels_custom"] = []defaultChannelEntry{{String1: "Ignored label", ChannelIDs: []string{"channel", "b", "channel"}}}
	for _, category := range []string{"News", "Updated category"} {
		base.channel.DefaultCategoryName = category
		w := consoleRequest(p, http.MethodGet, "system-admin", "")
		var result struct {
			Channels []*model.ChannelWithTeamData `json:"channels"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Channels) != 3 {
			t.Fatalf("bad hierarchy response: %d %s", w.Code, w.Body)
		}
		byID := map[string]*model.ChannelWithTeamData{}
		for _, channel := range result.Channels {
			byID[channel.Id] = channel
		}
		if byID["channel"].DefaultCategoryName != category || byID["channel"].TeamDisplayName != "Display team" ||
			byID["b"].TeamDisplayName != "Display other" || byID["b"].DefaultCategoryName != "" || byID["town"].Name != model.DefaultChannelName {
			t.Fatalf("wrong team/category metadata: %s", w.Body)
		}
	}
}
