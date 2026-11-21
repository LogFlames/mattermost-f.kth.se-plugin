package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestKeycloakSubcommandHelp(t *testing.T) {
	p, _ := newAdminTestPlugin(t)

	if got := keycloakMigrateUsage["migrate_to_keycloak"]; got != "Usage: `/keycloak migrate_to_keycloak create|bind|update`" {
		t.Fatalf("unexpected migrate_to_keycloak usage: %q", got)
	}

	if strings.Contains(keycloakMigrateUsage["migrate_to_google create-external"], "[org-unit-key]") {
		t.Fatal("create-external org-unit key is still shown as optional")
	}

	tests := map[string]string{
		"/keycloak enable":                            "Allow a Mattermost user",
		"/keycloak disable":                           "Prevent a Mattermost user",
		"/keycloak new_user":                          "Create a domestic Keycloak account",
		"/keycloak migrate_to_google":                 "Migrate a Mattermost account from Keycloak/SAML",
		"/keycloak migrate_to_google create-kth":      "Create a KTH user's Google Workspace account",
		"/keycloak migrate_to_google create-external": "Create an external Google Workspace account",
		"/keycloak migrate_to_google bind":            "Bind an existing Google Workspace account",
		"/keycloak migrate_to_google unmigrate":       "Reverse a Google migration",
		"/keycloak migrate_to_keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID",
		"/keycloak migrate_to_keycloak create":        "Create a temporary-password Keycloak account",
		"/keycloak migrate_to_keycloak bind":          "Bind an existing Keycloak account",
		"/keycloak migrate_to_keycloak update":        "Synchronize a previously created or bound Keycloak account",
	}

	for command, description := range tests {
		t.Run(command, func(t *testing.T) {
			response, appErr := p.keycloakCommand(&model.CommandArgs{
				UserId:  "admin",
				Command: command,
			})
			if appErr != nil {
				t.Fatalf("command returned AppError: %v", appErr)
			}
			if !strings.Contains(response.Text, "\n"+description) {
				t.Fatalf("missing subcommand description:\n%s", response.Text)
			}
		})
	}

	t.Run("migrate_to_google update is removed", func(t *testing.T) {
		response, appErr := p.keycloakCommand(&model.CommandArgs{
			UserId:  "admin",
			Command: "/keycloak migrate_to_google update",
		})
		if appErr != nil {
			t.Fatalf("command returned AppError: %v", appErr)
		}
		if strings.Contains(response.Text, "migrate_to_google update") {
			t.Fatalf("removed subcommand is still advertised:\n%s", response.Text)
		}
	})
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

func TestKeycloakEnableDisableMattermostLogin(t *testing.T) {
	p, api := newAdminTestPlugin(t)
	p.setConfiguration(&configuration{KeycloakClientID: "test", KeycloakClientSecret: "test"})

	existing := api.users[1]
	existing.AuthData = model.NewPointer("keycloak123")

	var gotAttributes map[string][]string
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
			_, _ = w.Write([]byte(`[{"id":"keycloak123","username":"existing@example.org","attributes":{"mattermostUsername":["existing"]}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/admin/realms/mattermost/users/keycloak123":
			var user keycloakUserRepresentation
			if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
				t.Fatalf("decode Keycloak update: %v", err)
			}
			gotAttributes = user.Attributes
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldURL := keycloakURL
	keycloakURL = server.URL
	t.Cleanup(func() { keycloakURL = oldURL })

	for _, test := range []struct {
		command string
		want    string
	}{
		{command: "/keycloak enable @existing", want: "True"},
		{command: "/keycloak disable @existing", want: "False"},
	} {
		response, appErr := p.keycloakCommand(&model.CommandArgs{
			UserId:  "admin",
			Command: test.command,
		})
		if appErr != nil {
			t.Fatalf("%s returned AppError: %v", test.command, appErr)
		}
		if !strings.Contains(response.Text, "Mattermost login") {
			t.Fatalf("unexpected response for %s: %s", test.command, response.Text)
		}
		if got := keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "allowMattermostLogin"); got != test.want {
			t.Fatalf("%s set allowMattermostLogin to %q, want %q", test.command, got, test.want)
		}
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

	response, appErr := p.keycloakCommand(&model.CommandArgs{
		UserId:  "admin",
		Command: "/keycloak migrate_to_google unmigrate @existing",
	})
	if appErr != nil {
		t.Fatalf("command returned AppError: %v", appErr)
	}
	if !strings.Contains(response.Text, "Unmigrated") {
		t.Fatalf("unexpected response: %s", response.Text)
	}
	if keycloakMigrationAttribute(&keycloakUserRepresentation{Attributes: gotAttributes}, "accountMigrated") != "False" {
		t.Fatalf("accountMigrated was not reset to False")
	}
	if _, ok := gotAttributes["mattermostEmail"]; ok {
		t.Fatalf("mattermostEmail was not removed")
	}
	if _, ok := gotAttributes["migratedFromUsername"]; ok {
		t.Fatalf("migratedFromUsername was not removed")
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
		"Hi @{{username}}, account {{account}}, password {{password}}",
		"alice",
		"alice@fysiksektionen.se",
		"temporary-password",
	)
	if got != "Hi @alice, account alice@fysiksektionen.se, password temporary-password" {
		t.Fatalf("rendered migration text = %q", got)
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
