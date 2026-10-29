package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const mistralTestModel = "ministral-14b-2512"

func TestMistralClientComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}

		var body mistralRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != mistralTestModel {
			t.Errorf("model = %q, want %q", body.Model, mistralTestModel)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Content != "Translate this" {
			t.Errorf("messages = %#v", body.Messages)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"Translated"}}]}`))
	}))
	defer server.Close()

	client := NewMistralClient("test-key", mistralTestModel)
	client.apiURL = server.URL

	result, err := client.Complete("Instructions", "Translate this")
	if err != nil {
		t.Fatalf("Complete returned error: %v", err)
	}
	if result != "Translated" {
		t.Errorf("result = %q, want Translated", result)
	}
}

func TestMistralClientCompleteErrors(t *testing.T) {
	tests := []struct {
		name       string
		apiKey     string
		statusCode int
		response   string
		wantError  string
	}{
		{
			name:      "missing API key",
			wantError: "Mistral API key is not configured",
		},
		{
			name:       "provider error",
			apiKey:     "test-key",
			statusCode: http.StatusBadRequest,
			response:   `{"error":{"message":"invalid request"}}`,
			wantError:  "Mistral returned HTTP 400: invalid request",
		},
		{
			name:       "empty completion",
			apiKey:     "test-key",
			statusCode: http.StatusOK,
			response:   `{"choices":[]}`,
			wantError:  "Mistral returned no completion",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewMistralClient(test.apiKey, mistralTestModel)
			if test.statusCode != 0 {
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					writer.WriteHeader(test.statusCode)
					_, _ = writer.Write([]byte(test.response))
				}))
				defer server.Close()
				client.apiURL = server.URL
			}

			_, err := client.Complete("", "")
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
		})
	}
}
