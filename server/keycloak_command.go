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
		response.Text = "Keycloak account migration is not implemented yet."
		return response, nil
	case "migrate_to_keycloak":
		response.Text = "Keycloak account migration is not implemented yet."
		return response, nil
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
