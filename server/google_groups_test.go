package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"strings"
	"testing"
)

type googleTestTransport func(*http.Request) (*http.Response, error)

func (transport googleTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func setupGoogleTestTransport(t *testing.T, handler func(*http.Request) (int, string), cloudHandlers ...func(*http.Request) (int, string)) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	account, err := json.Marshal(googleServiceAccount{ClientEmail: "service@example.com", PrivateKey: string(key), TokenURI: "https://oauth2.googleapis.com/token"})
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = googleTestTransport(func(request *http.Request) (*http.Response, error) {
		var status int
		var body string
		if request.URL.Host == "cloudidentity.googleapis.com" && len(cloudHandlers) > 0 {
			status, body = cloudHandlers[0](request)
		} else if request.URL.Host == "cloudidentity.googleapis.com" {
			switch request.URL.Path {
			case "/v1/groups:lookup":
				status, body = http.StatusOK, `{"name":"groups/id"}`
			case "/v1/groups/id":
				status, body = http.StatusOK, `{"name":"groups/id","groupKey":{"id":"team@example.org"},"displayName":"Team"}`
			case "/v1/groups/id/memberships":
				status, body = http.StatusOK, `{"memberships":[]}`
			default:
				t.Errorf("unexpected Cloud Identity request: %s", request.URL)
				status, body = http.StatusNotFound, `{}`
			}
		} else {
			status, body = handler(request)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return string(account)
}
