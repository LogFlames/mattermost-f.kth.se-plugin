package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const googleDirectoryURL = "https://admin.googleapis.com/admin/directory/v1"

type googleAPIError struct {
	status int
	body   string
}

func (e *googleAPIError) Error() string {
	return fmt.Sprintf("Google Directory API returned HTTP %d: %s", e.status, e.body)
}

type googleServiceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

type googleDirectoryUser struct {
	ID           string `json:"id"`
	PrimaryEmail string `json:"primaryEmail"`
	Name         struct {
		GivenName  string `json:"givenName"`
		FamilyName string `json:"familyName"`
	} `json:"name"`
}

type googleOrgUnit struct {
	Name string `json:"name"`
	Path string `json:"orgUnitPath"`
}

func (p *Plugin) googleAccessToken() (string, error) {
	var account googleServiceAccount
	if err := json.Unmarshal([]byte(p.getConfiguration().GoogleServiceAccountKey), &account); err != nil {
		return "", fmt.Errorf("decode Google service account key: %w", err)
	}
	if account.ClientEmail == "" || account.PrivateKey == "" {
		return "", fmt.Errorf("Google service account key is missing client_email or private_key")
	}
	if account.TokenURI == "" {
		account.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("decode Google service account private key: invalid PEM")
	}
	privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if pkcs1Key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes); pkcs1Err == nil {
			privateKey = pkcs1Key
		} else {
			return "", fmt.Errorf("parse Google service account private key: %w", err)
		}
	}
	rsaKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("Google service account private key is not RSA")
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]interface{}{
		"iss": account.ClientEmail, "scope": "https://www.googleapis.com/auth/admin.directory.user https://www.googleapis.com/auth/admin.directory.orgunit",
		"aud": account.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if adminEmail := p.getConfiguration().GoogleAdminEmail; adminEmail != "" {
		var delegatedClaims map[string]interface{}
		_ = json.Unmarshal(claims, &delegatedClaims)
		delegatedClaims["sub"] = adminEmail
		claims, _ = json.Marshal(delegatedClaims)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign Google service account token: %w", err)
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)}}
	request, err := http.NewRequest(http.MethodPost, account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Google token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("request Google access token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("Google token endpoint returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("Google token response did not contain an access token")
	}
	return token.AccessToken, nil
}

func (p *Plugin) googleRequest(method, endpoint string, payload interface{}, result interface{}) error {
	token, err := p.googleAccessToken()
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return fmt.Errorf("encode Google request: %w", encodeErr)
		}
		body = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Google request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("call Google Directory API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		details, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return &googleAPIError{status: response.StatusCode, body: strings.TrimSpace(string(details))}
	}
	if result != nil && response.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			return fmt.Errorf("decode Google Directory response: %w", err)
		}
	}
	return nil
}

func (p *Plugin) getGoogleUser(email string) (*googleDirectoryUser, error) {
	var user googleDirectoryUser
	err := p.googleRequest(http.MethodGet, googleDirectoryURL+"/users/"+url.PathEscape(email), nil, &user)
	if err != nil {
		return nil, fmt.Errorf("get Google account %q: %w", email, err)
	}
	return &user, nil
}

func (p *Plugin) createGoogleUser(email, firstName, lastName, orgUnit, password string) (*googleDirectoryUser, error) {
	if err := validateKeycloakEmail(email); err != nil {
		return nil, err
	}
	if _, err := p.getGoogleUser(email); err == nil {
		return nil, fmt.Errorf("Google account conflict: %q already exists", email)
	} else {
		var apiErr *googleAPIError
		if !errors.As(err, &apiErr) || apiErr.status != http.StatusNotFound {
			return nil, fmt.Errorf("check Google account conflict: %w", err)
		}
	}
	payload := map[string]interface{}{
		"primaryEmail": email, "name": map[string]string{"givenName": firstName, "familyName": lastName},
		"changePasswordAtNextLogin": password != "",
	}
	if password != "" {
		payload["password"] = password
	}
	if orgUnit != "" {
		payload["orgUnitPath"] = orgUnit
	}
	var user googleDirectoryUser
	if err := p.googleRequest(http.MethodPost, googleDirectoryURL+"/users", payload, &user); err != nil {
		return nil, fmt.Errorf("create Google account: %w", err)
	}
	return &user, nil
}

func (p *Plugin) listGoogleOrgUnits() ([]string, error) {
	var response struct {
		OrganizationUnits []googleOrgUnit `json:"organizationUnits"`
	}
	if err := p.googleRequest(http.MethodGet, googleDirectoryURL+"/customer/my_customer/orgunits?type=all", nil, &response); err != nil {
		return nil, fmt.Errorf("list Google org units: %w", err)
	}
	units := make([]string, 0, len(response.OrganizationUnits))
	for _, unit := range response.OrganizationUnits {
		units = append(units, unit.Path)
	}
	return units, nil
}

func randomPassword(length int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*"
	password := make([]byte, length)
	random := make([]byte, length)
	_, _ = rand.Read(random)
	for i := range password {
		password[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(password)
}

func (p *Plugin) migrationMattermostUser(reference string) (*model.User, error) {
	username := strings.TrimPrefix(reference, "~")
	user, appErr := p.API.GetUserByUsername(username)
	if appErr != nil {
		return nil, fmt.Errorf("get Mattermost user %q: %s", username, appErr.Error())
	}
	if user == nil {
		return nil, fmt.Errorf("Mattermost user %q was not found", username)
	}
	return user, nil
}

func (p *Plugin) updateMattermostIdentity(user *model.User, authService, authData string) error {
	user.AuthService = authService
	user.AuthData = model.NewPointer(authData)
	if _, appErr := p.API.UpdateUser(user); appErr != nil {
		return fmt.Errorf("update Mattermost authentication data: %s", appErr.Error())
	}
	return nil
}
