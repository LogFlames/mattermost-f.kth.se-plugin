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
	"regexp"
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

var (
	googleOrgUnitFPattern          = regexp.MustCompile(`(?i)^/?Teknologer[^/]*/Teknologer i F-(?:[øo0])(?:[^/]*)$`)
	googleOrgUnitXXPattern         = regexp.MustCompile(`(?i)^/?Teknologer[^/]*/Teknologer i F-([0-9]+)(?:[^/]*)$`)
	googleOrgUnitNumericKeyPattern = regexp.MustCompile(`(?i)^f([0-9]+)$`)
)

const (
	keycloakMigrationGoogleServiceAccountKey = "KeycloakMigrationGoogleServiceAccountKey"
	googleGroupsSyncServiceAccountKey        = "GoogleGroupsSyncServiceAccountKey"
)

func (p *Plugin) googleServiceAccountKey(keyName string) (string, error) {
	configuration := p.getConfiguration()
	switch keyName {
	case keycloakMigrationGoogleServiceAccountKey:
		return configuration.KeycloakMigrationGoogleServiceAccountKey, nil
	case googleGroupsSyncServiceAccountKey:
		return configuration.GoogleGroupsSyncServiceAccountKey, nil
	default:
		return "", fmt.Errorf("unknown Google service account setting %q", keyName)
	}
}

func (p *Plugin) googleAccessToken(serviceAccountKey, adminEmail string) (string, error) {
	var account googleServiceAccount
	if err := json.Unmarshal([]byte(serviceAccountKey), &account); err != nil {
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
		"iss": account.ClientEmail, "scope": "https://www.googleapis.com/auth/admin.directory.user.readonly https://www.googleapis.com/auth/admin.directory.user https://www.googleapis.com/auth/admin.directory.orgunit.readonly",
		"aud": account.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if adminEmail != "" {
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

func (p *Plugin) googleRequest(serviceAccountKeyName, adminEmail, method, endpoint string, payload interface{}, result interface{}) error {
	serviceAccountKey, err := p.googleServiceAccountKey(serviceAccountKeyName)
	if err != nil {
		return err
	}
	token, err := p.googleAccessToken(serviceAccountKey, adminEmail)
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

func (p *Plugin) getGoogleUser(serviceAccountKeyName, adminEmail, email string) (*googleDirectoryUser, error) {
	var user googleDirectoryUser
	err := p.googleRequest(serviceAccountKeyName, adminEmail, http.MethodGet, googleDirectoryURL+"/users/"+url.PathEscape(email), nil, &user)
	if err != nil {
		return nil, fmt.Errorf("get Google account %q: %w", email, err)
	}
	return &user, nil
}

func (p *Plugin) createGoogleUser(serviceAccountKeyName, adminEmail, email, firstName, lastName, orgUnit, password string, alternativeEmails ...string) (*googleDirectoryUser, error) {
	if err := validateFysiksektionenEmail(email); err != nil {
		return nil, err
	}
	if _, err := p.getGoogleUser(serviceAccountKeyName, adminEmail, email); err == nil {
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
	if alternativeEmail := alternativeGoogleEmail(email, alternativeEmails...); alternativeEmail != "" {
		payload["emails"] = []map[string]interface{}{{
			"address": alternativeEmail,
			"type":    "other",
		}}
	}
	var user googleDirectoryUser
	if err := p.googleRequest(serviceAccountKeyName, adminEmail, http.MethodPost, googleDirectoryURL+"/users", payload, &user); err != nil {
		return nil, fmt.Errorf("create Google account: %w", err)
	}
	return &user, nil
}

func alternativeGoogleEmail(primaryEmail string, alternativeEmails ...string) string {
	for _, email := range alternativeEmails {
		email = strings.TrimSpace(email)
		if email != "" && !strings.EqualFold(email, primaryEmail) {
			return email
		}
	}
	return ""
}

func (p *Plugin) updateGoogleUserAlternativeEmail(serviceAccountKeyName, adminEmail, userID, alternativeEmail string) error {
	alternativeEmail = strings.TrimSpace(alternativeEmail)
	if alternativeEmail == "" {
		return nil
	}
	payload := map[string]interface{}{
		"emails": []map[string]interface{}{{
			"address": alternativeEmail,
			"type":    "other",
		}},
	}
	if err := p.googleRequest(serviceAccountKeyName, adminEmail, http.MethodPatch, googleDirectoryURL+"/users/"+url.PathEscape(userID), payload, nil); err != nil {
		return fmt.Errorf("update Google account alternative email: %w", err)
	}
	return nil
}

func (p *Plugin) listGoogleOrgUnits(serviceAccountKeyName, adminEmail string) ([]string, error) {
	var units []string
	pageToken := ""
	for {
		query := url.Values{"type": {"all"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			OrganizationUnits []googleOrgUnit `json:"organizationUnits"`
			NextPageToken     string          `json:"nextPageToken"`
		}
		endpoint := googleDirectoryURL + "/customer/my_customer/orgunits?" + query.Encode()
		if err := p.googleRequest(serviceAccountKeyName, adminEmail, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, fmt.Errorf("list Google org units: %w", err)
		}
		for _, unit := range response.OrganizationUnits {
			units = append(units, unit.Path)
		}
		if response.NextPageToken == "" {
			break
		}
		pageToken = response.NextPageToken
	}
	return units, nil
}

func (p *Plugin) resolveGoogleOrgUnit(serviceAccountKeyName, adminEmail, requested string) (string, error) {
	units, err := p.listGoogleOrgUnits(serviceAccountKeyName, adminEmail)
	if err != nil {
		return "", err
	}
	return resolveGoogleOrgUnitKey(units, "kth", requested)
}

func resolveGoogleOrgUnitKey(units []string, category, requested string) (string, error) {
	if requested == "" {
		return "", nil
	}

	if strings.EqualFold(category, "external") {
		externalKeys := map[string]string{
			"alumner":  "Alumner",
			"external": "Icke-teknologer",
			"utility":  "Utility-konton",
		}
		leaf, ok := externalKeys[strings.ToLower(requested)]
		if !ok {
			return "", fmt.Errorf("Google organization unit key %q was not found", requested)
		}
		var match string
		for _, unit := range units {
			if strings.EqualFold(unitLeaf(unit), leaf) {
				if match != "" {
					return "", fmt.Errorf("Google organization unit key %q is ambiguous", requested)
				}
				match = unit
			}
		}
		if match == "" {
			return "", fmt.Errorf("Google organization unit key %q was not found", requested)
		}
		return match, nil
	}

	key := strings.ToLower(strings.ReplaceAll(requested, "-", ""))
	var keyAlias string
	switch key {
	case "fo", "f0", "fø":
		keyAlias = "f"
	}
	if match := googleOrgUnitNumericKeyPattern.FindStringSubmatch(key); len(match) == 2 {
		if match[1] != "0" {
			keyAlias = "f" + match[1]
		}
	}
	if keyAlias == "" {
		return "", fmt.Errorf("Google organization unit key %q was not found", requested)
	}

	var match string
	for _, unit := range units {
		path := strings.Trim(unit, "/")
		if keyAlias == "f" {
			if googleOrgUnitFPattern.MatchString(path) {
				if match != "" {
					return "", fmt.Errorf("Google organization unit key %q is ambiguous", requested)
				}
				match = unit
			}
			continue
		}
		matchAlias := googleOrgUnitXXPattern.FindStringSubmatch(path)
		if len(matchAlias) == 2 && strings.EqualFold("f"+matchAlias[1], keyAlias) {
			if match != "" {
				return "", fmt.Errorf("Google organization unit key %q is ambiguous", requested)
			}
			match = unit
		}
	}
	if match == "" {
		return "", fmt.Errorf("Google organization unit key %q was not found", requested)
	}
	return match, nil
}

func unitLeaf(unit string) string {
	unit = strings.Trim(unit, "/")
	if index := strings.LastIndex(unit, "/"); index >= 0 {
		return unit[index+1:]
	}
	return unit
}

func googleOrgUnitKeys(units []string) []string {
	keys := make([]string, 0)
	external := []struct {
		key  string
		leaf string
	}{
		{"alumner", "Alumner"},
		{"external", "Icke-teknologer"},
		{"utility", "Utility-konton"},
	}
	for _, candidate := range external {
		for _, unit := range units {
			if strings.EqualFold(unitLeaf(unit), candidate.leaf) {
				keys = append(keys, candidate.key)
				break
			}
		}
	}
	if _, err := resolveGoogleOrgUnitKey(units, "kth", "f-ø"); err == nil {
		keys = append(keys, "f-ø")
	}
	seen := make(map[string]bool)
	for _, unit := range units {
		match := googleOrgUnitXXPattern.FindStringSubmatch(strings.Trim(unit, "/"))
		if len(match) == 2 {
			key := "f-" + match[1]
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
	}
	return keys
}

func randomPassword(length int) string {
	if length < 4 {
		length = 4
	}

	const (
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower   = "abcdefghijkmnopqrstuvwxyz"
		digits  = "23456789"
		symbols = "!@#$%^&*"
	)

	groups := []string{upper, lower, digits, symbols}
	alphabet := upper + lower + digits + symbols
	password := make([]byte, length)
	random := make([]byte, length)
	_, _ = rand.Read(random)

	for i := range password {
		password[i] = alphabet[int(random[i])%len(alphabet)]
	}
	for i, group := range groups {
		password[i] = group[int(random[i])%len(group)]
	}
	return string(password)
}

func (p *Plugin) migrationMattermostUser(reference string) (*model.User, error) {
	if !strings.HasPrefix(reference, "@") {
		return nil, fmt.Errorf("Mattermost user reference %q must start with @", reference)
	}
	username := strings.TrimPrefix(reference, "@")
	if username == "" {
		return nil, fmt.Errorf("Mattermost user reference %q has no username", reference)
	}
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
