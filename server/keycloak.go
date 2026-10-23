package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const keycloakRealm = "mattermost"

var keycloakURL = "https://mattermost.fysiksektionen.se:9001"

func (p *Plugin) getKeycloakAccessToken(clientID, clientSecret string) (string, error) {
	endpoint := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", keycloakURL, url.PathEscape(keycloakRealm))
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("create Keycloak token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("request Keycloak access token: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Keycloak token endpoint returned HTTP %d", response.StatusCode)
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tokenResponse); err != nil {
		return "", fmt.Errorf("decode Keycloak token response: %w", err)
	}
	if tokenResponse.AccessToken == "" {
		return "", fmt.Errorf("Keycloak token response did not contain an access token")
	}
	return tokenResponse.AccessToken, nil
}

func keycloakID() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		return "", fmt.Errorf("generate Keycloak account ID: %w", err)
	}
	return fmt.Sprintf("keycloak%08d", value.Int64()), nil
}
