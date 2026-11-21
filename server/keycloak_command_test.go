package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestKeycloakSubcommandHelp(t *testing.T) {
	p, _ := newAdminTestPlugin(t)

	tests := map[string]string{
		"/keycloak new_user":                          "Create a domestic Keycloak account",
		"/keycloak migrate_to_google":                 "Migrate a Mattermost account from Keycloak/SAML",
		"/keycloak migrate_to_google create-kth":      "Create a Google Workspace account for a KTH user",
		"/keycloak migrate_to_google create-external": "Create a Google Workspace account for an external",
		"/keycloak migrate_to_google bind":            "Bind an existing Google Workspace account",
		"/keycloak migrate_to_google update":          "Update the bound Google Workspace account",
		"/keycloak migrate_to_keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID",
		"/keycloak migrate_to_keycloak create":        "Create a Keycloak account",
		"/keycloak migrate_to_keycloak bind":          "Bind an existing Keycloak account",
		"/keycloak migrate_to_keycloak update":        "Synchronize the bound Keycloak account",
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
}
