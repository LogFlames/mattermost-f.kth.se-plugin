package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestKeycloakSubcommandHelp(t *testing.T) {
	p, _ := newAdminTestPlugin(t)

	if strings.Contains(keycloakMigrateUsage["migrate_to_google create-external"], "[org-unit-key]") {
		t.Fatal("create-external org-unit key is still shown as optional")
	}

	tests := map[string]string{
		"/keycloak new_user":                          "Create a domestic Keycloak account",
		"/keycloak migrate_to_google":                 "Migrate a Mattermost account from Keycloak/SAML",
		"/keycloak migrate_to_google create-kth":      "Create a KTH user's Google Workspace account",
		"/keycloak migrate_to_google create-external": "Create an external Google Workspace account",
		"/keycloak migrate_to_google bind":            "Bind an existing Google Workspace account",
		"/keycloak migrate_to_keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID",
		"/keycloak migrate_to_keycloak create":        "Create a Keycloak account",
		"/keycloak migrate_to_keycloak bind":          "Bind an existing Keycloak account",
		"/keycloak migrate_to_keycloak update":        "Synchronize the bound Keycloak account by the Mattermost user's Google ID",
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
