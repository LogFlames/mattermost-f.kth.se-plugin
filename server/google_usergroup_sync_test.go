package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type usergroupSyncTestAPI struct {
	*googleGroupSyncTestAPI
	members    []*model.User
	added      []string
	removed    []string
	lookupFail bool
	listFail   bool
	addFail    bool
}

func (a *usergroupSyncTestAPI) GetGroupByName(name string) (*model.Group, *model.AppError) {
	if name != "target" {
		return nil, testAppError(http.StatusNotFound)
	}
	return &model.Group{Id: "group"}, nil
}

func (a *usergroupSyncTestAPI) GetUserByEmail(email string) (*model.User, *model.AppError) {
	if a.lookupFail {
		return nil, testAppError(http.StatusInternalServerError)
	}
	return a.googleGroupSyncTestAPI.GetUserByEmail(email)
}

func (a *usergroupSyncTestAPI) GetGroupMemberUsers(_ string, page, perPage int) ([]*model.User, *model.AppError) {
	if a.listFail {
		return nil, testAppError(http.StatusInternalServerError)
	}
	start := page * perPage
	if start >= len(a.members) {
		return nil, nil
	}
	end := start + perPage
	if end > len(a.members) {
		end = len(a.members)
	}
	return a.members[start:end], nil
}

func (a *usergroupSyncTestAPI) UpsertGroupMember(_, userID string) (*model.GroupMember, *model.AppError) {
	if a.addFail {
		return nil, testAppError(http.StatusInternalServerError)
	}
	a.added = append(a.added, userID)
	return &model.GroupMember{}, nil
}

func (a *usergroupSyncTestAPI) DeleteGroupMember(_, userID string) (*model.GroupMember, *model.AppError) {
	a.removed = append(a.removed, userID)
	return &model.GroupMember{}, nil
}

func TestValidateGoogleUsergroupSyncs(t *testing.T) {
	valid := googleUsergroupSync{Usergroup: "@target", GroupEmail: "group@fysiksektionen.se", ExcludedUsers: []string{model.NewId()}}
	for _, tc := range []struct {
		name  string
		syncs []googleUsergroupSync
		valid bool
	}{
		{"valid", []googleUsergroupSync{valid}, true},
		{"duplicate", []googleUsergroupSync{valid, valid}, false},
		{"missing mention", []googleUsergroupSync{{Usergroup: "target", GroupEmail: valid.GroupEmail}}, false},
		{"bad domain", []googleUsergroupSync{{Usergroup: "@target", GroupEmail: "group@example.org"}}, false},
		{"missing domain", []googleUsergroupSync{{Usergroup: "@target", GroupEmail: "group"}}, false},
		{"invalid user ID", []googleUsergroupSync{{Usergroup: "@target", GroupEmail: valid.GroupEmail, ExcludedUsers: []string{"bad"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGoogleUsergroupSyncs(tc.syncs); (err == nil) != tc.valid {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}

func TestGoogleUsergroupSync(t *testing.T) {
	googleFailure := false
	key := setupGoogleTestTransport(t, func(request *http.Request) (int, string) {
		if request.URL.Host == "oauth2.googleapis.com" {
			if err := request.ParseForm(); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(request.Form.Get("assertion"), ".")
			if len(parts) != 3 {
				t.Fatal("invalid JWT")
			}
			data, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var claims struct {
				Subject string `json:"sub"`
			}
			if err := json.Unmarshal(data, &claims); err != nil || claims.Subject != "admin@fysiksektionen.se" {
				t.Fatalf("delegation claims %s: %v", data, err)
			}
			return http.StatusOK, `{"access_token":"token"}`
		}
		switch request.URL.Path {
		case "/admin/directory/v1/groups/team@fysiksektionen.se":
			return http.StatusOK, `{"id":"google","email":"team@fysiksektionen.se"}`
		case "/admin/directory/v1/groups/google/members":
			if googleFailure {
				return http.StatusForbidden, `{}`
			}
			if request.URL.Query().Get("includeDerivedMembership") != "true" {
				t.Fatal("derived membership not requested")
			}
			return http.StatusOK, `{"members":[{"email":"kept@fysiksektionen.se","type":"USER"},{"email":"add@fysiksektionen.se","type":"USER"},{"email":"excluded@fysiksektionen.se","type":"USER"},{"email":"nested@fysiksektionen.se","type":"GROUP"}]}`
		default:
			t.Fatalf("unexpected request %s", request.URL)
			return http.StatusNotFound, `{}`
		}
	})
	config := &configuration{GoogleGroupsSyncDomain: "fysiksektionen.se", GoogleGroupsSyncAdminEmail: "admin@fysiksektionen.se", GoogleGroupsSyncServiceAccountKey: key}
	excluded := model.NewId()
	sync := googleUsergroupSync{Usergroup: "@target", GroupEmail: "team@fysiksektionen.se", ExcludedUsers: []string{excluded}}
	config.GoogleGroupsUsergroupSyncs = []googleUsergroupSync{sync}
	for _, failure := range []string{"", "google", "lookup", "list", "add"} {
		t.Run(failure, func(t *testing.T) {
			googleFailure = failure == "google"
			api := &usergroupSyncTestAPI{
				googleGroupSyncTestAPI: &googleGroupSyncTestAPI{categoryTestAPI: &categoryTestAPI{kv: map[string][]byte{}}, users: map[string]*model.User{
					"kept@fysiksektionen.se":     {Id: "kept"},
					"add@fysiksektionen.se":      {Id: "add"},
					"excluded@fysiksektionen.se": {Id: excluded},
				}},
				lookupFail: failure == "lookup", listFail: failure == "list", addFail: failure == "add",
			}
			for i := 0; i < 200; i++ {
				api.members = append(api.members, &model.User{Id: fmt.Sprintf("old-%d", i)})
			}
			api.members = append(api.members, &model.User{Id: "kept"}, &model.User{Id: excluded})
			p := &Plugin{configuration: config}
			p.SetAPI(api)
			err := p.syncGoogleUsergroup(context.Background(), config, sync, config.GoogleGroupsSyncAdminEmail)
			if failure != "" {
				if err == nil || len(api.removed) != 0 {
					t.Fatalf("failure %s: error=%v, removals=%v", failure, err, api.removed)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(api.added, []string{"add"}) || len(api.removed) != 201 {
				t.Fatalf("error=%v added=%v removed=%v", err, api.added, api.removed)
			}
			foundExcluded := false
			for _, id := range api.removed {
				if id == "kept" {
					t.Fatal("removed a matching member")
				}
				foundExcluded = foundExcluded || id == excluded
			}
			if !foundExcluded {
				t.Fatal("excluded member was not removed")
			}
			api.added = nil
			api.removed = nil
			p.runGoogleUsergroupSyncs(context.Background())
			if !reflect.DeepEqual(api.added, []string{"add"}) || len(api.removed) != 201 {
				t.Fatalf("scheduled sync: added=%v removed=%d", api.added, len(api.removed))
			}
			api.added = nil
			api.removed = nil
			continuousConfig := config.Clone()
			continuousConfig.GoogleGroupsSyncMode = "continuous"
			continuousConfig.GoogleGroupsSyncDelaySeconds = 10
			p.setConfiguration(continuousConfig)
			p.runGoogleGroupSyncJobs(context.Background())
			if !reflect.DeepEqual(api.added, []string{"add"}) || len(api.removed) != 201 {
				t.Fatalf("hourly usergroup sync in continuous mode: added=%v removed=%d", api.added, len(api.removed))
			}
		})
	}
}
