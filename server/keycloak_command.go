package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
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
	case "migrate_to_google":
		return p.migrateKeycloakUserToGoogle(args, fields)
	case "migrate_to_keycloak":
		return p.migrateGoogleUserToKeycloak(args, fields)
	case "new_user":
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
		response.Text = fmt.Sprintf("Keycloak account created for %s with temporary password %s. Keycloak will require the password to be changed upon first log-in.", fields[2], fields[3])
		return response, nil
	default:
		response.Text = keycloakNewUserUsage
		return response, nil
	}
}

var keycloakMigrateUsage map[string]string = map[string]string{
	"migrate_to_google":                 "Usage: `/keycloak migrate_to_google list-org-units|create-kth|create-external|bind`",
	"migrate_to_google list-org-units":  "Usage: `/keycloak migrate_to_google list-org-units`",
	"migrate_to_google create-kth":      "Usage: `/keycloak migrate_to_google create-kth ~<mattermost-user-to-migrate> <kth-id> <org-unit-#>`",
	"migrate_to_google create-external": "Usage: `/keycloak migrate_to_google create-external ~<mattermost-user-to-migrate> <fysiksektionen.se-address> [org-unit]`",
	"migrate_to_google bind":            "Usage: `/keycloak migrate_to_google bind ~<mattermost-user-to-migrate> <fysiksektionen.se-address>`",
	"migrate_to_keycloak":               "Usage: `/keycloak migrate_to_keycloak <create|bind|update>`",
	"migrate_to_keycloak create":        "Usage: `/keycloak migrate_to_keycloak create ~<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate_to_keycloak bind":          "Usage: `/keycloak migrate_to_keycloak bind ~<mattermost-user-to-migrate> <keycloak-email>`",
	"migrate_to_keycloak update":        "Usage: `/keycloak migrate_to_keycloak update ~<mattermost-user-to-migrate> <keycloak-email>`",
}

func (p *Plugin) migrateKeycloakUserToGoogle(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 3 {
		response.Text = keycloakMigrateUsage["migrate_to_google"]
		return response, nil
	}
	if fields[2] == "list-org-units" {
		if len(fields) != 3 {
			response.Text = keycloakMigrateUsage["migrate_to_google list-org-units"]
			return response, nil
		}
		configuration := p.getConfiguration()
		units, err := p.listGoogleOrgUnits(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		response.Text = strings.Join(units, "\n")
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

	var googleUser *googleDirectoryUser
	var password string
	if len(fields) < 4 {
		response.Text = keycloakMigrateUsage["migrate_to_google "+fields[2]]
		return response, nil
	}
	switch fields[2] {
	case "create-kth":
		if len(fields) != 5 {
			response.Text = keycloakMigrateUsage["migrate_to_google create-kth"]
			return response, nil
		}
		configuration := p.getConfiguration()
		orgUnit, resolveErr := p.resolveGoogleOrgUnit(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fields[4])
		if resolveErr != nil {
			response.Text = resolveErr.Error()
			return response, nil
		}
		googleUser, err = p.createGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fmt.Sprintf("%s@fysiksektionen.se", fields[3]), keycloakUser.FirstName, keycloakUser.LastName, orgUnit, "")
	case "create-external":
		if len(fields) != 4 && len(fields) != 5 {
			response.Text = keycloakMigrateUsage["migrate_to_google create-external"]
			return response, nil
		}
		orgUnit := p.getConfiguration().KeycloakMigrationGoogleExternalOrgUnit
		if len(fields) == 5 {
			orgUnit = fields[4]
		}
		password = randomPassword(16)
		configuration := p.getConfiguration()
		orgUnit, err = p.resolveGoogleOrgUnit(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, orgUnit)
		if err != nil {
			response.Text = err.Error()
			return response, nil
		}
		googleUser, err = p.createGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fields[3], keycloakUser.FirstName, keycloakUser.LastName, orgUnit, password)
	case "bind":
		if len(fields) != 4 {
			response.Text = keycloakMigrateUsage["migrate_to_google bind"]
			return response, nil
		}
		configuration := p.getConfiguration()
		googleUser, err = p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, fields[3])
		if err != nil {
			err = fmt.Errorf("Google account %q does not exist; use create-kth or create-external first", fields[3])
		}
	default:
		response.Text = keycloakMigrateUsage["migrate_to_google"]
		return response, nil
	}
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateMattermostIdentity(user, model.ServiceOpenid, googleUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateKeycloakMigration(keycloakUser, true, googleUser.PrimaryEmail, googleUser.ID, user.Email, keycloakUser.ID, user.Id, user.Email); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	response.Text = fmt.Sprintf("Migrated ~%s to Google account %s. Their Mattermost account will be updated on first log-in.", user.Username, googleUser.PrimaryEmail)
	if password != "" {
		response.Text += fmt.Sprintf("\n\n Temporary password: `%s`", password)
	}
	if err := p.notifyGoogleMigration(user, googleUser.PrimaryEmail, password); err != nil {
		p.API.LogError("Google migration notification failed", "user_id", user.Id, "error", err.Error())
	}
	return response, nil
}

func (p *Plugin) migrateGoogleUserToKeycloak(args *model.CommandArgs, fields []string) (*model.CommandResponse, *model.AppError) {
	response := &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral}
	if len(fields) < 4 {
		response.Text = keycloakMigrateUsage["migrate_to_keycloak"]
		return response, nil
	}
	user, err := p.migrationMattermostUser(fields[2])
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	configuration := p.getConfiguration()
	googleUser, err := p.getGoogleUser(keycloakMigrationGoogleServiceAccountKey, configuration.KeycloakMigrationGoogleAdminEmail, user.Email)
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	keycloakEmail := fields[3]
	var keycloakUser *keycloakUserRepresentation
	switch fields[2] {
	case "create":
		if len(fields) != 4 {
			response.Text = keycloakMigrateUsage["migrate_to_keycloak create"]
			return response, nil
		}
		if err = p.createKeycloakUser(keycloakEmail, randomPassword(16), googleUser.Name.GivenName, googleUser.Name.FamilyName, user.Username); err == nil {
			keycloakUser, err = p.getKeycloakUser(keycloakEmail)
		}
	case "bind", "update":
		if len(fields) != 4 {
			response.Text = keycloakMigrateUsage["migrate_to_keycloak "+fields[2]]
			return response, nil
		}
		keycloakUser, err = p.getKeycloakUser(keycloakEmail)
		if err != nil && fields[2] == "bind" {
			err = fmt.Errorf("Keycloak account %q does not exist; use /keycloak migrate_to_keycloak create %s %s", keycloakEmail, fields[2], keycloakEmail)
		}
	default:
		response.Text = keycloakMigrateUsage["migrate_to_keycloak"]
		return response, nil
	}
	if err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if user.AuthData == nil || *user.AuthData == "" {
		response.Text = "Mattermost user has no Google Auth Data; cannot migrate to Keycloak."
		return response, nil
	}
	if err = p.updateMattermostIdentity(user, model.UserAuthServiceSaml, keycloakUser.ID); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	if err = p.updateKeycloakMigration(keycloakUser, true, keycloakUser.Username, keycloakUser.ID, googleUser.PrimaryEmail, googleUser.ID, *user.AuthData, googleUser.PrimaryEmail); err != nil {
		response.Text = err.Error()
		return response, nil
	}
	response.Text = fmt.Sprintf("Migrated %s to Keycloak account %s.", user.Username, keycloakUser.Username)
	return response, nil
}

func (p *Plugin) notifyGoogleMigration(user *model.User, email, temporaryPassword string) error {
	configuration := p.getConfiguration()
	if configuration.KeycloakMigrationGoogleSendEphemeral {
		message := fmt.Sprintf("Your Mattermost account has been migrated to Google Workspace. Sign in with `%s`.", email)
		if temporaryPassword != "" {
			message += fmt.Sprintf(" Your temporary password is `%s`; Google will require you to change it at first login.", temporaryPassword)
		}
		if post := p.API.SendEphemeralPost(user.Id, &model.Post{UserId: user.Id, Message: message}); post == nil {
			return fmt.Errorf("Mattermost did not create the migration notification")
		}
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

func (p *Plugin) updateKeycloakMigration(user *keycloakUserRepresentation, migrated bool, toEmail, toID, fromEmail, fromID, mattermostID, mattermostEmail string) error {
	configuration := p.getConfiguration()
	token, err := p.getKeycloakAccessToken(configuration.KeycloakClientID, configuration.KeycloakClientSecret)
	if err != nil {
		return err
	}
	if user.Attributes == nil {
		user.Attributes = map[string][]string{}
	}
	accountMigrated := "false"
	if migrated {
		accountMigrated = "true"
	}
	user.Attributes["accountMigrated"] = []string{accountMigrated}
	user.Attributes["migratedToEmail"] = []string{toEmail}
	user.Attributes["migratedFromEmail"] = []string{fromEmail}
	user.Attributes["migratedToID"] = []string{toID}
	user.Attributes["migratedFromID"] = []string{fromID}
	user.Attributes["mattermostID"] = []string{mattermostID}
	user.Attributes["mattermostEmail"] = []string{mattermostEmail}
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
