package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const mistralTestModel = "ministral-14b-2512"

func TestMistralClientComplete(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		reasoningEffort string
		content         string
		want            string
	}{
		{
			name:    "string content",
			model:   mistralTestModel,
			content: `"Translated"`,
			want:    "Translated",
		},
		{
			name:            "large 4 structured content",
			model:           "mistral-large-4",
			reasoningEffort: "low",
			content:         `[{"type":"thinking","thinking":[{"type":"text","text":"Do not publish reasoning"}]},{"type":"text","text":"## Translation\n\n"},{"type":"text","text":"A café [link](https://example.com)."}]`,
			want:            "## Translation\n\nA café [link](https://example.com).",
		},
		{
			name:            "large 4 alias",
			model:           "mistral-large-4-0",
			reasoningEffort: "low",
			content:         `"Translated"`,
			want:            "Translated",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", request.Method)
				}
				if request.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
				}

				var body struct {
					Model           string           `json:"model"`
					Messages        []mistralMessage `json:"messages"`
					ReasoningEffort *string          `json:"reasoning_effort"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if body.Model != test.model {
					t.Errorf("model = %q, want %q", body.Model, test.model)
				}
				if test.reasoningEffort == "" {
					if body.ReasoningEffort != nil {
						t.Errorf("unexpected reasoning_effort: %q", *body.ReasoningEffort)
					}
				} else if body.ReasoningEffort == nil || *body.ReasoningEffort != test.reasoningEffort {
					t.Errorf("reasoning_effort = %v, want %q", body.ReasoningEffort, test.reasoningEffort)
				}
				if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[1].Content != "Translate this" {
					t.Errorf("messages = %#v", body.Messages)
				}

				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":` + test.content + `}}]}`))
			}))
			defer server.Close()

			client := NewMistralClient("test-key", test.model)
			client.apiURL = server.URL

			result, err := client.Complete("Instructions", "Translate this")
			if err != nil {
				t.Fatalf("Complete returned error: %v", err)
			}
			if result != test.want {
				t.Errorf("result = %q, want %q", result, test.want)
			}
		})
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
		{
			name:       "reasoning without an answer",
			apiKey:     "test-key",
			statusCode: http.StatusOK,
			response:   `{"choices":[{"message":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"Not a translation"}]}]}}]}`,
			wantError:  "Mistral returned no completion",
		},
		{
			name:       "whitespace text chunks",
			apiKey:     "test-key",
			statusCode: http.StatusOK,
			response:   `{"choices":[{"message":{"content":[{"type":"text","text":" \n"},{"type":"text","text":"\t"}]}}]}`,
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

func TestMistralModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected models request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"mistral-small-latest","name":"mistral-small-2603","max_context_length":262144,"capabilities":{"completion_chat":true,"reasoning":true}},
			{"id":"mistral-large-4-0","name":"mistral-large-4","max_context_length":1048576,"capabilities":{"completion_chat":true,"reasoning":true},"pricing":{"input_per_million":2}},
			{"id":"embedding","capabilities":{"completion_chat":false}},
			{"id":"future-model","capabilities":{"completion_chat":true,"reasoning":true}}
		]}`))
	}))
	defer server.Close()
	client := NewMistralClient("test-key", "")
	client.apiURL = server.URL + "/chat/completions"
	models, err := client.Models(context.Background())
	if err != nil || len(models) != 3 {
		t.Fatalf("models = %+v, error = %v", models, err)
	}
	if models[0].ID != "future-model" || len(models[0].ReasoningEfforts) != 0 ||
		models[1].MaxContextLength != 1048576 || !reflect.DeepEqual(models[1].ReasoningEfforts, []string{"low", "medium", "high"}) ||
		!reflect.DeepEqual(models[2].ReasoningEfforts, []string{"none", "high"}) || string(models[1].Pricing) != `{"input_per_million":2}` {
		t.Fatalf("incorrect model metadata: %+v", models)
	}
	for _, status := range []int{http.StatusUnauthorized, http.StatusOK} {
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("invalid provider response containing test-key"))
		}))
		client.apiURL = provider.URL + "/chat/completions"
		_, err := client.Models(context.Background())
		provider.Close()
		if err == nil || strings.Contains(err.Error(), "test-key") || strings.Contains(err.Error(), "provider response") {
			t.Fatalf("provider error was not safely reported: %v", err)
		}
	}
}

func TestMistralModelConfigurationCompatibility(t *testing.T) {
	for _, test := range []struct {
		value string
		want  mistralModelSettings
	}{
		{`"ministral-14b-2512"`, mistralModelSettings{ID: "ministral-14b-2512"}},
		{`{"id":"mistral-large-4","reasoning_effort":"medium"}`, mistralModelSettings{ID: "mistral-large-4", ReasoningEffort: "medium"}},
	} {
		var config configuration
		if err := json.Unmarshal([]byte(`{"translatorbot_mistralmodel":`+test.value+`}`), &config); err != nil {
			t.Fatal(err)
		}
		if config.TranslatorBot_MistralModel != test.want {
			t.Fatalf("model settings = %+v, want %+v", config.TranslatorBot_MistralModel, test.want)
		}
	}
}

type mistralTestTransport func(*http.Request) (*http.Response, error)

func (f mistralTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestTranslatorDictionary(t *testing.T) {
	var config configuration
	if err := json.Unmarshal([]byte(`{
		"translatorbot_apikey":"test-key",
		"translatorbot_mistralmodel":"ministral-14b-2512",
		"translatorbot_systemprompt":"Use this dictionary:",
		"translatorbot_userprompt":"Translate: {message}",
		"translatorbot_dictionary":"  mottagning = reception\nsektionslokal = chapter room\n"
	}`), &config); err != nil {
		t.Fatal(err)
	}
	wantSystem := "Use this dictionary:\n  mottagning = reception\nsektionslokal = chapter room\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body mistralRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := []mistralMessage{
			{Role: "system", Content: wantSystem},
			{Role: "user", Content: "Translate: Hej mottagningen!"},
		}
		if !reflect.DeepEqual(body.Messages, want) {
			t.Errorf("messages = %#v, want %#v", body.Messages, want)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello reception!"}}]}`))
	}))
	defer server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = mistralTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != mistralAPIURL {
			t.Errorf("unexpected request URL: %s", r.URL)
		}
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return originalTransport.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	p := &Plugin{}
	for _, test := range []struct{ dictionary, systemPrompt string }{
		{config.TranslatorBot_Dictionary, "Use this dictionary:\n  mottagning = reception\nsektionslokal = chapter room\n"},
		{"mottagning = welcome week", "Use this dictionary:\nmottagning = welcome week"},
	} {
		config.TranslatorBot_Dictionary = test.dictionary
		wantSystem = test.systemPrompt
		got, err := p.translate("Hej mottagningen!", &config, "")
		if err != nil || got != "Hello reception!" {
			t.Fatalf("translation = %q, error = %v", got, err)
		}
	}
	for _, dictionary := range []string{"", " \n\t"} {
		config.TranslatorBot_Dictionary = dictionary
		if _, err := p.translate("Hej mottagningen!", &config, ""); err == nil || err.Error() != "Translator dictionary is not configured" {
			t.Fatalf("empty dictionary error = %v", err)
		}
	}
}
