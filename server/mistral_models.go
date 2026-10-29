package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

type mistralModelSettings struct {
	ID              string `json:"id"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func (s *mistralModelSettings) UnmarshalJSON(data []byte) error {
	var id string
	if json.Unmarshal(data, &id) == nil {
		*s = mistralModelSettings{ID: id}
		return nil
	}
	type settings mistralModelSettings
	return json.Unmarshal(data, (*settings)(s))
}

type mistralModel struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	MaxContextLength int      `json:"max_context_length"`
	Aliases          []string `json:"aliases"`
	Capabilities     struct {
		CompletionChat bool `json:"completion_chat"`
		Reasoning      bool `json:"reasoning"`
		Vision         bool `json:"vision"`
	} `json:"capabilities"`
	ReasoningEfforts []string        `json:"reasoning_efforts"`
	Pricing          json.RawMessage `json:"pricing,omitempty"`
	Cost             json.RawMessage `json:"cost,omitempty"`
}

func mistralReasoningEfforts(name string) []string {
	switch name {
	case "mistral-large-4", "mistral-large-4-0":
		return []string{"low", "medium", "high"}
	case "mistral-small-2603", "mistral-medium-latest", "mistral-medium-3-5", "mistral-medium-3.5", "mistral-medium-2604":
		return []string{"none", "high"}
	case "zai-glm-5-3":
		return []string{"low", "high", "max"}
	default:
		return []string{}
	}
}

func (c *MistralClient) Models(ctx context.Context) ([]mistralModel, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("Save the Mistral API key before loading models.")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.apiURL, "/chat/completions")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("Could not create the Mistral models request.")
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Could not reach Mistral to load models.")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Mistral model lookup failed (HTTP %d). Check the saved API key and try again.", response.StatusCode)
	}
	var result struct {
		Data []mistralModel `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("Could not read Mistral's model list.")
	}
	models := []mistralModel{}
	for _, m := range result.Data {
		if !m.Capabilities.CompletionChat {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		m.ReasoningEfforts = mistralReasoningEfforts(name)
		models = append(models, m)
	}
	slices.SortFunc(models, func(a, b mistralModel) int { return strings.Compare(a.ID, b.ID) })
	return models, nil
}

func (p *Plugin) serveMistralModels(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
	}
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		fail(http.StatusUnauthorized, "Sign in to view Mistral models.")
		return
	}
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		fail(http.StatusForbidden, "Only system admins can view Mistral models.")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	models, err := NewMistralClient(p.getConfiguration().TranslatorBot_APIKey, "").Models(r.Context())
	if err != nil {
		fail(http.StatusBadGateway, err.Error())
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
}
