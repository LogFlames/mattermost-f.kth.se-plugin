package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
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

type keycloakUserDetails struct {
	keycloakUserRepresentation
	Enabled       *bool `json:"enabled,omitempty"`
	EmailVerified *bool `json:"emailVerified,omitempty"`
}

const keepKeycloakLoginFlag = "--keep-mamo-login-through-keycloak"
const forceGoogleLoginFlag = "--force-mamo-login-through-google"

func (p *Plugin) registerKeycloakCommand() error {
	command := &model.Command{
		Trigger:          "keycloak",
		AutoComplete:     true,
		AutoCompleteHint: "[get|ban|unban|new-user|delete-user|reset-password|set-login-method|migrate-to-google|migrate-to-keycloak]",
		AutoCompleteDesc: "Manage or migrate a Keycloak account",
	}
	command.AutocompleteData = model.NewAutocompleteData(command.Trigger, command.AutoCompleteHint, command.AutoCompleteDesc)
	command.AutocompleteData.RoleID = model.SystemAdminRoleId
	return p.API.RegisterCommand(command)
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
	case "get":
		return p.getKeycloakAccountDetails(fields)
	case "ban", "unban":
		return p.setKeycloakAccountBan(args, fields)
	case "delete-user":
		return p.deleteKeycloakAccount(fields)
	case "reset-password":
		if len(fields) != 3 {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["reset-password"], "reset-password")
			return response, nil
		}
		return p.resetKeycloakUserPassword(args, fields[2])
	case "set-login-method":
		return p.setLoginMethod(fields)
	case "migrate-to-google":
		return p.migrateKeycloakUserToGoogle(args, fields)
	case "migrate-to-keycloak":
		return p.migrateGoogleUserToKeycloak(args, fields)
	case "new-user":
		if len(fields) == 2 {
			response.Text = keycloakUsageWithDescriptions(keycloakNewUserUsage, "new-user")
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
	"get":                               "Usage: `/keycloak get @<mattermost-user>`",
	"ban":                               "Usage: `/keycloak ban @<mattermost-user> <reason>`",
	"unban":                             "Usage: `/keycloak unban @<mattermost-user>`",
	"delete-user":                       "Usage: `/keycloak delete-user by-email <mattermost-email> [--also-deactivate]` or `/keycloak delete-user by-authdata @<mattermost-user> [--also-deactivate]`",
	"reset-password":                    "Usage: `/keycloak reset-password @<mattermost-user>`",
	"set-login-method":                  "Usage: `/keycloak set-login-method @<mattermost-user> google|saml`",
	"migrate-to-google":                 "Usage: `/keycloak migrate-to-google list-org-units|create-kth|create-external|bind|unmigrate`",
	"migrate-to-google list-org-units":  "Usage: `/keycloak migrate-to-google list-org-units`",
	"migrate-to-google create-kth":      "Usage: `/keycloak migrate-to-google create-kth @<mattermost-user-to-migrate> <kth-id> <org-unit-key> [--keep-mamo-login-through-keycloak]`",
	"migrate-to-google create-external": "Usage: `/keycloak migrate-to-google create-external @<mattermost-user-to-migrate> <fysiksektionen.se-address> <org-unit-key> [--force-mamo-login-through-google]`",
	"migrate-to-google bind":            "Usage: `/keycloak migrate-to-google bind @<mattermost-user-to-migrate> <fysiksektionen.se-address> [--keep-mamo-login-through-keycloak]`",
	"migrate-to-google unmigrate":       "Usage: `/keycloak migrate-to-google unmigrate @<mattermost-user-to-unmigrate>`",
	"migrate-to-keycloak":               "Usage: `/keycloak migrate-to-keycloak prep|create|bind|update`",
	"migrate-to-keycloak create":        "Usage: `/keycloak migrate-to-keycloak create @<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate-to-keycloak bind":          "Usage: `/keycloak migrate-to-keycloak bind @<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate-to-keycloak update":        "Usage: `/keycloak migrate-to-keycloak update @<mattermost-user-to-migrate>`",
	"migrate-to-keycloak prep":          "Usage: `/keycloak migrate-to-keycloak prep <google-account-email>`",
}

var keycloakSubcommandDescriptions map[string]string = map[string]string{
	"get":                               "Show a user's Mattermost authentication and linked Keycloak account details.",
	"ban":                               "Ban a user's Keycloak account, record the reason, and deactivate their Mattermost account.",
	"unban":                             "Unban a user's Keycloak account and clear the ban reason. This does not reactivate their Mattermost account.",
	"delete-user":                       "Permanently delete a Keycloak account matched by its mattermostEmail attribute or a Mattermost user's auth data; optionally deactivate the Mattermost user.",
	"new-user":                          "Create a domestic Keycloak account for a Mattermost username, with a temporary password.",
	"reset-password":                    "Generate a new temporary password for the user's linked Keycloak account. The password must be changed on first login.",
	"set-login-method":                  "Set Google/OpenID or Keycloak/SAML authentication when both accounts have the same authentication ID, and update Keycloak's Mattermost login permission.",
	"migrate-to-google":                 "Migrate a Keycloak account to Google, including its username and migration metadata. Login defaults to Google/OpenID for create-kth and bind, and Keycloak/SAML for create-external.",
	"migrate-to-google list-org-units":  "List the available org-unit keys and their Google Workspace organization units.",
	"migrate-to-google create-kth":      "Create a KTH user's Google Workspace account using an f-Ø or f-XX org-unit key, then update the Mattermost login method.",
	"migrate-to-google create-external": "Create an external Google Workspace account using an alumner, external, or utility org-unit key. Keep Keycloak/SAML unless --force-mamo-login-through-google is specified.",
	"migrate-to-google bind":            "Bind an existing Google Workspace account to a Mattermost account currently using Keycloak, then update the Mattermost login method.",
	"migrate-to-google unmigrate":       "Reverse a Google migration and restore the Mattermost account's old Keycloak/SAML identity.",
	"migrate-to-keycloak":               "Migrate a Mattermost account from Google Workspace/OpenID to Keycloak/SAML.",
	"migrate-to-keycloak create":        "Create a temporary-password Keycloak account from the Mattermost user's Google profile and immediately switch the Mattermost account to SAML/Keycloak.",
	"migrate-to-keycloak bind":          "Bind an existing Keycloak account to a Mattermost account currently using Google and immediately switch the Mattermost account to SAML/Keycloak.",
	"migrate-to-keycloak update":        "Synchronize a previously created or bound Keycloak account with the Mattermost user's current Google ID, username, profile data, and migration metadata.",
	"migrate-to-keycloak prep":          "Create a Mattermost user from an existing Google account, if no matching Mattermost account already exists.",
}

func keycloakUsageWithDescriptions(usage, subcommand string) string {
	if description := keycloakSubcommandDescriptions[subcommand]; description != "" {
		return usage + "\n" + description
	}
	return usage
}

func (p *Plugin) getKeycloakAccountDetails(fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) != 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["get"], "get")
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[2])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	service := user.AuthService
	if service == "" {
		service = "email/password"
	}
	auth, err := json.MarshalIndent(struct {
		Username    string  `json:"username"`
		AuthService string  `json:"auth_service"`
		AuthData    *string `json:"auth_data"`
	}{user.Username, service, user.AuthData}, "", "  ")
	if err != nil {
		response.Text = fmt.Sprintf("Could not format Mattermost authentication: %v", err)
		return response, nil
	}
	response.Text = "#### Mattermost authentication\n\n" + indentKeycloakJSON(auth)
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text += "\n\nCould not find the Keycloak account: Mattermost user has no authentication data."
		return response, nil
	}
	authData := *user.AuthData
	match, err := p.getKeycloakUserByQueryWithUniqueness("q", "mattermostID:"+authData, false, true)
	if err != nil {
		response.Text += fmt.Sprintf("\n\nCould not find a unique Keycloak account: %v", err)
		return response, nil
	}
	if match.ID == "" {
		response.Text += "\n\nCould not read the Keycloak account: the matching account has no ID."
		return response, nil
	}
	profile, err := p.getKeycloakUserDetails(match.ID)
	if err != nil {
		response.Text += fmt.Sprintf("\n\nCould not read the Keycloak account: %v", err)
		return response, nil
	}
	if profile.ID != match.ID || !slices.Contains(profile.Attributes["mattermostID"], authData) {
		response.Text += "\n\nCould not verify that the Keycloak account matches the Mattermost authentication data."
		return response, nil
	}
	details, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		response.Text += fmt.Sprintf("\n\nCould not format the Keycloak account: %v", err)
		return response, nil
	}
	response.Text += "\n\n#### Keycloak account\n\n" + indentKeycloakJSON(details)
	return response, nil
}

func indentKeycloakJSON(data []byte) string {
	return "    " + strings.ReplaceAll(string(data), "\n", "\n    ")
}

func (p *Plugin) getKeycloakUserDetails(userID string) (*keycloakUserDetails, error) {
	configuration := p.getConfiguration()
	if configuration.KeycloakClientID == "" || configuration.KeycloakClientSecret == "" {
		return nil, fmt.Errorf("Keycloak client credentials are not configured")
	}
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(userID))
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create Keycloak user details request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("get Keycloak user details: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Keycloak user details returned HTTP %d", response.StatusCode)
	}
	var user keycloakUserDetails
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode Keycloak user details: %w", err)
	}
	return &user, nil
}

func (p *Plugin) setLoginMethod(fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) != 4 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["set-login-method"], "set-login-method")
		return response, nil
	}
	var target string
	switch strings.ToLower(fields[3]) {
	case "google":
		target = model.ServiceOpenid
	case "saml":
		target = model.UserAuthServiceSaml
	default:
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["set-login-method"], "set-login-method")
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[2])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	switch user.AuthService {
	case model.ServiceOpenid, model.UserAuthServiceSaml:
	default:
		response.Text = "Mattermost user is not authenticated with Google/OpenID or Keycloak/SAML."
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no authentication data."
		return response, nil
	}
	authData := *user.AuthData
	keycloakUser, err := p.getKeycloakUserByMattermostID(authData)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak account for @%s: %v", user.Username, err)
		return response, nil
	}
	if keycloakMigrationAttribute(keycloakUser, "mattermostID") != authData {
		response.Text = "Keycloak mattermostID does not match the Mattermost authentication data."
		return response, nil
	}
	configuration := p.getConfiguration()
	googleUser, err := p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, authData)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Google account for @%s: %v", user.Username, err)
		return response, nil
	}
	if googleUser.ID != authData {
		response.Text = "Google account ID does not match the Mattermost authentication data."
		return response, nil
	}
	allowKeycloak := target == model.UserAuthServiceSaml
	previousService := user.AuthService
	previousPermission, hadPermission := keycloakUser.Attributes["allowMattermostLogin"]
	if allowKeycloak {
		if err = p.updateKeycloakMattermostLogin(keycloakUser, true); err != nil {
			response.Text = fmt.Sprintf("Could not enable Keycloak Mattermost login; Mattermost authentication was not changed: %v", err)
			return response, nil
		}
	}
	if user.AuthService != target {
		if err = p.updateMattermostIdentity(user, target, authData); err != nil {
			if allowKeycloak {
				if rollbackErr := p.writeKeycloakMattermostLogin(keycloakUser, previousPermission, hadPermission); rollbackErr != nil {
					response.Text = fmt.Sprintf("Keycloak Mattermost login was enabled, but %v; restoring the previous Keycloak permission failed: %v", err, rollbackErr)
				} else {
					response.Text = fmt.Sprintf("Could not change Mattermost authentication: %v. The previous Keycloak login permission was restored.", err)
				}
			} else {
				response.Text = fmt.Sprintf("Mattermost authentication was not changed; Keycloak Mattermost login was not changed: %v", err)
			}
			return response, nil
		}
	}
	if !allowKeycloak {
		if err = p.updateKeycloakMattermostLogin(keycloakUser, false); err != nil {
			if previousService != target {
				if rollbackErr := p.updateMattermostIdentity(user, previousService, authData); rollbackErr != nil {
					response.Text = fmt.Sprintf("Mattermost authentication was set to Google/OpenID, but Keycloak login permission could not be disabled: %v. Restoring the previous Mattermost authentication failed: %v", err, rollbackErr)
				} else {
					response.Text = fmt.Sprintf("Could not disable Keycloak Mattermost login: %v. The previous Mattermost authentication was restored.", err)
				}
			} else {
				response.Text = fmt.Sprintf("Mattermost authentication is set to Google/OpenID, but Keycloak Mattermost login permission could not be disabled: %v", err)
			}
			return response, nil
		}
	}
	if allowKeycloak {
		response.Text = fmt.Sprintf("Set @%s to Keycloak/SAML authentication and enabled Keycloak Mattermost login.", user.Username)
	} else {
		response.Text = fmt.Sprintf("Set @%s to Google/OpenID authentication and disabled Keycloak Mattermost login.", user.Username)
	}
	return response, nil
}

func (p *Plugin) deleteKeycloakAccount(fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if (len(fields) != 4 && len(fields) != 5) || (len(fields) == 5 && fields[4] != "--also-deactivate") {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["delete-user"], "delete-user")
		return response, nil
	}
	deactivate := len(fields) == 5
	var user *model.User
	var attribute, value, label string
	switch fields[2] {
	case "by-email":
		if err := validateKeycloakEmail(fields[3]); err != nil {
			response.Text = err.Error()
			return response, nil
		}
		attribute, value, label = "mattermostEmail", fields[3], fields[3]
		if deactivate {
			var appErr *model.AppError
			user, appErr = p.API.GetUserByEmail(value)
			if appErr != nil {
				response.Text = fmt.Sprintf("Could not find Mattermost user by email %q; no account was deleted: %s", value, appErr.Error())
				return response, nil
			}
			if user == nil || user.Id == "" {
				response.Text = fmt.Sprintf("Mattermost user for email %q was not found; no account was deleted.", value)
				return response, nil
			}
		}
	case "by-authdata":
		var err error
		user, err = p.migrationMattermostUser(fields[3])
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		if user.AuthData == nil || *user.AuthData == "" {
			response.Text = "Mattermost user has no authentication data; cannot find the Keycloak account."
			return response, nil
		}
		attribute, value, label = "mattermostID", *user.AuthData, "@"+user.Username
	default:
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["delete-user"], "delete-user")
		return response, nil
	}
	keycloakUser, err := p.getKeycloakUserByQueryWithUniqueness("q", attribute+":"+value, false, true)
	if err != nil {
		response.Text = fmt.Sprintf("Could not find a unique Keycloak account for %s: %v", label, err)
		return response, nil
	}
	if keycloakUser.ID == "" || keycloakMigrationAttribute(keycloakUser, attribute) != value {
		response.Text = fmt.Sprintf("Keycloak account ID or %s is missing or does not match; no account was deleted.", attribute)
		return response, nil
	}
	if err = p.deleteKeycloakUser(keycloakUser.ID); err != nil {
		response.Text = fmt.Sprintf("Could not confirm deletion of the Keycloak account for %s: %v. Mattermost deactivation was not attempted.", label, err)
		return response, nil
	}
	if deactivate {
		if appErr := p.API.UpdateUserActive(user.Id, false); appErr != nil {
			response.Text = fmt.Sprintf("Deleted the Keycloak account for @%s, but Mattermost deactivation failed or was incomplete: %s. Deactivate the Mattermost user separately.", user.Username, appErr.Error())
			return response, nil
		}
		response.Text = fmt.Sprintf("Deleted the Keycloak account and deactivated the Mattermost account for @%s.", user.Username)
	} else {
		response.Text = fmt.Sprintf("Deleted the Keycloak account for %s. The Mattermost account was not deactivated.", label)
		if user == nil {
			response.Text += " A user relying on Keycloak/SAML can no longer log in through Keycloak."
		} else if user.AuthService == model.UserAuthServiceSaml {
			response.Text += " The user can no longer log in through Keycloak/SAML."
		}
	}
	return response, nil
}

func (p *Plugin) deleteKeycloakUser(userID string) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(userID))
	request, err := http.NewRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create Keycloak account deletion request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("delete Keycloak account: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		details, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return fmt.Errorf("Keycloak account deletion returned HTTP %d (read error response: %w)", response.StatusCode, readErr)
		}
		return fmt.Errorf("Keycloak account deletion returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}

func (p *Plugin) setKeycloakAccountBan(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	ban := fields[1] == "ban"
	if (ban && len(fields) < 4) || (!ban && len(fields) != 3) {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage[fields[1]], fields[1])
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
	reason := strings.Join(fields[3:], " ")
	if err = p.updateKeycloakAccountBan(keycloakUser, ban, reason); err != nil {
		response.Text = err.Error()
		return response, nil
	}

	if ban {
		if appErr := p.API.UpdateUserActive(user.Id, false); appErr != nil {
			response.Text = fmt.Sprintf("The Keycloak account for @%s was banned, but Mattermost deactivation failed or was incomplete: %s. Retry the ban command.", user.Username, appErr.Error())
		} else {
			response.Text = fmt.Sprintf("Banned the Keycloak account and deactivated the Mattermost account for @%s.", user.Username)
		}
	} else {
		response.Text = fmt.Sprintf("Unbanned the Keycloak account for @%s. Their Mattermost account was not reactivated.", user.Username)
	}
	if err := p.logKeycloakModerationDecision(args.UserId, user.Username, ban, reason); err != nil {
		p.API.LogError("Could not post Keycloak moderation log", "user_id", args.UserId, "error", err.Error())
		response.Text += "\n\nCould not post the moderation log. Check the server logs for details."
	}
	return response, nil
}

func (p *Plugin) logKeycloakModerationDecision(adminID, username string, ban bool, reason string) error {
	channelID := p.getConfiguration().KeycloakModerationLogChannelID
	if channelID == "" {
		return nil
	}
	admin, appErr := p.API.GetUser(adminID)
	if appErr != nil {
		return fmt.Errorf("get moderation administrator: %w", appErr)
	}
	if admin == nil || admin.Username == "" {
		return fmt.Errorf("moderation administrator %q has no username", adminID)
	}
	message := fmt.Sprintf("@%s unbanned @%s", admin.Username, username)
	if ban {
		message = fmt.Sprintf("@%s banned @%s for the reason: %s", admin.Username, username, reason)
	}
	if _, appErr = p.API.CreatePost(&model.Post{
		UserId: p.pluginBot.UserId, ChannelId: channelID, Message: message,
	}); appErr != nil {
		return fmt.Errorf("post moderation decision: %w", appErr)
	}
	return nil
}

func (p *Plugin) resetKeycloakUserPassword(args *model.CommandArgs, reference string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	user, err := p.migrationMattermostUser(reference)
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
		keycloakUser, err = p.findKeycloakUserByQuery("q", "mattermostUsername:"+user.Username, false)
	}
	if err != nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak account for Mattermost user %s: %v", user.Username, err)
		return response, nil
	}
	if keycloakUser == nil {
		response.Text = fmt.Sprintf("Could not find the Keycloak account for Mattermost user %s.", user.Username)
		return response, nil
	}

	password := randomPassword(16)
	if err = p.resetKeycloakPassword(keycloakUser, password); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	response.Text = fmt.Sprintf("Reset the password for @%s's Keycloak account %s.\n\nTemporary password: `%s`\nThe user will be required to change this password on first login.", user.Username, keycloakUser.Username, password)
	return response, nil
}

func (p *Plugin) migrateKeycloakUserToGoogle(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-google"], "migrate-to-google")
		return response, nil
	}
	if fields[2] == "list-org-units" {
		if len(fields) != 3 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate-to-google list-org-units"],
				"migrate-to-google list-org-units",
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
				keycloakMigrateUsage["migrate-to-google unmigrate"],
				"migrate-to-google unmigrate",
			)
			return response, nil
		}
		return p.unmigrateKeycloakUserFromGoogle(args, fields[3])
	}
	if len(fields) == 3 {
		usageKey := "migrate-to-google " + fields[2]
		if usage := keycloakMigrateUsage[usageKey]; usage != "" {
			response.Text = keycloakUsageWithDescriptions(usage, usageKey)
			return response, nil
		}
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-google"], "migrate-to-google")
		return response, nil
	}
	if fields[2] != "create-kth" && fields[2] != "create-external" && fields[2] != "bind" {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-google"], "migrate-to-google")
		return response, nil
	}
	usageKey := "migrate-to-google " + fields[2]
	expectedLength := 6
	if fields[2] == "bind" {
		expectedLength = 5
	}
	keepKeycloakLogin := fields[2] == "create-external"
	if len(fields) == expectedLength+1 && ((fields[2] == "create-external" && fields[len(fields)-1] == forceGoogleLoginFlag) ||
		(fields[2] != "create-external" && fields[len(fields)-1] == keepKeycloakLoginFlag)) {
		keepKeycloakLogin = !keepKeycloakLogin
		fields = fields[:len(fields)-1]
	}
	if len(fields) != expectedLength {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage[usageKey], usageKey)
		return response, nil
	}
	for _, field := range fields[3:] {
		if strings.HasPrefix(field, "--") {
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage[usageKey], usageKey)
			return response, nil
		}
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
		response.Text = keycloakMigrateUsage["migrate-to-google"]
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
	previousKeycloakUser := *keycloakUser
	previousKeycloakUser.Attributes = maps.Clone(keycloakUser.Attributes)
	if err = p.updateKeycloakMigrationProfile(keycloakUser, true, *user.AuthData, googleUser.ID, googleUser.PrimaryEmail, false, keepKeycloakLogin, keycloakUser.FirstName, keycloakUser.LastName, keycloakUser.Email, keycloakUser.Username, googleUsername(googleUser.PrimaryEmail)); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	targetService := model.ServiceOpenid
	if keepKeycloakLogin {
		targetService = model.UserAuthServiceSaml
	}
	if err = p.updateMattermostIdentity(user, targetService, googleUser.ID); err != nil {
		if rollbackErr := p.writeKeycloakMigrationProfile(&previousKeycloakUser); rollbackErr != nil {
			response.Text = fmt.Sprintf("Keycloak migration profile was updated, but %v; restoring the previous Keycloak profile failed: %v", err, rollbackErr)
		} else {
			response.Text = fmt.Sprintf("Could not update Mattermost authentication: %v. The previous Keycloak profile was restored; the Google account may already have been created or updated.", err)
		}
		if password != "" && fields[2] == "create-external" {
			response.Text += fmt.Sprintf("\n\nTemporary Google password: `%s`", password)
		}
		return response, nil
	}
	if fields[2] == "create-kth" || fields[2] == "create-external" || fields[2] == "bind" {
		p.notifyMigrationInitiator(args.UserId, user.Username, googleUser.PrimaryEmail, keycloakUser.Email, password, fields[2], keepKeycloakLogin)
	}
	response.Text = fmt.Sprintf("Migrated @%s to Google account %s with Google ID %s. %s", user.Username, googleUser.PrimaryEmail, googleUser.ID, keycloakLoginPermissionMessage(keepKeycloakLogin))
	if password != "" && fields[2] == "create-external" {
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
	if user.AuthService != model.ServiceOpenid && user.AuthService != model.UserAuthServiceSaml {
		response.Text = "Mattermost user is not currently authenticated with Google/OpenID or Keycloak/SAML."
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
		response.Text = fmt.Sprintf("Keycloak migration profile was restored, but %v", err)
		return response, nil
	}
	response.Text = fmt.Sprintf("Unmigrated @%s back to Keycloak account %s.", user.Username, keycloakUser.Username)
	return response, nil
}

func (p *Plugin) prepMattermostUserFromGoogle(googleEmail string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if err := validateFysiksektionenEmail(googleEmail); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	configuration := p.getConfiguration()
	googleUser, err := p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, googleEmail)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if googleUser.PrimaryEmail == "" || googleUser.ID == "" {
		response.Text = "Google account response is missing an account ID or primary e-mail."
		return response, nil
	}

	existing, appErr := p.API.GetUserByEmail(googleUser.PrimaryEmail)
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		response.Text = fmt.Sprintf("Could not check for an existing Mattermost user by e-mail: %s", appErr.Error())
		return response, nil
	}
	if existing != nil {
		response.Text = fmt.Sprintf("Mattermost user @%s already exists for Google account %s; no account was created.", existing.Username, googleUser.PrimaryEmail)
		return response, nil
	}

	username := googleUsername(googleUser.PrimaryEmail)
	existing, appErr = p.API.GetUserByUsername(username)
	if appErr != nil && appErr.StatusCode != http.StatusNotFound {
		response.Text = fmt.Sprintf("Could not check for an existing Mattermost username: %s", appErr.Error())
		return response, nil
	}
	if existing != nil {
		response.Text = fmt.Sprintf("Mattermost username @%s is already in use by another account; no account was created.", username)
		return response, nil
	}

	page := 0
	for {
		users, getErr := p.API.GetUsers(&model.UserGetOptions{Page: page, PerPage: 200})
		if getErr != nil {
			response.Text = fmt.Sprintf("Could not check Mattermost accounts for Google ID conflicts: %s", getErr.Error())
			return response, nil
		}
		for _, candidate := range users {
			if candidate != nil && candidate.AuthData != nil && *candidate.AuthData == googleUser.ID {
				response.Text = fmt.Sprintf("Mattermost user @%s already uses Google account ID %s; no account was created.", candidate.Username, googleUser.ID)
				return response, nil
			}
		}
		if len(users) < 200 {
			break
		}
		page++
	}

	newUser := &model.User{
		Username:    username,
		Email:       googleUser.PrimaryEmail,
		FirstName:   googleUser.Name.GivenName,
		LastName:    googleUser.Name.FamilyName,
		AuthService: model.ServiceOpenid,
		AuthData:    model.NewPointer(googleUser.ID),
	}
	created, appErr := p.API.CreateUser(newUser)
	if appErr != nil {
		response.Text = fmt.Sprintf("Could not create Mattermost user for Google account %s: %s", googleUser.PrimaryEmail, appErr.Error())
		return response, nil
	}
	if created == nil {
		response.Text = "Mattermost did not return the newly created user."
		return response, nil
	}
	if err = p.OIDCOnboarding_UserCreated(created); err != nil {
		response.Text = fmt.Sprintf("Created Mattermost user @%s for Google account %s, but default-team onboarding failed: %v", created.Username, googleUser.PrimaryEmail, err)
		return response, nil
	}
	response.Text = fmt.Sprintf("Created Mattermost user @%s for Google account %s with Google ID %s.", created.Username, googleUser.PrimaryEmail, googleUser.ID)
	return response, nil
}

func (p *Plugin) migrateGoogleUserToKeycloak(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 3 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-keycloak"], "migrate-to-keycloak")
		return response, nil
	}
	if len(fields) == 3 {
		usageKey := "migrate-to-keycloak " + fields[2]
		if usage := keycloakMigrateUsage[usageKey]; usage != "" {
			response.Text = keycloakUsageWithDescriptions(usage, usageKey)
			return response, nil
		}
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-keycloak"], "migrate-to-keycloak")
		return response, nil
	}
	if fields[2] == "prep" {
		if len(fields) != 4 {
			response.Text = keycloakUsageWithDescriptions(
				keycloakMigrateUsage["migrate-to-keycloak prep"],
				"migrate-to-keycloak prep",
			)
			return response, nil
		}
		return p.prepMattermostUserFromGoogle(fields[3])
	}
	if len(fields) < 4 {
		response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-keycloak"], "migrate-to-keycloak")
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
			usageKey := "migrate-to-keycloak " + fields[2]
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
			response.Text = keycloakUsageWithDescriptions(keycloakMigrateUsage["migrate-to-keycloak create"], "migrate-to-keycloak create")
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
				keycloakMigrateUsage["migrate-to-keycloak "+fields[2]],
				"migrate-to-keycloak "+fields[2],
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
				keycloakMigrateUsage["migrate-to-keycloak update"],
				"migrate-to-keycloak update",
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
		response.Text = keycloakMigrateUsage["migrate-to-keycloak"]
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
	if err = p.updateMattermostIdentity(user, model.UserAuthServiceSaml, googleUser.ID); err != nil {
		response.Text = fmt.Sprintf("Keycloak migration profile was updated, but %v", err)
		if migrationPassword != "" {
			response.Text += fmt.Sprintf("\n\nTemporary Keycloak password: `%s`", migrationPassword)
		}
		return response, nil
	}
	response.Text = fmt.Sprintf("Migrated @%s to Keycloak account %s. The Mattermost account now uses SAML/Keycloak authentication.", user.Username, keycloakUser.Username)
	if fields[2] == "create" && migrationPassword != "" {
		response.Text += fmt.Sprintf("\n\nTemporary password: `%s`\nKeycloak will require this password to be changed upon first log-in.", migrationPassword)
	}
	return response, nil
}

const (
	defaultKeycloakMigrationDMText    = "Hi @{{username}},\n\nYour Mattermost account has been migrated to Google account {{account}}. {{login_instructions}}\n\nIf you have any questions, you can DM me or get help at mattermaster@f.kth.se."
	defaultKeycloakMigrationEmailText = "Hello {{username}},\n\nYour Mattermost account has been migrated to Google account {{account}}. {{login_instructions}}\n\nIf you have any questions, reply to this e-mail or get help from mattermaster@f.kth.se."
)

func keycloakLoginPermissionMessage(allow bool) string {
	if allow {
		return "Mattermost uses Keycloak/SAML authentication and Keycloak's Mattermost login permission is enabled."
	}
	return "Mattermost uses Google/OpenID authentication and Keycloak's Mattermost login permission is disabled."
}

func keycloakMigrationLoginInstructions(googleCreationMethod string, keepKeycloakLogin bool, account string) string {
	if keepKeycloakLogin {
		return "Although a Google account has been created for you, you should continue signing in to Mattermost through the 'International Student' sign-in."
	}
	switch googleCreationMethod {
	case "create-external":
		return fmt.Sprintf("To sign in to Mattermost, first log-in to %s at accounts.google.com with your temporary password, then use the KTH button in Mattermost with your @fysiksektionen.se account.", account)
	case "create-kth":
		return "To sign in to Mattermost, select the KTH button and sign in with your KTH account."
	default:
		return "To sign in to Mattermost, use the @fysiksektionen.se-account associated with your Mattermost account."
	}
}

func renderKeycloakMigrationText(text, username, account, password, googleCreationMethod string, keepKeycloakLogin bool) string {
	loginInstructions := keycloakMigrationLoginInstructions(googleCreationMethod, keepKeycloakLogin, account)
	if !strings.Contains(text, "{{login_instructions}}") && !strings.Contains(text, "{{login_method}}") {
		text += loginInstructions
	}
	replacer := strings.NewReplacer(
		"{{username}}", username,
		"{{account}}", account,
		"{{password}}", password,
		"{{login_instructions}}", loginInstructions,
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

func (p *Plugin) notifyMigrationInitiator(userID, username, googleEmail, keycloakEmail, password, googleCreationMethod string, keepKeycloakLogin bool) {
	dmText, emailText := keycloakMigrationNotificationTexts(p.getConfiguration(), googleCreationMethod)

	dm, err := p.API.GetDirectChannel(userID, p.pluginBot.UserId)
	if err != nil {
		p.API.LogError("Could not create migration notification direct-message channel", "user_id", userID, "error", err.Error())
		return
	}

	root, err := p.API.CreatePost(&model.Post{
		UserId:    p.pluginBot.UserId,
		ChannelId: dm.Id,
		Message:   fmt.Sprintf("You migrated @%s to Google account %s. Their Keycloak e-mail is %s. %s Please review the attached example DM and e-mail before sending them.", username, googleEmail, keycloakEmail, keycloakLoginPermissionMessage(keepKeycloakLogin)),
	})
	if err != nil {
		p.API.LogError("Could not send migration notification thread", "user_id", userID, "error", err.Error())
		return
	}

	for _, text := range []string{
		renderKeycloakMigrationText(dmText, username, googleEmail, password, googleCreationMethod, keepKeycloakLogin),
		renderKeycloakMigrationText(emailText, username, googleEmail, password, googleCreationMethod, keepKeycloakLogin),
	} {
		if _, appErr := p.API.CreatePost(&model.Post{
			UserId:    p.pluginBot.UserId,
			ChannelId: dm.Id,
			RootId:    root.Id,
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
			"mattermostUsername":      {mattermostUsername},
			"creationMethod":          {"MattermostPluginCommand"},
			"isActuallyInternational": {"No"}, // international students should create their own accounts
			"confirmsIsInternational": {"Yes"},
			"kthProgram":              {"None"},
			"kthExpectedEndOfStudies": {"2099-12"},
			"accountType":             {"Unknown (created by Mattermost plugin)"},
			"accountMigrated":         {"False"},
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

const keycloakNewUserUsage = "Usage: `/keycloak new-user <email> <temp-password> <first-name> <last-name> <mattermost-username> <international: yes/no>`"

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
	return p.getKeycloakUserByQueryWithUniqueness(parameter, value, exact, false)
}

func (p *Plugin) getKeycloakUserByQueryWithUniqueness(parameter, value string, exact, requireUnique bool) (*keycloakUserRepresentation, error) {
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
	if requireUnique && len(users) != 1 {
		return nil, fmt.Errorf("multiple Keycloak accounts match %s %q", parameter, value)
	}
	return &users[0], nil
}

func googleUsername(primaryEmail string) string {
	username := strings.SplitN(primaryEmail, "@", 2)[0]
	return username
}

func (p *Plugin) updateKeycloakMigrationProfile(user *keycloakUserRepresentation, migrated bool, fromID, mattermostID, mattermostEmail string, allowProfileUpdates, allowMattermostLogin bool, firstName, lastName, email string, usernameMigration ...string) error {
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
	return p.writeKeycloakMigrationProfile(user)
}

func (p *Plugin) writeKeycloakMigrationProfile(user *keycloakUserRepresentation) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
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
	return p.writeKeycloakMattermostLogin(user, []string{keycloakBooleanString(allow)}, true)
}

func (p *Plugin) writeKeycloakMattermostLogin(user *keycloakUserRepresentation, values []string, present bool) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if user.Attributes == nil {
		user.Attributes = map[string][]string{}
	}
	if present {
		user.Attributes["allowMattermostLogin"] = values
	} else {
		delete(user.Attributes, "allowMattermostLogin")
	}

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

func (p *Plugin) updateKeycloakAccountBan(user *keycloakUserRepresentation, ban bool, reason string) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if user.Attributes == nil {
		user.Attributes = map[string][]string{}
	}
	user.Attributes["accountBanned"] = []string{keycloakBooleanString(ban)}
	if ban {
		user.Attributes["banReason"] = []string{reason}
	} else {
		delete(user.Attributes, "banReason")
	}

	body, err := json.Marshal(user)
	if err != nil {
		return fmt.Errorf("encode Keycloak account ban update: %w", err)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(user.ID))
	request, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create Keycloak account ban update: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("update Keycloak account ban attributes: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Keycloak account ban update returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}

func (p *Plugin) resetKeycloakPassword(user *keycloakUserRepresentation, password string) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	body, err := json.Marshal(keycloakCredential{Type: "password", Value: password, Temporary: true})
	if err != nil {
		return fmt.Errorf("encode temporary Keycloak password: %w", err)
	}
	endpoint := fmt.Sprintf("%s/admin/realms/%s/users/%s/reset-password", keycloakURL, url.PathEscape(keycloakRealm), url.PathEscape(user.ID))
	request, err := http.NewRequest(http.MethodPut, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create temporary Keycloak password reset request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("reset Keycloak password: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Keycloak password reset returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(details)))
	}
	return nil
}
