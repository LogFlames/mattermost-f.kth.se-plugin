package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type adminTestAPI struct {
	*defaultChannelTestAPI
	channelPermission bool
	systemAdmin       bool
	membershipError   bool
}

func newAdminTestPlugin(t *testing.T) (*Plugin, *adminTestAPI) {
	t.Helper()
	p, base := newDefaultChannelTestPlugin(t)
	api := &adminTestAPI{defaultChannelTestAPI: base, channelPermission: true, systemAdmin: true}
	p.SetAPI(api)
	api.users = []*model.User{{Id: "admin", Username: "admin"}, {Id: "existing", Username: "existing"}, {Id: "new", Username: "new"}, {Id: "deleted", Username: "deleted", DeleteAt: 1}}
	api.members = model.ChannelMembers{{UserId: "existing", ChannelId: "channel"}}
	return p, api
}

func (a *adminTestAPI) LogInfo(string, ...any) {}
func (a *adminTestAPI) KVSetWithExpiry(key string, value []byte, _ int64) *model.AppError {
	return a.KVSet(key, value)
}
func (a *adminTestAPI) HasPermissionTo(userID string, permission *model.Permission) bool {
	return (userID == "admin" || userID == "other-admin") && a.systemAdmin && permission == model.PermissionManageSystem
}
func (a *adminTestAPI) HasPermissionToChannel(userID, channelID string, permission *model.Permission) bool {
	return userID == "admin" && a.channelPermission && channelID == "channel" &&
		(permission == model.PermissionManagePublicChannelMembers || permission == model.PermissionManagePrivateChannelMembers)
}
func (a *adminTestAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	return &model.Team{Id: id, DisplayName: "Fysiksektionen"}, nil
}
func (a *adminTestAPI) GetTeams() ([]*model.Team, *model.AppError) {
	return []*model.Team{{Id: "team", DisplayName: "Fysiksektionen"}}, nil
}
func (a *adminTestAPI) GetTeamsForUser(string) ([]*model.Team, *model.AppError) {
	return a.GetTeams()
}
func (a *adminTestAPI) GetChannelsForTeamForUser(string, string, bool) ([]*model.Channel, *model.AppError) {
	return []*model.Channel{&a.channel}, nil
}
func (a *adminTestAPI) GetUserByEmail(string) (*model.User, *model.AppError) {
	return nil, testAppError(http.StatusNotFound)
}
func (a *adminTestAPI) GetUserByUsername(string) (*model.User, *model.AppError) {
	return nil, testAppError(http.StatusNotFound)
}
func (a *adminTestAPI) AddChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	return a.AddUserToChannel(channelID, userID, "bot")
}
func (a *adminTestAPI) GetChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	if a.membershipError {
		return nil, testAppError(http.StatusServiceUnavailable)
	}
	return a.categoryTestAPI.GetChannelMember(channelID, userID)
}
func (a *adminTestAPI) AddUserToChannel(channelID, userID, actorID string) (*model.ChannelMember, *model.AppError) {
	if actorID != "admin" && actorID != "bot" {
		panic("unexpected actor")
	}
	if userID == a.failAdd {
		return nil, testAppError(http.StatusServiceUnavailable)
	}
	if member, err := a.GetChannelMember(channelID, userID); err == nil {
		return member, nil
	}
	a.added = append(a.added, channelID+":"+userID)
	member := &model.ChannelMember{ChannelId: channelID, UserId: userID}
	a.members = append(a.members, *member)
	return member, nil
}

func adminHTTP(t *testing.T, p *Plugin, user, endpoint string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/"+endpoint, strings.NewReader(string(data)))
	r.Header.Set("Mattermost-User-Id", user)
	w := httptest.NewRecorder()
	p.ServeHTTP(nil, w, r)
	return w
}

func adminPreviewHTTP(t *testing.T, p *Plugin, input adminInput) adminPreview {
	t.Helper()
	w := adminHTTP(t, p, "admin", "preview", input)
	if w.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", w.Code, w.Body)
	}
	var preview adminPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestAdminDashboardRequiresSystemAdmin(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	input := adminInput{Action: "mass_add", TeamID: "team", ChannelID: "channel"}
	preview := adminPreviewHTTP(t, p, input)
	api.systemAdmin = false
	for _, user := range []string{"", "ordinary-user", "admin"} {
		for _, endpoint := range []string{"options", "preview", "execute"} {
			t.Run(user+"/"+endpoint, func(t *testing.T) {
				body := any(input)
				if endpoint == "execute" {
					body = map[string]string{"preview_id": preview.ID}
				}
				data, _ := json.Marshal(body)
				method := http.MethodPost
				if endpoint == "options" {
					method = http.MethodGet
				}
				r := httptest.NewRequest(method, "/admin/"+endpoint, strings.NewReader(string(data)))
				r.Header.Set("Mattermost-User-Id", user)
				w := httptest.NewRecorder()
				p.ServeHTTP(nil, w, r)
				want := http.StatusForbidden
				if user == "" {
					want = http.StatusUnauthorized
				}
				if w.Code != want || len(api.added) != 0 || api.saves != 0 || len(api.kv) != 1 {
					t.Fatalf("dashboard accessible without system admin permission: %d %s", w.Code, w.Body)
				}
			})
		}
	}
	api.systemAdmin = true
	w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
	if w.Code != http.StatusOK || !slices.Equal(api.added, []string{"channel:new"}) {
		t.Fatalf("system admin could not apply reviewed changes: %d %s", w.Code, w.Body)
	}
}

func TestAdminPreviewRequiresFreshSingleUseConfirmation(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	input := adminInput{Action: "mass_add", TeamID: "team", ChannelID: "channel"}
	preview := adminPreviewHTTP(t, p, input)
	if len(preview.Changes) != 1 || preview.Changes[0].UserID != "new" || len(api.added) != 0 {
		t.Fatalf("preview must exclude existing, deleted and requesting users without adding members: %+v, added=%v", preview, api.added)
	}
	confirm := map[string]string{"preview_id": preview.ID}
	if w := adminHTTP(t, p, "other-admin", "execute", confirm); w.Code != http.StatusConflict || len(api.added) != 0 {
		t.Fatalf("another user applied preview: %d %s", w.Code, w.Body)
	}
	api.users = append(api.users, &model.User{Id: "late", Username: "late"})
	if w := adminHTTP(t, p, "admin", "execute", confirm); w.Code != http.StatusConflict || len(api.added) != 0 {
		t.Fatalf("stale scope applied: %d %s", w.Code, w.Body)
	}
	preview = adminPreviewHTTP(t, p, input)
	confirm["preview_id"] = preview.ID
	api.channelPermission = false
	if w := adminHTTP(t, p, "admin", "execute", confirm); w.Code != http.StatusForbidden || len(api.added) != 0 {
		t.Fatalf("revoked permission applied: %d %s", w.Code, w.Body)
	}
	api.channelPermission = true
	api.failAdd = "late"
	w := adminHTTP(t, p, "admin", "execute", confirm)
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != http.StatusOK || result["completed"] != float64(1) || result["failed"] != float64(1) || !slices.Equal(api.added, []string{"channel:new"}) {
		t.Fatalf("wrong partial result: %d %s, added=%v", w.Code, w.Body, api.added)
	}
	if w := adminHTTP(t, p, "admin", "execute", confirm); w.Code != http.StatusConflict || len(api.added) != 1 {
		t.Fatalf("confirmation replayed: %d %s", w.Code, w.Body)
	}
}

func TestAdminPreviewAuthorizationAndExpiry(t *testing.T) {
	for _, action := range []string{"categories", "internationals", "default_set", "default_unset", "mass_add", "keycloak_new_user", "migrate_to_keycloak"} {
		t.Run(action, func(t *testing.T) {
			p, api := newAdminTestPlugin(t)
			input := adminInput{Action: action, TeamID: "team", ChannelID: "channel"}
			for _, user := range []string{"", "ordinary-user"} {
				w := adminHTTP(t, p, user, "preview", input)
				if w.Code < 400 || len(api.kv) != 0 || api.saves != 0 || len(api.added) != 0 {
					t.Fatalf("unauthorized preview: %d %s", w.Code, w.Body)
				}
			}
		})
	}
	p, api := newAdminTestPlugin(t)
	preview := adminPreviewHTTP(t, p, adminInput{Action: "mass_add", TeamID: "team", ChannelID: "channel"})
	var plan adminPlan
	_ = json.Unmarshal(api.kv[adminPreviewPrefix+preview.ID], &plan)
	plan.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	api.kv[adminPreviewPrefix+preview.ID], _ = json.Marshal(plan)
	if w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID}); w.Code != http.StatusConflict || len(api.added) != 0 {
		t.Fatalf("expired preview applied: %d %s", w.Code, w.Body)
	}
	api.membershipError = true
	if w := adminHTTP(t, p, "admin", "preview", adminInput{Action: "mass_add", TeamID: "team", ChannelID: "channel"}); w.Code != http.StatusInternalServerError || len(api.added) != 0 {
		t.Fatalf("failed membership lookup treated as absence: %d %s", w.Code, w.Body)
	}
}

func TestAdminDefaultChannelPreviewAndBackgroundAddition(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	preview := adminPreviewHTTP(t, p, adminInput{Action: "default_set", TeamID: "team", ChannelID: "channel"})
	if len(preview.Changes) != 3 || api.saves != 0 || len(api.added) != 0 {
		t.Fatalf("wrong default preview or writes before confirmation: %+v", preview)
	}
	w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
	if w.Code != http.StatusOK || api.saves != 1 || len(api.added) != 0 {
		t.Fatalf("default was not saved and queued: %d %s", w.Code, w.Body)
	}
	p.runDefaultChannelJobs(context.Background())
	if !slices.Equal(api.added, []string{"channel:admin", "channel:new"}) {
		t.Fatalf("background addition missed reviewed users: %v", api.added)
	}
	preview = adminPreviewHTTP(t, p, adminInput{Action: "default_unset", TeamID: "team", ChannelID: "channel"})
	if len(preview.Changes) != 1 || preview.Changes[0].Before != "Default channel" {
		t.Fatalf("wrong removal preview: %+v", preview)
	}
	w = adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
	if w.Code != http.StatusOK || len(p.getConfiguration().defaultChannelIDs()) != 0 || len(api.members) != 3 {
		t.Fatalf("unset removed members or failed: %d %s", w.Code, w.Body)
	}
}

func TestAdminCategoryPreviewKeepsReviewedScope(t *testing.T) {
	for _, changedDefault := range []bool{false, true} {
		t.Run(fmt.Sprint(changedDefault), func(t *testing.T) {
			p, base := newCategoryTestPlugin(t)
			base.adminTeam = "team"
			api := &adminTestAPI{systemAdmin: true, defaultChannelTestAPI: &defaultChannelTestAPI{categoryTestAPI: base, p: p, settings: map[string]any{}, left: map[string]bool{}, users: []*model.User{{Id: "user", Username: "elsa"}, {Id: "late", Username: "late"}, base.user}}}
			p.SetAPI(api)
			base.searchChannels = func(*model.ChannelSearch) (*model.ChannelsWithCount, *model.AppError) {
				return &model.ChannelsWithCount{Channels: model.ChannelListWithTeamData{&model.ChannelWithTeamData{Channel: base.channel}}, TotalCount: 1}, nil
			}
			base.categories["user"] = []*model.SidebarCategoryWithChannels{{SidebarCategory: model.SidebarCategory{Id: "favorites", Type: model.SidebarCategoryFavorites, DisplayName: "Favorites"}, Channels: []string{"channel"}}}
			base.categories["late"] = []*model.SidebarCategoryWithChannels{{SidebarCategory: model.SidebarCategory{Id: "late-favorites", Type: model.SidebarCategoryFavorites, DisplayName: "Favorites"}, Channels: []string{"channel"}}}
			preview := adminPreviewHTTP(t, p, adminInput{Action: "categories", TeamID: "team"})
			if len(preview.Changes) != 1 || preview.Changes[0].Before != "Favorites" || preview.Changes[0].After != "New" || base.updates != 0 || base.created != 0 {
				t.Fatalf("wrong category preview or premature write: %+v", preview)
			}
			w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
			if w.Code != http.StatusOK {
				t.Fatalf("queue failed: %d %s", w.Code, w.Body)
			}
			base.members = append(base.members, model.ChannelMember{UserId: "late", ChannelId: "channel"})
			if changedDefault {
				base.channel.DefaultCategoryName = "Different"
			}
			p.runCategoryJobs(context.Background())
			wantUpdates := 1
			if changedDefault {
				wantUpdates = 0
			}
			if base.updates != wantUpdates || !slices.Equal(base.categories["late"][0].Channels, []string{"channel"}) {
				t.Fatalf("worker expanded reviewed scope: updates=%d late=%+v", base.updates, base.categories["late"])
			}
		})
	}
}

func TestAdminInternationalPreviewPaginatesAndChecksTargets(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	api.users = nil
	for i := range 102 {
		api.users = append(api.users, &model.User{Id: fmt.Sprintf("user-%03d", i), Username: fmt.Sprintf("member-%03d", i)})
	}
	api.settings["internationalstudents_defaultchannels"] = []string{"channel", "channel"}
	api.settings["internationalstudents_users"] = []string{"user-000", "user-100", "left-the-team"}
	preview := adminPreviewHTTP(t, p, adminInput{Action: "internationals", TeamID: "team"})
	if len(preview.Changes) != 2 || preview.Changes[0].UserID != "user-000" || preview.Changes[1].UserID != "user-100" || len(api.added) != 0 {
		t.Fatalf("wrong tracked member scope: %+v", preview)
	}
	w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
	if w.Code != http.StatusOK || !slices.Equal(api.added, []string{"channel:user-000", "channel:user-100"}) {
		t.Fatalf("wrong additions: %d %s %v", w.Code, w.Body, api.added)
	}
	api.channel.TeamId = "other-team"
	api.channelPermission = false
	w = adminHTTP(t, p, "admin", "preview", adminInput{Action: "internationals", TeamID: "team"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-team target was not authorized: %d %s", w.Code, w.Body)
	}
}

func TestAdminKeycloakPreviewDoesNotCreateOrStorePassword(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	api.systemAdmin = true
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})
	created := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"access_token":"test"}`))
		} else if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
		} else if r.Method == http.MethodPost {
			var account keycloakNewUserRequest
			if err := json.NewDecoder(r.Body).Decode(&account); err != nil {
				t.Error(err)
			}
			if account.Email != "test@example.org" || account.Attributes["mattermostUsername"][0] != "test-user" || !account.EmailVerified || !account.Enabled || len(account.Credentials) != 1 || !account.Credentials[0].Temporary || account.Credentials[0].Value != "temporary-test-password" {
				t.Error("wrong Keycloak account payload")
			}
			created++
			w.WriteHeader(http.StatusCreated)
		} else {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(server.Close)
	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })
	preview := adminPreviewHTTP(t, p, adminInput{Action: "keycloak_new_user", Email: "test@example.org", Username: "test-user", FirstName: "Test", LastName: "User"})
	if created != 0 || len(preview.Changes) != 1 {
		t.Fatalf("preview created an account or returned no account: %+v", preview)
	}
	for _, value := range api.kv {
		if strings.Contains(string(value), "password") || strings.Contains(string(value), "test-secret") {
			t.Fatal("credentials stored with the preview")
		}
	}
	w := adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID})
	if w.Code != http.StatusBadRequest || created != 0 {
		t.Fatalf("account creation without password: %d %s", w.Code, w.Body)
	}
	w = adminHTTP(t, p, "admin", "execute", map[string]string{"preview_id": preview.ID, "password": "temporary-test-password"})
	if w.Code != http.StatusOK || created != 1 || strings.Contains(w.Body.String(), "temporary-test-password") {
		t.Fatalf("creation failed or exposed password: %d %s", w.Code, w.Body)
	}
}
