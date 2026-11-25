package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type googleSyncConsoleTestAPI struct {
	*googleGroupSyncTestAPI
	teams    map[string]*model.Team
	failList bool
}

func (a *googleSyncConsoleTestAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	if team := a.teams[id]; team != nil {
		return team, nil
	}
	return nil, testAppError(http.StatusNotFound)
}

func (a *googleSyncConsoleTestAPI) KVList(page, perPage int) ([]string, *model.AppError) {
	if a.failList {
		return nil, testAppError(http.StatusInternalServerError)
	}
	return a.categoryTestAPI.KVList(page, perPage)
}

func googleSyncConsoleRequest(p *Plugin, method, user string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/google-groups/syncs", nil)
	r.Header.Set("Mattermost-User-Id", user)
	w := httptest.NewRecorder()
	p.ServeHTTP(nil, w, r)
	return w
}

func TestGoogleSyncConsoleAuthorization(t *testing.T) {
	api := &googleSyncConsoleTestAPI{googleGroupSyncTestAPI: &googleGroupSyncTestAPI{
		categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}},
		systemAdmins:    map[string]bool{"system": true},
	}}
	p := &Plugin{}
	p.SetAPI(api)
	for _, tc := range []struct {
		method, user string
		status       int
	}{
		{http.MethodGet, "", http.StatusUnauthorized},
		{http.MethodGet, "admin", http.StatusForbidden},
		{http.MethodPost, "system", http.StatusMethodNotAllowed},
		{http.MethodGet, "system", http.StatusOK},
	} {
		w := googleSyncConsoleRequest(p, tc.method, tc.user)
		if w.Code != tc.status {
			t.Errorf("%s %q: status %d, body %s", tc.method, tc.user, w.Code, w.Body)
		}
		if tc.status == http.StatusOK && !strings.Contains(w.Body.String(), `"syncs":[]`) {
			t.Errorf("empty list should be an array: %s", w.Body)
		}
	}
}

func TestGoogleSyncConsoleReportsStoredSyncs(t *testing.T) {
	lastSyncedAt := time.Now().UnixMilli()
	job := googleGroupSync{
		GroupEmail: "group@example.org", GroupName: "Example group", TeamID: "team", ChannelID: "channel",
		CreatedBy: "creator", ActingEmail: "actor@example.org", DeadlineAt: lastSyncedAt + 2*googleGroupSyncEvery.Milliseconds(), LastSyncedAt: lastSyncedAt,
	}
	missing := googleGroupSync{GroupEmail: "missing@example.org", TeamID: "missing-team", ChannelID: "missing-channel", CreatedBy: "missing-user"}
	data, _ := json.Marshal(job)
	missingData, _ := json.Marshal(missing)
	base := &categoryTestAPI{
		kv: map[string][]byte{
			googleGroupSyncKey(job.TeamID, job.ChannelID, job.GroupEmail):             data,
			googleGroupSyncKey(missing.TeamID, missing.ChannelID, missing.GroupEmail): missingData,
			googleGroupConfirmationPrefix + "other":                                   []byte("not a sync"),
		},
		channel: model.Channel{Id: "channel", Name: "general", DisplayName: "General", TeamId: "team", DeleteAt: 1},
	}
	api := &googleSyncConsoleTestAPI{
		googleGroupSyncTestAPI: &googleGroupSyncTestAPI{
			categoryTestAPI: base, systemAdmins: map[string]bool{"system": true},
			usersByID: map[string]*model.User{"creator": {Id: "creator", Username: "alice"}},
		},
		teams: map[string]*model.Team{"team": {Id: "team", DisplayName: "Physics"}},
	}
	p := &Plugin{}
	p.SetAPI(api)
	w := googleSyncConsoleRequest(p, http.MethodGet, "system")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var result struct {
		Syncs []googleGroupSyncConsoleEntry `json:"syncs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Syncs) != 2 {
		t.Fatalf("expected both stored syncs, including expired: %+v", result.Syncs)
	}
	for _, entry := range result.Syncs {
		switch entry.GroupEmail {
		case job.GroupEmail:
			if entry.googleGroupSync != job || entry.TeamName != "Physics" || entry.ChannelName != "general" ||
				entry.ChannelDisplay != "General" || !entry.ChannelDeleted || entry.CreatorName != "alice" ||
				entry.NextSyncAt != lastSyncedAt+googleGroupSyncEvery.Milliseconds() {
				t.Fatalf("incorrect sync details: %+v", entry)
			}
		case missing.GroupEmail:
			if entry.TeamID != missing.TeamID || entry.ChannelID != missing.ChannelID || entry.CreatedBy != missing.CreatedBy ||
				entry.TeamName != "" || entry.ChannelName != "" || entry.CreatorName != "" {
				t.Fatalf("missing references not preserved: %+v", entry)
			}
		default:
			t.Fatalf("unexpected sync: %+v", entry)
		}
	}
	api.failList = true
	if w = googleSyncConsoleRequest(p, http.MethodGet, "system"); w.Code != http.StatusInternalServerError {
		t.Fatalf("list failure not reported: %d %s", w.Code, w.Body)
	}
	api.failList = false
	base.failChannel = "channel"
	if w = googleSyncConsoleRequest(p, http.MethodGet, "system"); w.Code != http.StatusInternalServerError {
		t.Fatalf("channel lookup failure not reported: %d %s", w.Code, w.Body)
	}
	base.failChannel = ""
	base.kv[googleGroupSyncPrefix+"bad"] = []byte("{")
	if w = googleSyncConsoleRequest(p, http.MethodGet, "system"); w.Code != http.StatusInternalServerError {
		t.Fatalf("malformed record not reported: %d %s", w.Code, w.Body)
	}
}
