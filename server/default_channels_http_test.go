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
	for _, scenario := range []string{"anonymous", "team-admin", "member", "method", "malformed", "old-action", "town-square", "archived", "direct", "failed-config-save", "changed-elsewhere", "unconfigured-addition", "save-job"} {
		t.Run(scenario, func(t *testing.T) {
			p, base := newDefaultChannelTestPlugin(t)
			p.SetAPI(&defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base})
			base.settings["defaultchannels_custom"] = []string{"channel"}
			user, method, body := "system-admin", http.MethodPost, `{"save_id":"save-1","expected_channel_ids":["channel"],"added_channel_ids":["channel"]}`
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
			case "old-action":
				body = `{"channel_id":"channel","action":"set"}`
			case "town-square":
				base.channel.Name = model.DefaultChannelName
			case "archived":
				base.channel.DeleteAt = 1
			case "direct":
				base.channel.Type = model.ChannelTypeDirect
			case "failed-config-save":
				base.settings["defaultchannels_custom"] = []string{}
				want = http.StatusConflict
			case "changed-elsewhere":
				base.settings["defaultchannels_custom"] = []string{"channel", "other"}
				want = http.StatusConflict
			case "unconfigured-addition":
				body = `{"save_id":"save-1","expected_channel_ids":["channel"],"added_channel_ids":["other"]}`
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

func TestDefaultChannelsConsoleBackfillAfterSaveAndRetry(t *testing.T) {
	p, base := newDefaultChannelTestPlugin(t)
	api := &defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base}
	p.SetAPI(api)
	base.users = []*model.User{{Id: "alice"}, {Id: "bob"}}
	base.channel.Type = model.ChannelTypePrivate
	// The native console saves configuration before invoking the save action.
	base.settings["defaultchannels_custom"] = []string{"other", "channel"}
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	body := `{"save_id":"save-1","expected_channel_ids":["channel","other"],"added_channel_ids":["channel"]}`
	for i := 0; i < 2; i++ {
		w := consoleRequest(p, http.MethodPost, "system-admin", body)
		if w.Code != http.StatusOK || base.saves != 0 || len(base.added) != 0 || len(base.kv) != 1 {
			t.Fatalf("add was not queued/idempotent: %d %s", w.Code, w.Body)
		}
	}
	if got := p.getConfiguration().defaultChannelIDs(); !slices.Equal(got, []string{"other", "channel"}) {
		t.Fatalf("overwrote another team's defaults: %v", got)
	}
	p.runDefaultChannelJobs(context.Background())
	if !slices.Equal(base.added, []string{"channel:alice", "channel:bob"}) || len(base.kv) != 1 || len(base.reports) != 0 || string(base.kv[defaultChannelCompletedSavePrefix+"channel"]) != "save-1" {
		t.Fatalf("console backfill failed: added=%v jobs=%d reports=%d", base.added, len(base.kv), len(base.reports))
	}
	// A retry after completion (including after restart) must not run a second pass.
	restarted := &Plugin{pluginBot: p.pluginBot}
	restarted.SetAPI(api)
	if w := consoleRequest(restarted, http.MethodPost, "system-admin", body); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	restarted.runDefaultChannelJobs(context.Background())
	if len(base.added) != 2 || len(base.kv) != 1 || base.settings["UnrelatedSetting"] != "keep" {
		t.Fatal("retried save repeated a completed backfill or changed configuration")
	}
	// A new save after unsetting/re-setting the channel starts a fresh pass.
	if w := consoleRequest(p, http.MethodPost, "system-admin", strings.ReplaceAll(body, "save-1", "save-2")); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	key := defaultChannelJobPrefix + "bulk_channel"
	var job defaultChannelJob
	if err := json.Unmarshal(base.kv[key], &job); err != nil {
		t.Fatal(err)
	}
	job.Page = 3
	if err := p.saveDefaultChannelJob(key, &job); err != nil {
		t.Fatal(err)
	}
	w := consoleRequest(p, http.MethodPost, "system-admin", strings.ReplaceAll(body, "save-1", "save-2"))
	if w.Code != http.StatusOK || json.Unmarshal(base.kv[key], &job) != nil || job.Page != 3 {
		t.Fatal("retry reset in-progress pagination")
	}
}

func TestDefaultChannelsConsoleSaveDoesNotBackfill(t *testing.T) {
	p, base := newDefaultChannelTestPlugin(t)
	p.SetAPI(&defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base})
	base.settings["defaultchannels_custom"] = []string{"channel", "channel"}
	if err := p.OnConfigurationChange(); err != nil {
		t.Fatal(err)
	}
	w := consoleRequest(p, http.MethodGet, "system-admin", "")
	var result map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("GET failed: %d %s", w.Code, w.Body)
	}
	value, err := json.Marshal(result["value"])
	if err != nil || string(value) != `["channel"]` || base.saves != 0 || len(base.kv) != 0 {
		t.Fatalf("GET must normalize without saving/queuing jobs: %s", w.Body)
	}
	if !slices.Equal(base.settings["defaultchannels_custom"].([]string), []string{"channel", "channel"}) {
		t.Fatal("reading config changed persisted settings")
	}
	// Simulate the enclosing console's Save using the staged API value.
	base.settings["defaultchannels_custom"] = result["value"]
	if err := base.SavePluginConfig(base.settings); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.getConfiguration().defaultChannelIDs(), []string{"channel"}) || len(base.kv) != 0 || len(base.added) != 0 {
		t.Fatal("saving unchanged defaults queued a backfill or changed channel IDs")
	}
	w = consoleRequest(p, http.MethodPost, "system-admin", `{"save_id":"no-additions","expected_channel_ids":["channel"],"added_channel_ids":[]}`)
	if w.Code != http.StatusOK || len(base.kv) != 0 {
		t.Fatal("save without additions queued a backfill")
	}
	defaultCommand(t, p, "set")
	if base.saves != 1 || len(base.kv) != 0 {
		t.Fatal("re-setting an existing default queued another backfill")
	}
}

func TestDefaultChannelsConsoleUsesLiveTeamAndCategory(t *testing.T) {
	p, base := newDefaultChannelTestPlugin(t)
	p.SetAPI(&defaultChannelsHTTPTestAPI{defaultChannelTestAPI: base})
	base.channels["b"] = model.Channel{Id: "b", TeamId: "other", Name: "private", Type: model.ChannelTypePrivate}
	base.channels["town"] = model.Channel{Id: "town", TeamId: "team", Name: model.DefaultChannelName, Type: model.ChannelTypeOpen}
	base.settings["defaultchannels_custom"] = []string{"channel", "b", "channel"}
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
