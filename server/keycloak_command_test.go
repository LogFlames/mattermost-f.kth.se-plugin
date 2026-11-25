package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

type keycloakRegistrationTestAPI struct {
	*adminTestAPI
	command *model.Command
}

type keycloakIdentityTestAPI struct {
	*adminTestAPI
	authUpdates int
	failUpdate  bool
}

func (a *keycloakIdentityTestAPI) UpdateUserAuth(userID string, auth *model.UserAuth) (*model.UserAuth, *model.AppError) {
	a.authUpdates++
	if a.failUpdate {
		return nil, testAppError(http.StatusInternalServerError)
	}
	return a.adminTestAPI.UpdateUserAuth(userID, auth)
}

func (a *keycloakIdentityTestAPI) GetUsers(_ *model.UserGetOptions) ([]*model.User, *model.AppError) {
	return a.users, nil
}

func (a *keycloakIdentityTestAPI) GetDirectChannel(_, _ string) (*model.Channel, *model.AppError) {
	return nil, testAppError(http.StatusInternalServerError)
}

func (a *keycloakIdentityTestAPI) LogError(string, ...any) {}

type keycloakBanTestAPI struct {
	*adminTestAPI
	activeUpdates []struct {
		userID string
		active bool
	}
	activeErr *model.AppError
	adminErr  *model.AppError
	postErr   *model.AppError
	posts     []*model.Post
	logErrors []string
}

type keycloakDeleteEmailTestAPI struct {
	*keycloakBanTestAPI
	lookups int
}

func (a *keycloakDeleteEmailTestAPI) GetUserByEmail(email string) (*model.User, *model.AppError) {
	a.lookups++
	return a.adminTestAPI.GetUserByEmail(email)
}

type keycloakNotificationTestAPI struct {
	*adminTestAPI
	posts []*model.Post
}

func (a *keycloakNotificationTestAPI) GetDirectChannel(_, _ string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: "dm"}, nil
}

func (a *keycloakNotificationTestAPI) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	post.Id = "post"
	a.posts = append(a.posts, post)
	return post, nil
}

func (a *keycloakBanTestAPI) GetUser(userID string) (*model.User, *model.AppError) {
	if a.adminErr != nil {
		return nil, a.adminErr
	}
	return a.adminTestAPI.GetUser(userID)
}

func (a *keycloakBanTestAPI) CreatePost(post *model.Post) (*model.Post, *model.AppError) {
	a.posts = append(a.posts, post)
	return post, a.postErr
}

func (a *keycloakBanTestAPI) LogError(message string, _ ...any) {
	a.logErrors = append(a.logErrors, message)
}

func (a *keycloakBanTestAPI) UpdateUserActive(userID string, active bool) *model.AppError {
	a.activeUpdates = append(a.activeUpdates, struct {
		userID string
		active bool
	}{userID, active})
	if a.activeErr != nil {
		return a.activeErr
	}
	for _, user := range a.users {
		if user.Id == userID {
			user.DeleteAt = 1
		}
	}
	return nil
}

func (a *keycloakRegistrationTestAPI) RegisterCommand(command *model.Command) error {
	a.command = command
	return nil
}

func TestKeycloakCommandAutocomplete(t *testing.T) {
	p, base := newAdminTestPlugin(t)
	api := &keycloakRegistrationTestAPI{adminTestAPI: base}
	p.SetAPI(api)

	if err := p.registerKeycloakCommand(); err != nil {
		t.Fatal(err)
	}
	command := api.command
	if command == nil || !command.AutoComplete || command.AutocompleteData == nil {
		t.Fatal("keycloak autocomplete was not registered")
	}
	if !strings.Contains(command.AutoCompleteHint, "new-user") ||
		!strings.Contains(command.AutoCompleteHint, "reset-password") ||
		strings.Contains(command.AutoCompleteHint, "new_user") ||
		strings.Contains(command.AutoCompleteHint, "reset_password") {
		t.Fatalf("unexpected keycloak autocomplete hint: %q", command.AutoCompleteHint)
	}
	data := command.AutocompleteData
	if data.Trigger != command.Trigger || data.HelpText != command.AutoCompleteDesc || data.RoleID != model.SystemAdminRoleId {
		t.Fatalf("unexpected keycloak autocomplete data: %+v", data)
	}
	if err := data.IsValid(); err != nil {
		t.Fatalf("invalid autocomplete data: %v", err)
	}

	response, err := p.keycloakCommand(&model.CommandArgs{UserId: "user", Command: "/keycloak"})
	if err != nil || response == nil || response.Text != "Only system admins can run /keycloak." {
		t.Fatalf("unauthorized command response: %+v, error: %v", response, err)
	}
}

func TestKeycloakSubcommandHelp(t *testing.T) {
	p, _ := newAdminTestPlugin(t)

	tests := map[string]string{
		"/keycloak new-user":                          "Create a domestic Keycloak account",
		"/keycloak get":                               "Show a user's Mattermost authentication",
		"/keycloak reset-password":                    "Generate a new temporary password",
		"/keycloak delete-user":                       "Permanently delete a Keycloak account",
		"/keycloak set-login-method":                  "Set Google/OpenID",
		"/keycloak migrate-to-google":                 "Migrate a Keycloak account to Google",
		"/keycloak migrate-to-google create-kth":      "Create a KTH user's Google Workspace account",
		"/keycloak migrate-to-google create-external": "Create an external Google Workspace account",
		"/keycloak migrate-to-google bind":            "Bind an existing Google Workspace account",
		"/keycloak migrate-to-google unmigrate":       "Reverse a Google migration",
		"/keycloak migrate-to-keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID",
		"/keycloak migrate-to-keycloak create":        "Create a temporary-password Keycloak account",
		"/keycloak migrate-to-keycloak bind":          "Bind an existing Keycloak account",
		"/keycloak migrate-to-keycloak update":        "Synchronize a previously created or bound Keycloak account",
		"/keycloak migrate-to-keycloak prep":          "Create a Mattermost user from an existing Google account",
	}

	for command, description := range tests {
		t.Run(command, func(t *testing.T) {
			response, appErr := p.keycloakCommand(&model.CommandArgs{
				UserId: "admin", Command: command,
			})
			if appErr != nil {
				t.Fatalf("command returned AppError: %v", appErr)
			}
			if !strings.Contains(response.Text, "\n"+description) {
				t.Fatalf("missing subcommand description:\n%s", response.Text)
			}
		})
	}
	t.Run("migrate-to-google update is removed", func(t *testing.T) {
		response, appErr := p.keycloakCommand(&model.CommandArgs{
			UserId: "admin", Command: "/keycloak migrate-to-google update",
		})
		if appErr != nil {
			t.Fatalf("command returned AppError: %v", appErr)
		}
		if strings.Contains(response.Text, "migrate-to-google update") {
			t.Fatalf("removed subcommand is still advertised:\n%s", response.Text)
		}
	})
	for _, command := range []string{"/keycloak migrate_to_google", "/keycloak migrate_to_keycloak"} {
		response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: command})
		if appErr != nil || strings.Contains(response.Text, "Migrate a ") {
			t.Fatalf("legacy command %s was accepted: response=%+v error=%v", command, response, appErr)
		}
	}
}

func TestKeycloakGet(t *testing.T) {
	tests := []struct {
		name, caller, command, search, details, want string
		detailStatus                                 int
		authData                                     *string
		wantDetails                                  bool
	}{
		{"success", "admin", "/keycloak get @existing", `[{"id":"keycloak-id"}]`, `{"id":"keycloak-id","username":"kc-user","email":"kc@example.org","enabled":false,"emailVerified":true,"attributes":{"mattermostID":["id123"],"multi":["first","second"],"empty":[],"markup":["` + "```" + `"]}}`, `"second"`, 200, model.NewPointer("id123"), true},
		{"unauthorized", "existing", "/keycloak get @existing", `[]`, `{}`, "Only system admins", 200, model.NewPointer("id123"), false},
		{"bad syntax", "admin", "/keycloak get @existing extra", `[]`, `{}`, "Usage:", 200, model.NewPointer("id123"), false},
		{"missing auth data", "admin", "/keycloak get @existing", `[]`, `{}`, "no authentication data", 200, nil, false},
		{"no match", "admin", "/keycloak get @existing", `[]`, `{}`, "was not found", 200, model.NewPointer("id123"), false},
		{"ambiguous", "admin", "/keycloak get @existing", `[{"id":"one"},{"id":"two"}]`, `{}`, "multiple Keycloak accounts", 200, model.NewPointer("id123"), false},
		{"missing id", "admin", "/keycloak get @existing", `[{}]`, `{}`, "has no ID", 200, model.NewPointer("id123"), false},
		{"details unavailable", "admin", "/keycloak get @existing", `[{"id":"keycloak-id"}]`, `{}`, "HTTP 403", 403, model.NewPointer("id123"), true},
		{"identity mismatch", "admin", "/keycloak get @existing", `[{"id":"keycloak-id"}]`, `{"id":"keycloak-id","attributes":{"mattermostID":["other"],"private":["do not display"]}}`, "Could not verify", 200, model.NewPointer("id123"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			base.users[1].AuthService = model.ServiceOpenid
			base.users[1].AuthData = tt.authData
			p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})
			detailRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"token"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
					if r.URL.Query().Get("q") != "mattermostID:id123" || r.Header.Get("Authorization") != "Bearer token" {
						t.Errorf("unexpected lookup: %s", r.URL)
					}
					_, _ = w.Write([]byte(tt.search))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users/keycloak-id":
					detailRequests++
					w.WriteHeader(tt.detailStatus)
					_, _ = w.Write([]byte(tt.details))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			oldURL := keycloakURL
			keycloakURL = server.URL
			t.Cleanup(func() { keycloakURL = oldURL })
			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: tt.caller, Command: tt.command})
			if appErr != nil || response == nil || response.ResponseType != model.CommandResponseTypeEphemeral || !strings.Contains(response.Text, tt.want) {
				t.Fatalf("response=%+v error=%v", response, appErr)
			}
			if (detailRequests > 0) != tt.wantDetails {
				t.Fatalf("unexpected detail requests: %d", detailRequests)
			}
			if tt.name == "success" {
				for _, want := range []string{`"auth_service": "openid"`, `"auth_data": "id123"`, `"username": "kc-user"`, `"enabled": false`, `"emailVerified": true`, `"mattermostID"`, `"first"`, `"empty": []`, "    \"markup\""} {
					if !strings.Contains(response.Text, want) {
						t.Errorf("missing %q: %s", want, response.Text)
					}
				}
			}
			if tt.name == "identity mismatch" && strings.Contains(response.Text, "do not display") {
				t.Fatal("displayed attributes for a different account")
			}
		})
	}
}

func TestKeycloakSetLoginMethod(t *testing.T) {
	t.Run("authorization and input", func(t *testing.T) {
		p, base := newAdminTestPlugin(t)
		api := &keycloakIdentityTestAPI{adminTestAPI: base}
		p.SetAPI(api)
		for _, tt := range []struct{ user, command, want string }{
			{"existing", "/keycloak set-login-method @existing saml", "Only system admins"},
			{"admin", "/keycloak set-login-method @existing", "Usage:"},
			{"admin", "/keycloak set-login-method @existing saml extra", "Usage:"},
			{"admin", "/keycloak set-login-method @existing ldap", "Usage:"},
			{"admin", "/keycloak set-login-method existing saml", "must start with @"},
			{"admin", "/keycloak enable @existing", "Usage:"},
			{"admin", "/keycloak disable @existing", "Usage:"},
			{"admin", "/keycloak switch-login-method @existing", "Usage:"},
		} {
			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: tt.user, Command: tt.command})
			if appErr != nil || !strings.Contains(response.Text, tt.want) || api.authUpdates != 0 {
				t.Fatalf("response=%+v error=%v updates=%d", response, appErr, api.authUpdates)
			}
		}
		base.users[1].AuthService = model.ServiceOpenid
		response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: "/keycloak set-login-method @existing saml"})
		if appErr != nil || !strings.Contains(response.Text, "no authentication data") || api.authUpdates != 0 {
			t.Fatalf("nil auth data: response=%+v error=%v", response, appErr)
		}
	})
	tests := []struct {
		name, service, target, authData, keycloakID, googleID, want, wantPermission string
		keycloakStatus, googleStatus, putStatus, wantUpdates                        int
		failUpdate                                                                  bool
	}{
		{"openid to saml", model.ServiceOpenid, "saml", "google123", "google123", "google123", "Keycloak/SAML", "True", 200, 200, 204, 1, false},
		{"saml to openid", model.UserAuthServiceSaml, "google", "google123", "google123", "google123", "Google/OpenID", "False", 200, 200, 204, 1, false},
		{"same saml", model.UserAuthServiceSaml, "saml", "google123", "google123", "google123", "Keycloak/SAML", "True", 200, 200, 204, 0, false},
		{"same openid", model.ServiceOpenid, "google", "google123", "google123", "google123", "Google/OpenID", "False", 200, 200, 204, 0, false},
		{"missing auth data", model.ServiceOpenid, "saml", "", "google123", "google123", "no authentication data", "", 200, 200, 204, 0, false},
		{"unsupported service", "ldap", "saml", "google123", "google123", "google123", "not authenticated", "", 200, 200, 204, 0, false},
		{"missing keycloak", model.ServiceOpenid, "saml", "google123", "", "google123", "was not found", "", 404, 200, 204, 0, false},
		{"keycloak unavailable", model.ServiceOpenid, "saml", "google123", "", "google123", "HTTP 500", "", 500, 200, 204, 0, false},
		{"mismatched keycloak", model.ServiceOpenid, "saml", "google123", "other", "google123", "Keycloak mattermostID does not match", "", 200, 200, 204, 0, false},
		{"missing google", model.ServiceOpenid, "saml", "google123", "google123", "", "Could not find the Google account", "", 200, 404, 204, 0, false},
		{"mismatched google", model.ServiceOpenid, "saml", "google123", "google123", "other", "Google account ID does not match", "", 200, 200, 204, 0, false},
		{"saml auth update fails", model.ServiceOpenid, "saml", "google123", "google123", "google123", "previous Keycloak login permission was restored", "False", 200, 200, 204, 1, true},
		{"openid auth update fails", model.UserAuthServiceSaml, "google", "google123", "google123", "google123", "Keycloak Mattermost login was not changed", "", 200, 200, 204, 1, true},
		{"saml permission fails", model.ServiceOpenid, "saml", "google123", "google123", "google123", "Mattermost authentication was not changed", "True", 200, 200, 500, 0, false},
		{"openid permission fails", model.UserAuthServiceSaml, "google", "google123", "google123", "google123", "previous Mattermost authentication was restored", "False", 200, 200, 500, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			api := &keycloakIdentityTestAPI{adminTestAPI: base, failUpdate: tt.failUpdate}
			p.SetAPI(api)
			user := api.users[1]
			user.AuthService = tt.service
			user.AuthData = model.NewPointer(tt.authData)
			var gotPermission string
			key := setupGoogleTestTransport(t, func(r *http.Request) (int, string) {
				switch {
				case r.URL.Host == "oauth2.googleapis.com":
					return 200, `{"access_token":"token"}`
				case r.URL.Host == "admin.googleapis.com":
					if r.URL.Path != "/admin/directory/v1/users/google123" {
						t.Errorf("unexpected Google lookup: %s", r.URL)
					}
					return tt.googleStatus, `{"id":"` + tt.googleID + `"}`
				case r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					return 200, `{"access_token":"token"}`
				case r.URL.Path == "/admin/realms/mattermost/users" && r.Method == http.MethodGet:
					if r.URL.Query().Get("q") != "mattermostID:google123" {
						t.Errorf("unexpected Keycloak lookup: %s", r.URL)
					}
					if tt.keycloakStatus == 404 {
						return 200, `[]`
					}
					return tt.keycloakStatus, `[{"id":"keycloak-uuid","attributes":{"mattermostID":["` + tt.keycloakID + `"],"allowMattermostLogin":["False"]}}]`
				case r.URL.Path == "/admin/realms/mattermost/users/keycloak-uuid" && r.Method == http.MethodPut:
					var profile keycloakUserRepresentation
					if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
						t.Fatal(err)
					}
					gotPermission = keycloakMigrationAttribute(&profile, "allowMattermostLogin")
					return tt.putStatus, `{}`
				default:
					t.Errorf("unexpected request: %s", r.URL)
					return 404, `{}`
				}
			})
			p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test", KeycloakMigrationGoogleServiceAccountKey: key})
			oldURL := keycloakURL
			keycloakURL = "https://keycloak.test"
			t.Cleanup(func() { keycloakURL = oldURL })

			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: "/keycloak set-login-method @existing " + tt.target})
			if appErr != nil || !strings.Contains(response.Text, tt.want) || api.authUpdates != tt.wantUpdates || gotPermission != tt.wantPermission {
				t.Fatalf("response=%+v error=%v updates=%d permission=%q", response, appErr, api.authUpdates, gotPermission)
			}
			expectedService := tt.service
			if tt.wantUpdates == 1 && !tt.failUpdate {
				if tt.target == "saml" {
					expectedService = model.UserAuthServiceSaml
				} else {
					expectedService = model.ServiceOpenid
				}
			}
			if user.AuthService != expectedService || *user.AuthData != tt.authData {
				t.Fatalf("identity changed unexpectedly: %+v", user)
			}
		})
	}
}

func TestKeycloakMigrationsUpdateMattermostAuth(t *testing.T) {
	for _, tt := range []struct {
		name, command, initialService, initialID, targetService, targetID string
		failUpdate, keepLogin                                             bool
	}{
		{"to google", "/keycloak migrate-to-google bind @existing existing@fysiksektionen.se", model.UserAuthServiceSaml, "keycloak123", model.ServiceOpenid, "google123", false, false},
		{"bind keep", "/keycloak migrate-to-google bind @existing existing@fysiksektionen.se --keep-mamo-login-through-keycloak", model.UserAuthServiceSaml, "keycloak123", model.UserAuthServiceSaml, "google123", false, true},
		{"create kth", "/keycloak migrate-to-google create-kth @existing existing f24", model.UserAuthServiceSaml, "keycloak123", model.ServiceOpenid, "google123", false, false},
		{"create kth keep", "/keycloak migrate-to-google create-kth @existing existing f24 --keep-mamo-login-through-keycloak", model.UserAuthServiceSaml, "keycloak123", model.UserAuthServiceSaml, "google123", false, true},
		{"create external", "/keycloak migrate-to-google create-external @existing existing@fysiksektionen.se external", model.UserAuthServiceSaml, "keycloak123", model.UserAuthServiceSaml, "google123", false, true},
		{"create external force google", "/keycloak migrate-to-google create-external @existing existing@fysiksektionen.se external --force-mamo-login-through-google", model.UserAuthServiceSaml, "keycloak123", model.ServiceOpenid, "google123", false, false},
		{"to keycloak", "/keycloak migrate-to-keycloak update @existing", model.ServiceOpenid, "google123", model.UserAuthServiceSaml, "google123", false, true},
		{"to google update fails", "/keycloak migrate-to-google bind @existing existing@fysiksektionen.se", model.UserAuthServiceSaml, "keycloak123", model.ServiceOpenid, "google123", true, false},
		{"to google rollback fails", "/keycloak migrate-to-google bind @existing existing@fysiksektionen.se", model.UserAuthServiceSaml, "keycloak123", model.ServiceOpenid, "google123", true, false},
		{"to keycloak update fails", "/keycloak migrate-to-keycloak update @existing", model.ServiceOpenid, "google123", model.UserAuthServiceSaml, "google123", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			api := &keycloakIdentityTestAPI{adminTestAPI: base, failUpdate: tt.failUpdate}
			p.SetAPI(api)
			user := api.users[1]
			user.AuthService, user.AuthData = tt.initialService, model.NewPointer(tt.initialID)
			var profile keycloakUserRepresentation
			profileUpdates := 0
			key := setupGoogleTestTransport(t, func(r *http.Request) (int, string) {
				switch {
				case r.URL.Host == "oauth2.googleapis.com":
					return 200, `{"access_token":"token"}`
				case r.URL.Host == "admin.googleapis.com" && r.URL.Path == "/admin/directory/v1/customer/my_customer/orgunits":
					return 200, `{"organizationUnits":[{"orgUnitPath":"/Teknologer/Teknologer i F-24"},{"orgUnitPath":"/Icke-teknologer"}]}`
				case r.URL.Host == "admin.googleapis.com" && r.Method == http.MethodGet && strings.Contains(tt.name, "create"):
					return 404, `{}`
				case r.URL.Host == "admin.googleapis.com" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
					return 200, `{"id":"google123","primaryEmail":"existing@fysiksektionen.se"}`
				case r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					return 200, `{"access_token":"token"}`
				case r.URL.Path == "/admin/realms/mattermost/users" && r.Method == http.MethodGet:
					switch r.URL.Query().Get("q") {
					case "mattermostID:keycloak123":
						return 200, `[{"id":"keycloak-uuid","username":"old","email":"existing@fysiksektionen.se","attributes":{"mattermostID":["keycloak123"],"allowMattermostLogin":["True"]}}]`
					case "mattermostID:google123":
						if tt.initialService == model.ServiceOpenid {
							return 200, `[{"id":"keycloak-uuid","username":"old","attributes":{"accountMigrated":["True"],"mattermostID":["google123"]}}]`
						}
					}
					return 200, `[]`
				case r.URL.Path == "/admin/realms/mattermost/users/keycloak-uuid" && r.Method == http.MethodPut:
					var update keycloakUserRepresentation
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Fatal(err)
					}
					profileUpdates++
					if tt.name == "to google rollback fails" && profileUpdates == 2 {
						return 500, `{}`
					}
					profile = update
					return 204, ""
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					return 404, `{}`
				}
			})
			p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test", KeycloakMigrationGoogleServiceAccountKey: key})
			oldURL := keycloakURL
			keycloakURL = "https://keycloak.test"
			t.Cleanup(func() { keycloakURL = oldURL })

			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: tt.command})
			want := "Migrated"
			if tt.failUpdate {
				if tt.name == "to google update fails" {
					want = "previous Keycloak profile was restored"
				} else if tt.name == "to google rollback fails" {
					want = "restoring the previous Keycloak profile failed"
				} else {
					want = "Keycloak migration profile was updated, but update Mattermost authentication data"
				}
			}
			if appErr != nil || !strings.Contains(response.Text, want) || api.authUpdates != 1 {
				t.Fatalf("response=%+v error=%v updates=%d", response, appErr, api.authUpdates)
			}
			expectedService, expectedID := tt.targetService, tt.targetID
			if tt.failUpdate {
				expectedService, expectedID = tt.initialService, tt.initialID
			}
			profileID, permission := tt.targetID, keycloakBooleanString(tt.keepLogin)
			if tt.name == "to google update fails" {
				profileID, permission = tt.initialID, "True"
			}
			if user.AuthService != expectedService || *user.AuthData != expectedID || keycloakMigrationAttribute(&profile, "mattermostID") != profileID {
				t.Fatalf("incorrect migration: user=%+v profile=%+v", user, profile)
			}
			if got := keycloakMigrationAttribute(&profile, "allowMattermostLogin"); got != permission {
				t.Fatalf("Keycloak login permission = %q", got)
			}
			if !tt.failUpdate && tt.initialService == model.UserAuthServiceSaml && !strings.Contains(response.Text, keycloakLoginPermissionMessage(tt.keepLogin)) {
				t.Fatalf("migration response missing login status: %s", response.Text)
			}
		})
	}
}

func TestGoogleMigrationFlagValidation(t *testing.T) {
	p, _ := newAdminTestPlugin(t)
	for _, command := range []string{
		"/keycloak migrate-to-google bind @existing existing@fysiksektionen.se --unknown",
		"/keycloak migrate-to-google bind @existing --keep-mamo-login-through-keycloak",
		"/keycloak migrate-to-google bind @existing existing@fysiksektionen.se --keep-mamo-login-through-keycloak --keep-mamo-login-through-keycloak",
		"/keycloak migrate-to-google create-kth @existing existing --keep-mamo-login-through-keycloak f24",
		"/keycloak migrate-to-google create-external @existing existing@fysiksektionen.se external --keep-mamo-login-through-keycloak",
		"/keycloak migrate-to-google create-kth @existing existing f24 --force-mamo-login-through-google",
		"/keycloak migrate-to-google bind @existing existing@fysiksektionen.se --force-mamo-login-through-google",
		"/keycloak migrate-to-google list-org-units --keep-mamo-login-through-keycloak",
		"/keycloak migrate-to-google unmigrate @existing --keep-mamo-login-through-keycloak",
	} {
		response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: command})
		if appErr != nil || !strings.Contains(response.Text, "Usage:") {
			t.Fatalf("%s: response=%+v error=%v", command, response, appErr)
		}
	}
}

func TestKeycloakDeleteUser(t *testing.T) {
	tests := []struct {
		name, command, caller, keycloakResponse, want string
		deleteStatus                                  int
		deactivateErr                                 bool
		wantDelete, wantDeactivation                  bool
	}{
		{"delete only", "/keycloak delete-user by-authdata @existing", "admin", `[{"id":"keycloak-id","attributes":{"mattermostID":["id123"]}}]`, "not deactivated", 204, false, true, false},
		{"deactivate", "/keycloak delete-user by-authdata @existing --also-deactivate", "admin", `[{"id":"keycloak-id","attributes":{"mattermostID":["id123"]}}]`, "and deactivated", 204, false, true, true},
		{"unauthorized", "/keycloak delete-user by-authdata @existing", "existing", `[]`, "Only system admins", 204, false, false, false},
		{"invalid flag", "/keycloak delete-user by-authdata @existing --unknown", "admin", `[]`, "Usage:", 204, false, false, false},
		{"missing account", "/keycloak delete-user by-authdata @existing --also-deactivate", "admin", `[]`, "was not found", 204, false, false, false},
		{"ambiguous account", "/keycloak delete-user by-authdata @existing", "admin", `[{"id":"first","attributes":{"mattermostID":["id123"]}},{"id":"second","attributes":{"mattermostID":["id123"]}}]`, "multiple Keycloak accounts", 204, false, false, false},
		{"wrong identity", "/keycloak delete-user by-authdata @existing", "admin", `[{"id":"keycloak-id","attributes":{"mattermostID":["other"]}}]`, "does not match", 204, false, false, false},
		{"missing id", "/keycloak delete-user by-authdata @existing", "admin", `[{"attributes":{"mattermostID":["id123"]}}]`, "missing", 204, false, false, false},
		{"delete fails", "/keycloak delete-user by-authdata @existing --also-deactivate", "admin", `[{"id":"keycloak-id","attributes":{"mattermostID":["id123"]}}]`, "deactivation was not attempted", 500, false, true, false},
		{"deactivation fails", "/keycloak delete-user by-authdata @existing --also-deactivate", "admin", `[{"id":"keycloak-id","attributes":{"mattermostID":["id123"]}}]`, "Deactivate the Mattermost user separately", 204, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			api := &keycloakBanTestAPI{adminTestAPI: base}
			p.SetAPI(api)
			p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})
			base.users[1].AuthData = model.NewPointer("id123")
			base.users[1].AuthService = model.UserAuthServiceSaml
			if tt.deactivateErr {
				api.activeErr = testAppError(http.StatusInternalServerError)
			}
			deleted := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"token"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
					if r.URL.Query().Get("q") != "mattermostID:id123" {
						t.Errorf("unexpected lookup: %s", r.URL)
					}
					_, _ = w.Write([]byte(tt.keycloakResponse))
				case r.Method == http.MethodDelete && r.URL.Path == "/admin/realms/mattermost/users/keycloak-id":
					deleted = true
					w.WriteHeader(tt.deleteStatus)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			oldURL := keycloakURL
			keycloakURL = server.URL
			t.Cleanup(func() { keycloakURL = oldURL })
			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: tt.caller, Command: tt.command})
			if appErr != nil || !strings.Contains(response.Text, tt.want) || deleted != tt.wantDelete || (len(api.activeUpdates) > 0) != tt.wantDeactivation {
				t.Fatalf("response=%+v error=%v delete=%t activeUpdates=%+v", response, appErr, deleted, api.activeUpdates)
			}
			if tt.name == "delete only" && !strings.Contains(response.Text, "no longer log in") {
				t.Fatalf("missing SAML warning: %s", response.Text)
			}
		})
	}
}

func TestKeycloakDeleteUserByEmail(t *testing.T) {
	tests := []struct {
		name, command, account, want             string
		mattermostEmail                          string
		lookupCount                              int
		wantSearch, wantDelete, wantDeactivation bool
	}{
		{"email only", "/keycloak delete-user by-email alice@example.org", `[{"id":"keycloak-id","attributes":{"mattermostEmail":["alice@example.org"]}}]`, "not deactivated", "", 0, true, true, false},
		{"deactivate", "/keycloak delete-user by-email alice@example.org --also-deactivate", `[{"id":"keycloak-id","attributes":{"mattermostEmail":["alice@example.org"]}}]`, "and deactivated", "alice@example.org", 1, true, true, true},
		{"missing Mattermost", "/keycloak delete-user by-email alice@example.org --also-deactivate", `[]`, "no account was deleted", "", 1, false, false, false},
		{"missing Keycloak", "/keycloak delete-user by-email alice@example.org", `[]`, "was not found", "", 0, true, false, false},
		{"ambiguous Keycloak", "/keycloak delete-user by-email alice@example.org", `[{"id":"1","attributes":{"mattermostEmail":["alice@example.org"]}},{"id":"2","attributes":{"mattermostEmail":["alice@example.org"]}}]`, "multiple Keycloak accounts", "", 0, true, false, false},
		{"mismatched email", "/keycloak delete-user by-email alice@example.org", `[{"id":"keycloak-id","attributes":{"mattermostEmail":["different@example.org"]}}]`, "does not match", "", 0, true, false, false},
		{"invalid email", "/keycloak delete-user by-email invalid", `[]`, "Invalid e-mail", "", 0, false, false, false},
		{"missing mode", "/keycloak delete-user alice@example.org", `[]`, "Usage:", "", 0, false, false, false},
		{"invalid mode", "/keycloak delete-user by-other alice@example.org", `[]`, "Usage:", "", 0, false, false, false},
		{"legacy command", "/keycloak delete_user @existing", `[]`, "Usage:", "", 0, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			base.users[1].Email = tt.mattermostEmail
			api := &keycloakDeleteEmailTestAPI{keycloakBanTestAPI: &keycloakBanTestAPI{adminTestAPI: base}}
			p.SetAPI(api)
			p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})
			searched, deleted := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"token"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
					searched = true
					if r.URL.Query().Get("q") != "mattermostEmail:alice@example.org" {
						t.Errorf("unexpected lookup: %s", r.URL)
					}
					_, _ = w.Write([]byte(tt.account))
				case r.Method == http.MethodDelete && r.URL.Path == "/admin/realms/mattermost/users/keycloak-id":
					deleted = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			oldURL := keycloakURL
			keycloakURL = server.URL
			t.Cleanup(func() { keycloakURL = oldURL })
			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: tt.command})
			if appErr != nil || !strings.Contains(response.Text, tt.want) || api.lookups != tt.lookupCount ||
				searched != tt.wantSearch || deleted != tt.wantDelete || (len(api.activeUpdates) > 0) != tt.wantDeactivation {
				t.Fatalf("response=%+v error=%v lookups=%d search=%t delete=%t activeUpdates=%+v", response, appErr, api.lookups, searched, deleted, api.activeUpdates)
			}
		})
	}
}

func TestMigrationMattermostUserRequiresAtUsername(t *testing.T) {
	p, _ := newAdminTestPlugin(t)

	if _, err := p.migrationMattermostUser("~alice"); err == nil {
		t.Fatal("~username reference was accepted")
	}
	if _, err := p.migrationMattermostUser("@"); err == nil {
		t.Fatal("empty @username reference was accepted")
	}
}

func TestKeycloakResetPassword(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})

	existing := api.users[1]
	existing.AuthData = model.NewPointer("keycloak123")

	var gotCredential keycloakCredential
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"keycloak123","username":"existing@example.org","attributes":{"mattermostUsername":["existing"]}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/mattermost/users/keycloak123/reset-password":
			if err := json.NewDecoder(r.Body).Decode(&gotCredential); err != nil {
				t.Fatalf("decode temporary password: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })

	response, appErr := p.keycloakCommand(&model.CommandArgs{
		UserId:  "admin",
		Command: "/keycloak reset-password @existing",
	})
	if appErr != nil {
		t.Fatalf("command returned AppError: %v", appErr)
	}
	if !strings.Contains(response.Text, "Temporary password:") || !strings.Contains(response.Text, "required to change") {
		t.Fatalf("unexpected reset response: %s", response.Text)
	}
	if gotCredential.Type != "password" || !gotCredential.Temporary || gotCredential.Value == "" {
		t.Fatalf("invalid temporary password credential: %+v", gotCredential)
	}
}

func TestKeycloakBanUnban(t *testing.T) {
	p, base := newAdminTestPlugin(t)
	api := &keycloakBanTestAPI{adminTestAPI: base}
	p.SetAPI(api)
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})

	existing := api.users[1]
	existing.AuthData = model.NewPointer("keycloak123")

	var gotAttributes map[string][]string
	updateStatus := http.StatusNoContent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
			if r.URL.Query().Get("q") != "mattermostID:keycloak123" {
				t.Fatalf("unexpected Keycloak search query: %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"keycloak123","username":"existing@example.org","attributes":{"banReason":["old reason"],"other":["preserved"]}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/mattermost/users/keycloak123":
			var user keycloakUserRepresentation
			if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
				t.Fatalf("decode Keycloak update: %v", err)
			}
			gotAttributes = user.Attributes
			w.WriteHeader(updateStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })

	for _, test := range []struct {
		command     string
		banned      string
		reason      string
		wantUpdates int
	}{
		{command: "/keycloak ban @existing repeated abuse in chat", banned: "True", reason: "repeated abuse in chat", wantUpdates: 1},
		{command: "/keycloak unban @existing", banned: "False", wantUpdates: 1},
	} {
		response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: test.command})
		if appErr != nil {
			t.Fatalf("%s returned AppError: %v", test.command, appErr)
		}
		wantStatus := "Banned"
		if test.banned == "False" {
			wantStatus = "Unbanned"
		}
		if !strings.Contains(response.Text, wantStatus) {
			t.Fatalf("unexpected response for %s: %s", test.command, response.Text)
		}
		if len(api.activeUpdates) != test.wantUpdates || api.activeUpdates[0].userID != existing.Id || api.activeUpdates[0].active {
			t.Fatalf("%s produced unexpected activation updates: %+v", test.command, api.activeUpdates)
		}
		if existing.DeleteAt == 0 {
			t.Fatalf("%s did not leave the Mattermost user deactivated", test.command)
		}
		if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "accountBanned"); got != test.banned {
			t.Fatalf("%s set accountBanned to %q, want %q", test.command, got, test.banned)
		}
		if test.reason != "" {
			if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "banReason"); got != test.reason {
				t.Fatalf("%s set banReason to %q, want %q", test.command, got, test.reason)
			}
		} else if _, ok := gotAttributes["banReason"]; ok {
			t.Fatalf("%s did not clear banReason", test.command)
		}
		if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "other"); got != "preserved" {
			t.Fatalf("%s did not preserve other attributes: %v", test.command, gotAttributes)
		}
	}

	api.activeErr = testAppError(http.StatusInternalServerError)
	response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: "/keycloak ban @existing retry ban"})
	if appErr != nil || !strings.Contains(response.Text, "was banned, but Mattermost deactivation failed or was incomplete") || len(api.activeUpdates) != 2 {
		t.Fatalf("deactivation failure not reported correctly: response=%+v error=%v updates=%+v", response, appErr, api.activeUpdates)
	}

	api.activeErr = nil
	response, appErr = p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: "/keycloak ban @existing retry ban"})
	if appErr != nil || !strings.Contains(response.Text, "deactivated the Mattermost account") || len(api.activeUpdates) != 3 {
		t.Fatalf("retry on deactivated user failed: response=%+v error=%v updates=%+v", response, appErr, api.activeUpdates)
	}

	updateStatus = http.StatusInternalServerError
	response, appErr = p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: "/keycloak ban @existing reason"})
	if appErr != nil || !strings.Contains(response.Text, "Keycloak account ban update returned HTTP 500") || len(api.activeUpdates) != 3 {
		t.Fatalf("Keycloak failure not reported correctly: response=%+v error=%v updates=%+v", response, appErr, api.activeUpdates)
	}

	for _, command := range []string{"/keycloak ban @existing", "/keycloak unban @existing extra"} {
		response, appErr = p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: command})
		if appErr != nil || !strings.Contains(response.Text, "Usage:") || len(api.activeUpdates) != 3 {
			t.Fatalf("invalid command %q: response=%+v error=%v updates=%+v", command, response, appErr, api.activeUpdates)
		}
	}
	response, appErr = p.keycloakCommand(&model.CommandArgs{UserId: "existing", Command: "/keycloak ban @existing reason"})
	if appErr != nil || !strings.Contains(response.Text, "Only system admins") || len(api.activeUpdates) != 3 {
		t.Fatalf("unauthorized ban: response=%+v error=%v updates=%+v", response, appErr, api.activeUpdates)
	}
}

func TestKeycloakModerationLog(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		channel      string
		keycloakFail bool
		activeFail   bool
		adminFail    bool
		postFail     bool
		wantMessage  string
		wantResponse string
		wantUpdates  int
		wantLogError bool
	}{
		{name: "disabled", command: "/keycloak ban @existing reason", adminFail: true, wantResponse: "Banned", wantUpdates: 1},
		{name: "ban", command: "/keycloak ban @existing repeated abuse in chat", channel: "moderation", wantMessage: "@admin banned @existing for the reason: repeated abuse in chat", wantResponse: "Banned", wantUpdates: 1},
		{name: "unban", command: "/keycloak unban @existing", channel: "moderation", wantMessage: "@admin unbanned @existing", wantResponse: "Unbanned"},
		{name: "deactivation fails", command: "/keycloak ban @existing reason", channel: "moderation", activeFail: true, wantMessage: "@admin banned @existing for the reason: reason", wantResponse: "deactivation failed or was incomplete", wantUpdates: 1},
		{name: "Keycloak fails", command: "/keycloak ban @existing reason", channel: "moderation", keycloakFail: true, wantResponse: "HTTP 500"},
		{name: "posting fails", command: "/keycloak ban @existing reason", channel: "moderation", postFail: true, wantMessage: "@admin banned @existing for the reason: reason", wantResponse: "Could not post the moderation log", wantUpdates: 1, wantLogError: true},
		{name: "deactivation and posting fail", command: "/keycloak ban @existing reason", channel: "moderation", activeFail: true, postFail: true, wantMessage: "@admin banned @existing for the reason: reason", wantResponse: "deactivation failed or was incomplete", wantUpdates: 1, wantLogError: true},
		{name: "administrator lookup fails", command: "/keycloak unban @existing", channel: "moderation", adminFail: true, wantResponse: "Could not post the moderation log", wantLogError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, base := newAdminTestPlugin(t)
			api := &keycloakBanTestAPI{adminTestAPI: base}
			p.SetAPI(api)
			p.setConfiguration(&configuration{
				KeycloakClientID: "test", KeycloakClientSecret: "test", KeycloakModerationLogChannelID: test.channel,
			})
			api.users[1].AuthData = model.NewPointer("keycloak123")
			if test.activeFail {
				api.activeErr = testAppError(http.StatusInternalServerError)
			}
			if test.adminFail {
				api.adminErr = testAppError(http.StatusInternalServerError)
			}
			if test.postFail {
				api.postErr = testAppError(http.StatusInternalServerError)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
					_, _ = w.Write([]byte(`[{"id":"keycloak123","username":"keycloak-user"}]`))
				case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/mattermost/users/keycloak123":
					if test.keycloakFail {
						w.WriteHeader(http.StatusInternalServerError)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			oldURL := keycloakURL
			keycloakURL = server.URL
			defer func() { keycloakURL = oldURL }()

			response, appErr := p.keycloakCommand(&model.CommandArgs{UserId: "admin", Command: test.command})
			if appErr != nil || !strings.Contains(response.Text, test.wantResponse) {
				t.Fatalf("unexpected response: %+v, error: %v", response, appErr)
			}
			if test.wantLogError && !strings.Contains(response.Text, "Could not post the moderation log") {
				t.Fatalf("moderation log failure not reported: %s", response.Text)
			}
			if len(api.activeUpdates) != test.wantUpdates {
				t.Fatalf("deactivation calls = %d, want %d", len(api.activeUpdates), test.wantUpdates)
			}
			if test.wantMessage == "" {
				if len(api.posts) != 0 {
					t.Fatalf("unexpected posts: %+v", api.posts)
				}
			} else if len(api.posts) != 1 || api.posts[0].ChannelId != test.channel ||
				api.posts[0].UserId != p.pluginBot.UserId || api.posts[0].Message != test.wantMessage {
				t.Fatalf("unexpected moderation post: %+v", api.posts)
			}
			if (len(api.logErrors) > 0) != test.wantLogError {
				t.Fatalf("unexpected log errors: %v", api.logErrors)
			}
		})
	}
}

func TestKeycloakMigrateToGoogleUnmigrate(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})

	existing := api.users[1]
	existing.AuthService = model.ServiceOpenid
	existing.AuthData = model.NewPointer("google123")

	var gotAttributes map[string][]string
	var gotUsername string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
			if r.URL.Query().Get("q") != "mattermostID:google123" {
				t.Fatalf("unexpected Keycloak search query: %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"keycloak123","username":"existing","attributes":{"accountMigrated":["True"],"mattermostID":["google123"],"mattermostEmail":["existing@fysiksektionen.se"],"migratedFromID":["keycloak123"],"migratedFromUsername":["existing@example.org"]}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/mattermost/users/keycloak123":
			var user keycloakUserRepresentation
			if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
				t.Fatalf("decode Keycloak update: %v", err)
			}
			gotAttributes = user.Attributes
			gotUsername = user.Username
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })

	for _, service := range []string{model.ServiceOpenid, model.UserAuthServiceSaml} {
		existing.AuthService = service
		existing.AuthData = model.NewPointer("google123")
		response, appErr := p.keycloakCommand(&model.CommandArgs{
			UserId: "admin", Command: "/keycloak migrate-to-google unmigrate @existing",
		})
		if appErr != nil || !strings.Contains(response.Text, "Unmigrated") {
			t.Fatalf("service %s: response=%+v error=%v", service, response, appErr)
		}
		if keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "accountMigrated") != "False" {
			t.Fatal("accountMigrated was not reset to False")
		}
		if _, ok := gotAttributes["mattermostEmail"]; ok {
			t.Fatal("mattermostEmail was not removed")
		}
		if _, ok := gotAttributes["migratedFromUsername"]; ok {
			t.Fatal("migratedFromUsername was not removed")
		}
		if gotUsername != "existing@example.org" {
			t.Fatalf("Keycloak username = %q, want old username", gotUsername)
		}
		if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "mattermostID"); got != "keycloak123" {
			t.Fatalf("mattermostID = %q, want old ID", got)
		}
		if existing.AuthService != model.UserAuthServiceSaml || existing.AuthData == nil || *existing.AuthData != "keycloak123" {
			t.Fatalf("Mattermost identity was not restored: service=%q authdata=%v", existing.AuthService, existing.AuthData)
		}
		if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "allowMattermostLogin"); got != "True" {
			t.Fatalf("Keycloak login permission = %q, want True", got)
		}
	}
}

func TestRandomPasswordMeetsGoogleCharacterClasses(t *testing.T) {
	password := randomPassword(16)
	if len(password) != 16 {
		t.Fatalf("password length is %d, expected 16", len(password))
	}
	if !strings.ContainsAny(password, "ABCDEFGHJKLMNPQRSTUVWXYZ") ||
		!strings.ContainsAny(password, "abcdefghijkmnopqrstuvwxyz") ||
		!strings.ContainsAny(password, "23456789") ||
		!strings.ContainsAny(password, "!@#$%^&*") {
		t.Fatalf("password does not contain all required character classes")
	}
}

func TestKeycloakBooleanAttributes(t *testing.T) {
	if keycloakBooleanString(true) != "True" {
		t.Fatal("Keycloak true attribute is not capitalized")
	}
	if keycloakBooleanString(false) != "False" {
		t.Fatal("Keycloak false attribute is not capitalized")
	}

	user := &keycloakUserRepresentation{
		Attributes: map[string][]string{"accountMigrated": {"True"}},
	}
	if !keycloakMigrationAttributeIsTrue(user, "accountMigrated") {
		t.Fatal("capitalized True attribute was not recognized")
	}
	user.Attributes["accountMigrated"] = []string{"true"}
	if !keycloakMigrationAttributeIsTrue(user, "accountMigrated") {
		t.Fatal("lowercase true attribute was not recognized")
	}
}

func TestGoogleMigrationKeycloakConflictsAllowBindTarget(t *testing.T) {
	p, _ := newAdminTestPlugin(t)
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/realms/mattermost/protocol/openid-connect/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/mattermost/users":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"bind-target","username":"alice@example.org","attributes":{"mattermostID":["google123"],"mattermostEmail":["alice@example.org"]}}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })

	if err := p.checkGoogleMigrationKeycloakConflicts("google123", "alice@example.org", "bind-target"); err != nil {
		t.Fatalf("bind target was reported as a conflict: %v", err)
	}
	if err := p.checkGoogleMigrationKeycloakConflicts("google123", "alice@example.org"); err == nil {
		t.Fatal("another Keycloak account with the Google ID was not reported as a conflict")
	}
}

func TestRenderKeycloakMigrationText(t *testing.T) {
	got := renderKeycloakMigrationText(
		"Hi @{{username}}, account {{account}}, password {{password}}. {{login_instructions}}",
		"alice",
		"alice@fysiksektionen.se",
		"temporary-password",
		"create-kth",
		true,
	)
	wantInstructions := keycloakMigrationLoginInstructions("create-kth", true, "alice@fysiksektionen.se")
	if got != "Hi @alice, account alice@fysiksektionen.se, password temporary-password. "+wantInstructions {
		t.Fatalf("rendered migration text = %q", got)
	}
	got = renderKeycloakMigrationText("Custom {{account}}", "alice", "alice@fysiksektionen.se", "", "create-kth", false)
	if got != "Custom alice@fysiksektionen.se"+keycloakMigrationLoginInstructions("create-kth", false, "alice@fysiksektionen.se") {
		t.Fatalf("rendered custom migration text = %q", got)
	}
	got = renderKeycloakMigrationText("Legacy {{login_method}}", "alice", "alice@fysiksektionen.se", "", "create-external", false)
	if got != "Legacy {{login_method}}" {
		t.Fatalf("rendered legacy migration placeholder = %q", got)
	}
}

func TestKeycloakMigrationLoginInstructions(t *testing.T) {
	for _, tt := range []struct {
		name, method, want string
		keepSAML           bool
	}{
		{
			name:   "external account uses KTH button",
			method: "create-external",
			want:   "To sign in to Mattermost, first log-in to student@example.org at accounts.google.com with your temporary password, then use the KTH button in Mattermost with your @fysiksektionen.se account.",
		},
		{
			name:   "KTH account uses KTH button",
			method: "create-kth",
			want:   "To sign in to Mattermost, select the KTH button and sign in with your KTH account.",
		},
		{
			name:     "SAML remains available after migration",
			method:   "create-external",
			keepSAML: true,
			want:     "Although a Google account has been created for you, you should continue signing in to Mattermost through the 'International Student' sign-in.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := keycloakMigrationLoginInstructions(tt.method, tt.keepSAML, "student@example.org"); got != tt.want {
				t.Fatalf("login instructions = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMigrationNotificationsDescribeActualLogin(t *testing.T) {
	for _, keep := range []bool{false, true} {
		p, base := newAdminTestPlugin(t)
		api := &keycloakNotificationTestAPI{adminTestAPI: base}
		p.SetAPI(api)
		instructions := keycloakMigrationLoginInstructions("create-kth", keep, "existing@fysiksektionen.se")
		p.setConfiguration(&configuration{
			KeycloakMigrationKTHDMText:    "Account: {{account}}. {{login_instructions}}",
			KeycloakMigrationKTHEmailText: "Account: {{account}}. {{login_instructions}}",
		})
		p.notifyMigrationInitiator("admin", "existing", "existing@fysiksektionen.se", "old@example.org", "", "create-kth", keep)
		if len(api.posts) != 3 {
			t.Fatalf("posts = %d, want 3", len(api.posts))
		}
		if !strings.Contains(api.posts[0].Message, keycloakLoginPermissionMessage(keep)) {
			t.Fatalf("notification status does not describe login permission: %s", api.posts[0].Message)
		}
		for _, post := range api.posts[1:] {
			if !strings.Contains(post.Message, "existing@fysiksektionen.se") || !strings.Contains(post.Message, instructions) {
				t.Fatalf("incorrect notification: %s", post.Message)
			}
		}
	}
}

func TestKeycloakMigrationNotificationTextBranches(t *testing.T) {
	configuration := &configuration{
		KeycloakMigrationDMText:            "generic DM",
		KeycloakMigrationEmailText:         "generic email",
		KeycloakMigrationKTHDMText:         "KTH DM",
		KeycloakMigrationKTHEmailText:      "KTH email",
		KeycloakMigrationExternalDMText:    "external DM",
		KeycloakMigrationExternalEmailText: "external email",
	}

	dm, email := keycloakMigrationNotificationTexts(configuration, "create-kth")
	if dm != "KTH DM" || email != "KTH email" {
		t.Fatalf("KTH branch = (%q, %q)", dm, email)
	}

	dm, email = keycloakMigrationNotificationTexts(configuration, "create-external")
	if dm != "external DM" || email != "external email" {
		t.Fatalf("external branch = (%q, %q)", dm, email)
	}

	dm, email = keycloakMigrationNotificationTexts(configuration, "bind")
	if dm != "generic DM" || email != "generic email" {
		t.Fatalf("generic branch = (%q, %q)", dm, email)
	}
}

func TestAlternativeGoogleEmail(t *testing.T) {
	if got := alternativeGoogleEmail("alice@fysiksektionen.se", "alice@example.com"); got != "alice@example.com" {
		t.Fatalf("alternative email = %q, expected Keycloak email", got)
	}
	if got := alternativeGoogleEmail("alice@fysiksektionen.se", "ALICE@FYSIKSEKTIONEN.SE"); got != "" {
		t.Fatalf("matching primary email was returned as alternative: %q", got)
	}
	if got := alternativeGoogleEmail("alice@fysiksektionen.se", ""); got != "" {
		t.Fatalf("empty alternative email was returned: %q", got)
	}
}

func TestValidateFysiksektionenEmail(t *testing.T) {
	if err := validateFysiksektionenEmail("alice@fysiksektionen.se"); err != nil {
		t.Fatalf("valid @fysiksektionen.se address rejected: %v", err)
	}
	if err := validateFysiksektionenEmail("alice"); err == nil {
		t.Fatal("address without @fysiksektionen.se suffix was accepted")
	}
	if err := validateFysiksektionenEmail("alice@example.com"); err == nil {
		t.Fatal("address with wrong domain was accepted")
	}
}

func TestResolveGoogleOrgUnitKey(t *testing.T) {
	units := []string{
		"/Alumner",
		"/Icke-teknologer",
		"/Utility-konton",
		"/Teknologer/Teknologer i F-Ø",
		"/Teknologer/Teknologer i F-24",
	}

	if !googleOrgUnitFPattern.MatchString(strings.Trim("/Teknologer/Teknologer i F-Ø", "/")) {
		t.Fatal("F-Ø org-unit pattern does not match the test fixture")
	}

	externalTests := map[string]string{
		"alumner":  "/Alumner",
		"external": "/Icke-teknologer",
		"utility":  "/Utility-konton",
	}
	for key, expected := range externalTests {
		resolved, err := resolveGoogleOrgUnitKey(units, "external", key)
		if err != nil {
			t.Fatalf("resolve external key %q: %v", key, err)
		}
		if resolved != expected {
			t.Fatalf("external key %q resolved to %q, expected %q", key, resolved, expected)
		}
	}

	for _, key := range []string{"f-o", "fo", "F-O", "F0", "f-0", "f-ø", "fø"} {
		resolved, err := resolveGoogleOrgUnitKey(units, "kth", key)
		if err != nil {
			t.Fatalf("resolve F-Ø key %q: %v", key, err)
		}
		if resolved != "/Teknologer/Teknologer i F-Ø" {
			t.Fatalf("key %q resolved to %q", key, resolved)
		}
	}
	for _, key := range []string{"f-24", "f24", "F-24", "F24"} {
		resolved, err := resolveGoogleOrgUnitKey(units, "kth", key)
		if err != nil {
			t.Fatalf("resolve F-XX key %q: %v", key, err)
		}
		if resolved != "/Teknologer/Teknologer i F-24" {
			t.Fatalf("key %q resolved to %q", key, resolved)
		}
	}

	if _, err := resolveGoogleOrgUnitKey(units, "external", "/Icke-teknologer"); err == nil {
		t.Fatal("full external org unit path was accepted")
	}
	if _, err := resolveGoogleOrgUnitKey(units, "kth", "/Teknologer/Teknologer i F-24"); err == nil {
		t.Fatal("full KTH org unit path was accepted")
	}
}
