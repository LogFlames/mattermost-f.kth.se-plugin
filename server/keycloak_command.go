package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type keycloakNewUserRequest struct {
	Username      string               `json:"username"`
	Email         string               `json:"email"`
	EmailVerified bool                 `json:"emailVerified"`
	Enabled       bool                 `json:"enabled"`
	FirstName     string               `json:"firstName"`
	LastName      string               `json:"lastName"`
	Attributes    map[string][]string  `json:"attributes"`
	Credentials   []keycloakCredential `json:"credentials"`
}

type keycloakCredential struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

type keycloakUserRepresentation struct {
	ID         string              `json:"id"`
	Username   string              `json:"username"`
	Email      string              `json:"email"`
	FirstName  string              `json:"firstName"`
	LastName   string              `json:"lastName"`
	Attributes map[string][]string `json:"attributes"`
}

func (p *Plugin) keycloakCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if !p.API.HasPermissionTo(args.UserId, model.PermissionManageSystem) {
		response.Text = "Only system admins can run /keycloak."
		return response, nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) < 2 {
		response.Text = keycloakNewUserUsage
		return response, nil
	}

	switch fields[1] {
	case "enable", "disable":
		allowMattermostLogin := fields[1] == "enable"
		return p.setKeycloakMattermostLogin(args, fields, allowMattermostLogin)
	case "migrate_to_google":
		return p.migrateKeycloakUserToGoogle(args, fields)
	case "migrate_to_keycloak":
		return p.migrateGoogleUserToKeycloak(args, fields)
	case "new_user":
		if len(fields) == 2 {
			response.Text = keycloakUsageWithDescriptions(keycloakNewUserUsage, "new_user")
			return response, nil
		}
		if len(fields) != 8 {
			response.Text = keycloakNewUserUsage
			return response, nil
		}
		switch strings.ToLower(fields[7]) {
		case "yes":
			response.Text = "International Keycloak accounts should not be created with this command. International students should create their accounts themselves."
			return response, nil
		case "no":
		default:
			response.Text = keycloakNewUserUsage
			return response, nil
		}
		if err := validateKeycloakEmail(fields[2]); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		if err := p.createKeycloakUser(fields[2], fields[3], fields[4], fields[5], fields[6]); err != nil {
			p.API.LogError("Keycloak user creation failed", "email", fields[2], "error", err.Error())
			if strings.HasSuffix(strings.SplitN(err.Error(), ":", 2)[0], "account conflict") {
				response.Text = err.Error()
				return response, nil
			}
			response.Text = "Could not create the Keycloak account. Check the server logs for details."
			return response, nil
		}
		response.Text = fmt.Sprintf("Keycloak account created for %s with temporary password `%s`. The user will be required to change this password on first login.", fields[2], fields[3])
		return response, nil
	default:
		response.Text = keycloakNewUserUsage
		return response, nil
	}
}

var keycloakMigrateUsage map[string]string = map[string]string{
	"enable":                            "Usage: `/keycloak enable @<mattermost-user>`",
	"disable":                           "Usage: `/keycloak disable @<mattermost-user>`",
	"migrate_to_google":                 "Usage: `/keycloak migrate_to_google list-org-units|create-kth|create-external|bind|unmigrate`",
	"migrate_to_google list-org-units":  "Usage: `/keycloak migrate_to_google list-org-units`",
	"migrate_to_google create-kth":      "Usage: `/keycloak migrate_to_google create-kth @<mattermost-user-to-migrate> <kth-id> <org-unit-key>`",
	"migrate_to_google create-external": "Usage: `/keycloak migrate_to_google create-external @<mattermost-user-to-migrate> <fysiksektionen.se-address> <org-unit-key>`",
	"migrate_to_google bind":            "Usage: `/keycloak migrate_to_google bind @<mattermost-user-to-migrate> <fysiksektionen.se-address>`",
	"migrate_to_google unmigrate":       "Usage: `/keycloak migrate_to_google unmigrate @<mattermost-user-to-unmigrate>`",
	"migrate_to_keycloak":               "Usage: `/keycloak migrate_to_keycloak create|bind|update`",
	"migrate_to_keycloak create":        "Usage: `/keycloak migrate_to_keycloak create @<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate_to_keycloak bind":          "Usage: `/keycloak migrate_to_keycloak bind @<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate_to_keycloak update":        "Usage: `/keycloak migrate_to_keycloak update @<mattermost-user-to-migrate>`",
}

var keycloakSubcommandDescriptions map[string]string = map[string]string{
	"enable":                            "Allow a Mattermost user to log in through their linked Keycloak account.",
	"disable":                           "Prevent a Mattermost user from logging in through their linked Keycloak account.",
	"new_user":                          "Create a domestic Keycloak account for a Mattermost username, with a temporary password.",
	"migrate_to_google":                 "Migrate a Mattermost account from Keycloak/SAML to Google Workspace/OpenID, including its Keycloak username and migration metadata.",
	"migrate_to_google list-org-units":  "List the available org-unit keys and their Google Workspace organization units.",
	"migrate_to_google create-kth":      "Create a KTH user's Google Workspace account using an f-Ø or f-XX org-unit key, then switch the Mattermost account to Google/OpenID immediately.",
	"migrate_to_google create-external": "Create an external Google Workspace account using an alumner, external, or utility org-unit key, then switch the Mattermost account to Google/OpenID immediately.",
	"migrate_to_google bind":            "Bind an existing Google Workspace account to a Mattermost account currently using Keycloak and switch the Mattermost account to Google/OpenID immediately.",
	"migrate_to_google unmigrate":       "Reverse a Google migration and restore the Mattermost account's old Keycloak identity.",
	"migrate_to_keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID to Keycloak/SAML.",
	"migrate_to_keycloak create":        "Create a temporary-password Keycloak account from the Mattermost user's Google profile and immediately switch the Mattermost account to SAML/Keycloak.",
	"migrate_to_keycloak bind":          "Bind an existing Keycloak account to a Mattermost account currently using Google and immediately switch the Mattermost account to SAML/Keycloak.",
	"migrate_to_keycloak update":        "Synchronize a previously created or bound Keycloak account with the Mattermost user's current Google ID, username, profile data, and migration metadata.",
}

func keycloakUsageWithDescriptions(usage, subcommand string) string {
	if description := keycloakSubcommandDescriptions[subcommand]; description != "" {
		return usage + "\n" + description
	}
	return usage
}

func (p *Plugin) setKeycloakMattermostLogin(args *model.CommandArgs, fields []string, allow bool) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	action := "disable"
	if allow {
		action = "enable"
	}
	if len(fields) != 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage[action], action)
		return response, nil
	}

	user, err := p.migrationMattermostUser(fields[2])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no authentication data; cannot find the Keycloak account."
		return response, nil
	}

	keycloakUser, err := p.getKeycloakUserByMattermostID(*user.AuthData)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak account for Mattermost user %s: %v", user.Username, err)
		return response, nil
	}
	if err = p.updateKeycloakMattermostLogin(keycloakUser, allow); err != nil {
		response.Text = err.Error()
		return response, nil
	}

	if allow {
		response.Text = fmt.Sprintf("Enabled Mattermost login for @%s.", user.Username)
	} else {
		response.Text = fmt.Sprintf("Disabled Mattermost login for @%s.", user.Username)
	}
	return response, nil
}

func (p *Plugin) migrateKeycloakUserToGoogle(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google"], "migrate_to_google")
		return response, nil
	}
	if fields[2] == "list-org-units" {
		if len(fields) != 3 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate_to_google list-org-units"],
				"migrate_to_google list-org-units",
			)
			return response, nil
		}
		configuration := p.getConfiguration()
		units, err := p.listGoogleOrgUnits(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		keys := googleOrgUnitKeys(units)
		for i, key := range keys {
			for _, unit := range units {
				resolved, resolveErr := resolveGoogleOrgUnitKey(units, "external", key)
				if resolveErr == nil && resolved == unit {
					keys[i] = key + " = " + unit
					break
				}
				resolved, resolveErr = resolveGoogleOrgUnitKey(units, "kth", key)
				if resolveErr == nil && resolved == unit {
					keys[i] = key + " = " + unit
					break
				}
			}
		}
		response.Text = strings.Join(keys, "\n")
		return response, nil
	}
	if fields[2] == "unmigrate" {
		if len(fields) != 4 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate_to_google unmigrate"],
				"migrate_to_google unmigrate",
			)
			return response, nil
		}
		return p.unmigrateKeycloakUserFromGoogle(args, fields[3])
	}
	if len(fields) == 3 {
		usageKey := "migrate_to_google " + fields[2]
		if usage := keycloakMigrateUsage[usageKey]; usage != "" {
			response.Text = keycloakUsageWithDescriptions(usage, usageKey)
			return response, nil
		}
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google"], "migrate_to_google")
		return response, nil
	}
	if fields[2] != "create-kth" && fields[2] != "create-external" && fields[2] != "bind" {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google"], "migrate_to_google")
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[3])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no authentication data; cannot find the Keycloak account."
		return response, nil
	}
	if user.AuthService != model.UserAuthServiceSaml {
		response.Text = "Mattermost user is not currently authenticated with SAML/Keycloak."
		return response, nil
	}
	if !regexp.MustCompile(`^keycloak[0-9]+$`).MatchString(*user.AuthData) {
		response.Text = fmt.Sprintf("Mattermost auth data %q is not in the expected Keycloak ID format.", *user.AuthData)
		return response, nil
	}
	keycloakUser, err := p.getKeycloakUserByMattermostID(*user.AuthData)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak account for Mattermost user %s: %v", user.Username, err)
		return response, nil
	}

	var googleUser *googleDirectoryUser
	var password string
	if len(fields) < 4 {
		response.Text = keycloakUsageWithDescriptions(
			keycloakMigrateUsage["migrate_to_google "+fields[2]],
			"migrate_to_google "+fields[2],
		)
		return response, nil
	}
	if keycloakMigrationAttributeIsTrue(keycloakUser, "accountMigrated") {
		response.Text = "Keycloak account is already marked as migrated."
		return response, nil
	}
	if keycloakMigrationAttribute(keycloakUser, "mattermostEmail") != "" {
		response.Text = "Keycloak account already has a Mattermost email migration attribute."
		return response, nil
	}
	switch fields[2] {
	case "create-kth":
		if len(fields) != 6 {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google create-kth"], "migrate_to_google create-kth")
			return response, nil
		}
		password = randomPassword(16)
		configuration := p.getConfiguration()
		units, resolveErr := p.listGoogleOrgUnits(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail)
		if resolveErr != nil {
			response.Text = resolveErr.Error()
			return response, nil
		}
		orgUnit, resolveErr := resolveGoogleOrgUnitKey(units, "kth", fields[5])
		if resolveErr != nil {
			response.Text = resolveErr.Error()
			return response, nil
		}
		googleEmail := fmt.Sprintf("%s@fysiksektionen.se", fields[4])
		googleUser, err = p.createGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, googleEmail, keycloakUser.FirstName, keycloakUser.LastName, orgUnit, password, keycloakUser.Email)
	case "create-external":
		if len(fields) != 6 {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google create-external"], "migrate_to_google create-external")
			return response, nil
		}
		if err = validateFysiksektionenEmail(fields[4]); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		password = randomPassword(16)
		configuration := p.getConfiguration()
		units, err := p.listGoogleOrgUnits(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		orgUnit, err := resolveGoogleOrgUnitKey(units, "external", fields[5])
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		googleUser, err = p.createGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fields[4], keycloakUser.FirstName, keycloakUser.LastName, orgUnit, password, keycloakUser.Email)
	case "bind":
		if len(fields) != 5 {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_google bind"], "migrate_to_google bind")
			return response, nil
		}
		if err = validateFysiksektionenEmail(fields[4]); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		configuration := p.getConfiguration()
		googleUser, err = p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fields[4])
		if err != nil {
			err = fmt.Errorf("Google account %q does not exist; use create-kth or create-external first", fields[4])
		}
	default:
		response.Text = keycloakMigrateUsage["migrate_to_google"]
		return response, nil
	}
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if fields[2] == "bind" {
		configuration := p.getConfiguration()
		alternativeEmail := alternativeGoogleEmail(fields[4], keycloakUser.Email)
		if alternativeEmail != "" {
			if err = p.updateGoogleUserAlternativeEmail(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, googleUser.ID, alternativeEmail); err != nil {
				response.Text = err.Error()
				return response, nil
			}
		}
	}
	if err = p.checkGoogleMigrationConflicts(user, googleUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.checkKeycloakMigrationConflicts(googleUser.ID, googleUser.PrimaryEmail, keycloakUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateKeycloakMigrationProfile(keycloakUser, true, *user.AuthData, googleUser.ID, googleUser.PrimaryEmail, false, false, keycloakUser.FirstName, keycloakUser.LastName, keycloakUser.Email, keycloakUser.Username, googleUsername(googleUser.PrimaryEmail)); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateMattermostIdentity(user, model.ServiceOpenid, googleUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if fields[2] == "create-kth" || fields[2] == "create-external" || fields[2] == "bind" {
		p.notifyMigrationInitiator(args.UserId, user.Username, keycloakUser.Username, password, fields[2])
	}
	response.Text = fmt.Sprintf("Migrated @%s to Google account %s. The Mattermost account now uses Google/OpenID authentication with Google ID %s.", user.Username, googleUser.PrimaryEmail, googleUser.ID)
	if password != "" {
		response.Text += fmt.Sprintf("\n\nTemporary Google password: `%s`", password)
	}
	return response, nil
}

func (p *Plugin) unmigrateKeycloakUserFromGoogle(args *model.CommandArgs, reference string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	user, err := p.migrationMattermostUser(reference)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if user.AuthService != model.ServiceOpenid {
		response.Text = "Mattermost user is not currently authenticated with Google/OpenID."
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no Google authentication data."
		return response, nil
	}

	keycloakUser, err := p.getKeycloakUserByMattermostID(*user.AuthData)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak migration account for Mattermost user %s: %v", user.Username, err)
		return response, nil
	}
	if !keycloakMigrationAttributeIsTrue(keycloakUser, "accountMigrated") {
		response.Text = "Keycloak account is not marked as migrated."
		return response, nil
	}
	if keycloakMigrationAttribute(keycloakUser, "mattermostID") != *user.AuthData {
		response.Text = "Google account ID does not match the Keycloak migration mattermostID attribute."
		return response, nil
	}
	oldMattermostID := keycloakMigrationAttribute(keycloakUser, "migratedFromID")
	if !regexp.MustCompile(`^keycloak[0-9]+$`).MatchString(oldMattermostID) {
		response.Text = "Keycloak account is missing or has invalid migratedFromID migration data."
		return response, nil
	}
	oldMattermostUsername := keycloakMigrationAttribute(keycloakUser, "migratedFromUsername")
	if oldMattermostUsername == "" {
		response.Text = "Keycloak account is missing migratedFromUsername migration data."
		return response, nil
	}

	if err = p.updateKeycloakMigrationProfile(keycloakUser, false, "", oldMattermostID, "", true, true, keycloakUser.FirstName, keycloakUser.LastName, keycloakUser.Email, "", oldMattermostUsername); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateMattermostIdentity(user, model.UserAuthServiceSaml, oldMattermostID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	response.Text = fmt.Sprintf("Unmigrated @%s back to Keycloak account %s.", user.Username, keycloakUser.Username)
	return response, nil
}

func (p *Plugin) migrateGoogleUserToKeycloak(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_keycloak"], "migrate_to_keycloak")
		return response, nil
	}
	if len(fields) == 3 {
		usageKey := "migrate_to_keycloak " + fields[2]
		if usage := keycloakMigrateUsage[usageKey]; usage != "" {
			response.Text = keycloakUsageWithDescriptions(usage, usageKey)
			return response, nil
		}
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_keycloak"], "migrate_to_keycloak")
		return response, nil
	}
	if len(fields) < 4 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_keycloak"], "migrate_to_keycloak")
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[3])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if user.AuthService != model.ServiceOpenid {
		response.Text = "Mattermost user is not currently authenticated with Google/OpenID."
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no Google Auth Data; cannot migrate to Keycloak."
		return response, nil
	}
	configuration := p.getConfiguration()
	googleUser, err := p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, *user.AuthData)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if googleUser.ID != *user.AuthData {
		response.Text = "Google account ID does not match the Mattermost Google auth data."
		return response, nil
	}
	if fields[2] == "create" || fields[2] == "bind" {
		if err = p.checkGoogleMigrationConflicts(user, googleUser.ID); err != nil {
			response.Text = err.Error()
			return response, nil
		}
	}
	keycloakEmail := ""
	if fields[2] != "update" {
		if len(fields) != 5 {
			usageKey := "migrate_to_keycloak " + fields[2]
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage[usageKey], usageKey)
			return response, nil
		}
		keycloakEmail = fields[4]
	}
	var keycloakUser *keycloakUserRepresentation
	migrationPassword := ""
	switch fields[2] {
	case "create":
		if len(fields) != 5 {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate_to_keycloak create"], "migrate_to_keycloak create")
			return response, nil
		}
		if err = validateKeycloakEmail(keycloakEmail); err == nil {
			err = p.checkGoogleMigrationKeycloakConflicts(googleUser.ID, googleUser.PrimaryEmail)
		}
		if err == nil {
			migrationPassword = randomPassword(16)
			err = p.createKeycloakMigrationUser(keycloakEmail, migrationPassword, googleUser.Name.GivenName, googleUser.Name.FamilyName, user.Username)
		}
		if err == nil {
			keycloakUser, err = p.getKeycloakUser(keycloakEmail)
		}
	case "bind":
		if len(fields) != 5 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate_to_keycloak "+fields[2]],
				"migrate_to_keycloak "+fields[2],
			)
			return response, nil
		}
		keycloakUser, err = p.getKeycloakUser(keycloakEmail)
		if err != nil {
			err = fmt.Errorf("Keycloak account %q does not exist", keycloakEmail)
		}
		if err == nil {
			if keycloakMigrationAttributeIsTrue(keycloakUser, "accountMigrated") {
				err = fmt.Errorf("Keycloak account is already marked as migrated; use update instead")
			} else if keycloakMigrationAttribute(keycloakUser, "mattermostEmail") != "" {
				err = fmt.Errorf("Keycloak account already has a Mattermost email migration attribute")
			} else if err = p.checkGoogleMigrationKeycloakConflicts(googleUser.ID, googleUser.PrimaryEmail, keycloakUser.ID); err == nil {
				oldMattermostID := keycloakMigrationAttribute(keycloakUser, "mattermostID")
				if !regexp.MustCompile(`^keycloak[0-9]+$`).MatchString(oldMattermostID) {
					err = fmt.Errorf("Keycloak account is missing or has invalid mattermostID migration data")
				}
			}
		}
	case "update":
		if len(fields) != 4 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate_to_keycloak update"],
				"migrate_to_keycloak update",
			)
			return response, nil
		}
		keycloakUser, err = p.findKeycloakUserByQuery("q", "mattermostID:"+googleUser.ID, false)
		if err != nil {
			err = fmt.Errorf("Could not find the Keycloak migration account for Google ID %q: %w", googleUser.ID, err)
		} else if keycloakUser == nil {
			err = fmt.Errorf("Could not find the Keycloak migration account for Google ID %q", googleUser.ID)
		}
		if err == nil {
			if !keycloakMigrationAttributeIsTrue(keycloakUser, "accountMigrated") {
				err = fmt.Errorf("Keycloak account has not been marked as migrated; use create or bind before update")
			} else if keycloakMigrationAttribute(keycloakUser, "mattermostID") != googleUser.ID {
				err = fmt.Errorf("Google account ID does not match the Keycloak migration mattermostID attribute")
			}
		}
	default:
		response.Text = keycloakMigrateUsage["migrate_to_keycloak"]
		return response, nil
	}
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if fields[2] == "create" {
		if err = p.updateKeycloakMigrationProfile(keycloakUser, true, "", googleUser.ID, googleUser.PrimaryEmail, false, true, googleUser.Name.GivenName, googleUser.Name.FamilyName, googleUser.PrimaryEmail, keycloakUser.Username, googleUsername(googleUser.PrimaryEmail)); err != nil {
			response.Text = err.Error()
			return response, nil
		}
	} else if fields[2] == "bind" {
		oldMattermostID := keycloakMigrationAttribute(keycloakUser, "mattermostID")
		if err = p.updateKeycloakMigrationProfile(keycloakUser, true, oldMattermostID, googleUser.ID, googleUser.PrimaryEmail, false, true, keycloakUser.FirstName, keycloakUser.LastName, keycloakUser.Email, keycloakUser.Username, googleUsername(googleUser.PrimaryEmail)); err != nil {
			response.Text = err.Error()
			return response, nil
		}
	} else {
		migratedFromUsername := keycloakMigrationAttribute(keycloakUser, "migratedFromUsername")
		if migratedFromUsername == "" {
			migratedFromUsername = keycloakUser.Username
		}
		if err = p.updateKeycloakMigrationProfile(keycloakUser, true, keycloakMigrationAttribute(keycloakUser, "migratedFromID"), googleUser.ID, googleUser.PrimaryEmail, false, true, googleUser.Name.GivenName, googleUser.Name.FamilyName, googleUser.PrimaryEmail, migratedFromUsername, googleUsername(googleUser.PrimaryEmail)); err != nil {
			response.Text = err.Error()
			return response, nil
		}
	}
	if err = p.updateMattermostIdentity(user, model.UserAuthServiceSaml, keycloakUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	response.Text = fmt.Sprintf("Migrated @%s to Keycloak account %s. The Mattermost account now uses SAML/Keycloak authentication.", user.Username, keycloakUser.Username)
	if fields[2] == "create" && migrationPassword != "" {
		response.Text += fmt.Sprintf("\n\nTemporary password: `%s`\nKeycloak will require this password to be changed upon first log-in.", migrationPassword)
	}
	return response, nil
}

const (
	defaultKeycloakMigrationDMText    = "Example DM text:\n\nHi @{{username}},\n\nYour account has successfully been migrated. You can now use {{account}}."
	defaultKeycloakMigrationEmailText = "Example e-mail text:\n\nHello {{username}},\n\nYour account migration was successful. Your account is: {{account}}."
)

func renderKeycloakMigrationText(text, username, account, password string) string {
	replacer := strings.NewReplacer(
		"{{username}}", username,
		"{{account}}", account,
		"{{password}}", password,
	)
	return replacer.Replace(text)
}

func keycloakMigrationNotificationTexts(configuration *configuration, googleCreationMethod string) (string, string) {
	dmText := configuration.KeycloakMigrationDMText
	emailText := configuration.KeycloakMigrationEmailText

	switch googleCreationMethod {
	case "create-kth":
		if strings.TrimSpace(configuration.KeycloakMigrationKTHDMText) != "" {
			dmText = configuration.KeycloakMigrationKTHDMText
		}
		if strings.TrimSpace(configuration.KeycloakMigrationKTHEmailText) != "" {
			emailText = configuration.KeycloakMigrationKTHEmailText
		}
	case "create-external":
		if strings.TrimSpace(configuration.KeycloakMigrationExternalDMText) != "" {
			dmText = configuration.KeycloakMigrationExternalDMText
		}
		if strings.TrimSpace(configuration.KeycloakMigrationExternalEmailText) != "" {
			emailText = configuration.KeycloakMigrationExternalEmailText
		}
	}

	if strings.TrimSpace(dmText) == "" {
		dmText = defaultKeycloakMigrationDMText
	}
	if strings.TrimSpace(emailText) == "" {
		emailText = defaultKeycloakMigrationEmailText
	}
	return dmText, emailText
}

func (p *Plugin) notifyMigrationInitiator(userID, username, account, password, googleCreationMethod string) {
	dmText, emailText := keycloakMigrationNotificationTexts(p.getConfiguration(), googleCreationMethod)

	dm, err := p.API.GetDirectChannel(userID, p.pluginBot.UserId)
	if err != nil {
		p.API.LogError("Could not create migration notification direct-message channel", "user_id", userID, "error", err.Error())
		return
	}

	for _, text := range []string{
		renderKeycloakMigrationText(dmText, username, account, password),
		renderKeycloakMigrationText(emailText, username, account, password),
	} {
		if _, appErr := p.API.CreatePost(&model.Post{
			UserId:    p.pluginBot.UserId,
			ChannelId: dm.Id,
			Message:   text,
		}); appErr != nil {
			p.API.LogError("Could not send migration notification direct message", "user_id", userID, "error", appErr.Error())
		}
	}
}

func keycloakMigrationAttribute(user *keycloakUserRepresentation, name string) string {
	if user == nil || user.Attributes == nil {
		return ""
	}
	values := user.Attributes[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func keycloakMigrationAttributeIsTrue(user *keycloakUserRepresentation, name string) bool {
	return strings.EqualFold(keycloakMigrationAttribute(user, name), "true")
}

func keycloakBooleanString(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func (p *Plugin) checkGoogleMigrationConflicts(user *model.User, googleID string) error {
	page := 0
	for {
		users, appErr := p.API.GetUsers(&model.UserGetOptions{Page: page, PerPage: 200})
		if appErr != nil {
			return fmt.Errorf("check Mattermost Google ID conflicts: %w", appErr)
		}
		for _, candidate := range users {
			if candidate == nil || candidate.Id == user.Id {
				continue
			}
			if candidate.AuthData != nil && *candidate.AuthData == googleID {
				return fmt.Errorf("Mattermost account conflict: Google ID %q belongs to another Mattermost account", googleID)
			}
		}
		if len(users) < 200 {
			break
		}
		page++
	}
	return nil
}

func (p *Plugin) checkKeycloakMigrationConflicts(googleID, googleEmail string, allowedKeycloakUserID ...string) error {
	isAllowed := func(user *keycloakUserRepresentation) bool {
		return user != nil && len(allowedKeycloakUserID) > 0 && user.ID == allowedKeycloakUserID[0]
	}
	if user, err := p.findKeycloakUserByQuery("q", "mattermostID:"+googleID, false); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google ID %q is already a mattermostID", googleID)
	}
	if user, err := p.findKeycloakUserByQuery("email", googleEmail, true); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google e-mail %q is already a Keycloak account e-mail", googleEmail)
	}
	if user, err := p.findKeycloakUserByQuery("q", "mattermostEmail:"+googleEmail, false); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google e-mail %q is already a Keycloak mattermostEmail", googleEmail)
	}
	return nil
}

func (p *Plugin) checkGoogleMigrationKeycloakConflicts(googleID, googleEmail string, allowedKeycloakUserID ...string) error {
	isAllowed := func(user *keycloakUserRepresentation) bool {
		return user != nil && len(allowedKeycloakUserID) > 0 && user.ID == allowedKeycloakUserID[0]
	}
	if user, err := p.findKeycloakUserByQuery("q", "mattermostID:"+googleID, false); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google ID %q is already a mattermostID", googleID)
	}
	if user, err := p.findKeycloakUserByQuery("email", googleEmail, true); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google e-mail %q is already a Keycloak account e-mail", googleEmail)
	}
	if user, err := p.findKeycloakUserByQuery("q", "mattermostEmail:"+googleEmail, false); err != nil {
		return err
	} else if user != nil && !isAllowed(user) {
		return fmt.Errorf("Keycloak account conflict: Google e-mail %q is already a Keycloak mattermostEmail", googleEmail)
	}
	return nil
}

func (p *Plugin) createKeycloakMigrationUser(email, password, firstName, lastName, mattermostUsername string) error {
	if err := validateKeycloakEmail(email); err != nil {
		return err
	}
	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return fmt.Errorf("Keycloak client credentials are not configured")
	}
	payload := keycloakNewUserRequest{
		Username:      email,
		Email:         email,
		EmailVerified: true,
		Enabled:       true,
		FirstName:     firstName,
		LastName:      lastName,
		Attributes: map[string][]string{
			"mattermostUsername": {mattermostUsername},
			"creationMethod":     {"MattermostPluginCommand"},
			"accountType":        {"Unknown (created by Mattermost plugin)"},
			"accountMigrated":    {"False"},
		},
		Credentials: []keycloakCredential{{Type: "password", Value: password, Temporary: true}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Keycloak migration user: %w", err)
	}
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if conflict, err := p.keycloakUserConflict(token, "email", email); err != nil {
		return err
	} else if conflict {
		return fmt.Errorf("Keycloak account conflict: the e-mail %q is already in use", email)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, url.PathEscape(keycloakRealm))
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Keycloak migration user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("create Keycloak migration user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		details, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("Keycloak migration user endpoint returned HTTP %d (read error response: %w)", response.StatusCode, readErr)
		}
		return fmt.Errorf("Keycloak migration user endpoint returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}

const keycloakNewUserUsage = "Usage: `/keycloak new_user <email> <temp-password> <first-name> <last-name> <mattermost-username> <international: yes|no>`"

func validateKeycloakEmail(email string) error {
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return fmt.Errorf("Invalid e-mail address %q. Use an address such as name@example.com.", email)
	}
	return nil
}

func validateFysiksektionenEmail(email string) error {
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return fmt.Errorf("Invalid e-mail address %q. Use a fysiksektionen.se-address such as name@fysiksektionen.se.", email)
	}
	if !strings.HasSuffix(strings.ToLower(email), "@fysiksektionen.se") {
		return fmt.Errorf("Invalid @fysiksektionen.se address %q. Include the @fysiksektionen.se suffix, for example name@fysiksektionen.se.", email)
	}
	return nil
}

func (p *Plugin) createKeycloakUser(email, password, firstName, lastName, mattermostUsername string) error {
	if err := validateKeycloakEmail(email); err != nil {
		return err
	}
	if err := p.checkMattermostUserConflicts(email, mattermostUsername); err != nil {
		return err
	}

	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return fmt.Errorf("Keycloak client credentials are not configured")
	}

	payload := keycloakNewUserRequest{
		Username:      email,
		Email:         email,
		EmailVerified: true,
		Enabled:       true,
		FirstName:     firstName,
		LastName:      lastName,
		Attributes: map[string][]string{
			"mattermostUsername":      {mattermostUsername},
			"confirmsIsInternational": {"Yes"},
			"isActuallyInternational": {"No"}, // international students should create their own accounts
			"creationMethod":          {"MattermostPluginCommand"},
			"accountType":             {"Unknown (created by Mattermost plugin)"},
			"kthProgram":              {"None"},
			"kthExpectedEndOfStudies": {"2099-12"},
		},
		Credentials: []keycloakCredential{{Type: "password", Value: password, Temporary: true}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Keycloak user: %w", err)
	}

	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if conflict, err := p.keycloakUserConflict(token, "email", email); err != nil {
		return err
	} else if conflict {
		return fmt.Errorf("Keycloak account conflict: the e-mail %q is already in use", email)
	}
	if conflict, err := p.keycloakUserConflict(token, "q", "mattermostUsername:"+mattermostUsername); err != nil {
		return err
	} else if conflict {
		return fmt.Errorf("Keycloak account conflict: the Mattermost username %q is already in use", mattermostUsername)
	}

	endpoint := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, url.PathEscape(keycloakRealm))
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Keycloak user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("create Keycloak user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		details, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("Keycloak user endpoint returned HTTP %d (read error response: %w)", response.StatusCode, readErr)
		}
		if details := strings.TrimSpace(string(details)); details != "" {
			return fmt.Errorf("Keycloak user endpoint returned HTTP %d: %s", response.StatusCode, details)
		}
		return fmt.Errorf("Keycloak user endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (p *Plugin) checkMattermostUserConflicts(email, mattermostUsername string) error {
	user, appErr := p.API.GetUserByEmail(email)
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check Mattermost e-mail conflict: %w", appErr)
	}
	if user != nil {
		return fmt.Errorf("Mattermost account conflict: the e-mail %q is already in use", email)
	}

	user, appErr = p.API.GetUserByUsername(mattermostUsername)
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check Mattermost username conflict: %w", appErr)
	}
	if user != nil {
		return fmt.Errorf("Mattermost account conflict: the username %q is already in use", mattermostUsername)
	}
	return nil
}

func (p *Plugin) keycloakUserConflict(token, parameter, value string) (bool, error) {
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users", keycloakURL, url.PathEscape(keycloakRealm))
	query := url.Values{parameter: {value}}
	if parameter == "email" {
		query.Set("exact", "true")
	}

	request, err := http.NewRequest(http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return false, fmt.Errorf("create Keycloak conflict request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return false, fmt.Errorf("check Keycloak account conflict: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Keycloak user search returned HTTP %d", response.StatusCode)
	}

	var users []json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return false, fmt.Errorf("decode Keycloak conflict response: %w", err)
	}
	return len(users) > 0, nil
}

func (p *Plugin) getKeycloakUser(email string) (*keycloakUserRepresentation, error) {
	return p.getKeycloakUserByQuery("email", email, true)
}

func (p *Plugin) getKeycloakUserByMattermostID(mattermostID string) (*keycloakUserRepresentation, error) {
	return p.getKeycloakUserByQuery("q", "mattermostID:"+mattermostID, false)
}

func (p *Plugin) findKeycloakUserByQuery(parameter, value string, exact bool) (*keycloakUserRepresentation, error) {
	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return nil, fmt.Errorf("Keycloak client credentials are not configured")
	}
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return nil, err
	}
	query := url.Values{parameter: {value}}
	if exact {
		query.Set("exact", "true")
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users?%s", keycloakURL, url.PathEscape(keycloakRealm), query.Encode())
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Keycloak account search request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("search Keycloak account: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Keycloak user search returned HTTP %d", response.StatusCode)
	}
	var users []keycloakUserRepresentation
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("decode Keycloak user response: %w", err)
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

func (p *Plugin) getKeycloakUserByQuery(parameter, value string, exact bool) (*keycloakUserRepresentation, error) {
	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return nil, fmt.Errorf("Keycloak client credentials are not configured")
	}
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return nil, err
	}
	query := url.Values{parameter: {value}}
	if exact {
		query.Set("exact", "true")
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users?%s", keycloakURL, url.PathEscape(keycloakRealm), query.Encode())
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Keycloak user request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("get Keycloak user: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Keycloak user search returned HTTP %d", response.StatusCode)
	}
	var users []keycloakUserRepresentation
	if err := json.NewDecoder(response.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("decode Keycloak user response: %w", err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("Keycloak account matching %s %q was not found", parameter, value)
	}
	return &users[0], nil
}

func googleUsername(primaryEmail string) string {
	username := strings.SplitN(primaryEmail, "@", 2)[0]
	return username
}

func (p *Plugin) updateKeycloakMigrationProfile(user *keycloakUserRepresentation, migrated bool, fromID, mattermostID, mattermostEmail string, allowProfileUpdates, allowMattermostLogin bool, firstName, lastName, email string, usernameMigration ...string) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if user.Attributes == nil {
		user.Attributes = map[string][]string{}
	}
	accountMigrated := keycloakBooleanString(migrated)
	user.Attributes["accountMigrated"] = []string{accountMigrated}
	if fromID == "" {
		delete(user.Attributes, "migratedFromID")
	} else {
		user.Attributes["migratedFromID"] = []string{fromID}
	}
	if len(usernameMigration) == 2 {
		fromUsername, username := usernameMigration[0], usernameMigration[1]
		if fromUsername == "" {
			delete(user.Attributes, "migratedFromUsername")
		} else {
			user.Attributes["migratedFromUsername"] = []string{fromUsername}
		}
		if username != "" {
			user.Username = username
		}
	}
	user.Attributes["mattermostID"] = []string{mattermostID}
	if mattermostEmail == "" {
		delete(user.Attributes, "mattermostEmail")
	} else {
		user.Attributes["mattermostEmail"] = []string{mattermostEmail}
	}
	user.Attributes["allowProfileUpdates"] = []string{keycloakBooleanString(allowProfileUpdates)}
	user.Attributes["allowMattermostLogin"] = []string{keycloakBooleanString(allowMattermostLogin)}
	if firstName != "" {
		user.FirstName = firstName
	}
	if lastName != "" {
		user.LastName = lastName
	}
	if email != "" {
		user.Email = email
	}
	body, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("encode Keycloak migration update: %w", err)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(user.ID))
	request, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Keycloak migration update: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("update Keycloak migration attributes: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Keycloak migration update returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}

func (p *Plugin) updateKeycloakMattermostLogin(user *keycloakUserRepresentation, allow bool) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if user.Attributes == nil {
		user.Attributes = map[string][]string{}
	}
	user.Attributes["allowMattermostLogin"] = []string{keycloakBooleanString(allow)}

	body, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("encode Keycloak Mattermost login update: %w", err)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(user.ID))
	request, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Keycloak Mattermost login update: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("update Keycloak Mattermost login attribute: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Keycloak Mattermost login update returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}
