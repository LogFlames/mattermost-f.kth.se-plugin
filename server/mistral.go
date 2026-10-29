package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	mistralAPIURL = "https://api.mistral.ai/v1/chat/completions"

	mistralMaxRetries    = 5
	mistralRetryInterval = 5 * time.Second
)

type MistralClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	apiURL     string
}

type mistralRequest struct {
	Model    string           `json:"model"`
	Messages []mistralMessage `json:"messages"`
}

type mistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralResponse struct {
	Choices []struct {
		Message mistralMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewMistralClient(apiKey, model string) *MistralClient {
	return &MistralClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiURL:     mistralAPIURL,
	}
}

func (c *MistralClient) Complete(systemPrompt, userPrompt string) (string, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return "", fmt.Errorf("Mistral API key is not configured")
	}
	if strings.TrimSpace(c.model) == "" {
		return "", fmt.Errorf("Mistral model is not configured")
	}

	requestBody, err := json.Marshal(mistralRequest{
		Model: c.model,
		Messages: []mistralMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal Mistral request: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, c.apiURL, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create Mistral request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")

	var lastErr error
	for attempt := 0; attempt <= mistralMaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(mistralRetryInterval)
		}

		response, err := c.httpClient.Do(request)
		if err != nil {
			return "", fmt.Errorf("call Mistral: %w", err)
		}

		var result mistralResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			response.Body.Close()
			return "", fmt.Errorf("decode Mistral response: %w", err)
		}
		response.Body.Close()

		if response.StatusCode == http.StatusTooManyRequests {
			if result.Error != nil {
				lastErr = fmt.Errorf("Mistral returned HTTP 429: %s", result.Error.Message)
			} else {
				lastErr = fmt.Errorf("Mistral returned HTTP 429")
			}
			// Re-create the request body reader for the next attempt.
			request.Body = io.NopCloser(bytes.NewReader(requestBody))
			continue
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			if result.Error != nil {
				return "", fmt.Errorf("Mistral returned HTTP %d: %s", response.StatusCode, result.Error.Message)
			}
			return "", fmt.Errorf("Mistral returned HTTP %d", response.StatusCode)
		}
		if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
			return "", fmt.Errorf("Mistral returned no completion")
		}
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("Mistral rate limited after %d retries: %w", mistralMaxRetries, lastErr)
}
