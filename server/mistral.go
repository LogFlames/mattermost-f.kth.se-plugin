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

	mistralMaxRetries      = 5
	mistralRetryInterval   = 5 * time.Second
	mistralDiagnosticLimit = 16 * 1024
)

type MistralClient struct {
	apiKey          string
	model           string
	reasoningEffort string
	httpClient      *http.Client
	apiURL          string
	logError        func(string, ...any)
}

type mistralDiagnosticBody struct {
	bytes.Buffer
	limit int
}

func (b *mistralDiagnosticBody) Write(data []byte) (int, error) {
	n := len(data)
	_, _ = b.Buffer.Write(data[:min(n, b.limit-b.Len())])
	return n, nil
}

type mistralRequest struct {
	Model           string           `json:"model"`
	Messages        []mistralMessage `json:"messages"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

type mistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func NewMistralClient(apiKey, model string) *MistralClient {
	effort := ""
	if model == "mistral-large-4" || model == "mistral-large-4-0" {
		effort = "low"
	}
	return &MistralClient{
		apiKey:          apiKey,
		model:           model,
		reasoningEffort: effort,
		httpClient:      &http.Client{Timeout: 120 * time.Second},
		apiURL:          mistralAPIURL,
	}
}

func (c *MistralClient) Complete(systemPrompt, userPrompt string) (string, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return "", fmt.Errorf("Mistral API key is not configured")
	}
	if strings.TrimSpace(c.model) == "" {
		return "", fmt.Errorf("Mistral model is not configured")
	}

	payload := mistralRequest{
		Model: c.model,
		Messages: []mistralMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ReasoningEffort: c.reasoningEffort,
	}
	requestBody, err := json.Marshal(payload)
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

		captured := &mistralDiagnosticBody{limit: mistralDiagnosticLimit + len(c.apiKey) + 1}
		reader := io.TeeReader(response.Body, captured)
		var result mistralResponse
		decodeErr := json.NewDecoder(reader).Decode(&result)
		if decodeErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			_, _ = io.Copy(io.Discard, io.LimitReader(reader, int64(captured.limit-captured.Len())))
		}
		response.Body.Close()
		failure := func(reason string) error {
			redact := func(value string) string { return strings.ReplaceAll(value, c.apiKey, "[REDACTED]") }
			contentType := redact(response.Header.Get("Content-Type"))
			requestID := redact(response.Header.Get("X-Request-Id"))
			contentType = contentType[:min(len(contentType), 256)]
			requestID = requestID[:min(len(requestID), 256)]
			if c.logError != nil {
				body := redact(captured.String())
				truncated := captured.Len() == captured.limit || len(body) > mistralDiagnosticLimit
				body = body[:min(len(body), mistralDiagnosticLimit)]
				c.logError("Mistral response failed", "status", response.StatusCode, "content_type", contentType,
					"request_id", requestID, "reason", reason, "response_body", body, "truncated", truncated)
			}
			return fmt.Errorf("%s (HTTP %d, content-type %q, request-id %q)", reason, response.StatusCode, contentType, requestID)
		}

		if response.StatusCode == http.StatusTooManyRequests {
			lastErr = failure("Mistral rate limited the request")
			// Re-create the request body reader for the next attempt.
			request.Body = io.NopCloser(bytes.NewReader(requestBody))
			continue
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return "", failure("Mistral rejected the request")
		}
		if decodeErr != nil {
			return "", failure("Could not decode Mistral response")
		}
		if len(result.Choices) == 0 || len(result.Choices[0].Message.Content) == 0 {
			return "", failure("Mistral returned no completion")
		}
		var content string
		if err := json.Unmarshal(result.Choices[0].Message.Content, &content); err != nil {
			var chunks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(result.Choices[0].Message.Content, &chunks); err != nil {
				return "", failure("Could not decode Mistral content")
			}
			var text strings.Builder
			for _, chunk := range chunks {
				if chunk.Type == "text" {
					text.WriteString(chunk.Text)
				}
			}
			content = text.String()
		}
		if strings.TrimSpace(content) == "" {
			return "", failure("Mistral returned no completion")
		}
		return content, nil
	}

	return "", fmt.Errorf("Mistral rate limited after %d retries: %w", mistralMaxRetries, lastErr)
}
