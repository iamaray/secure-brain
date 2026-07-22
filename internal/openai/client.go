package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"secure-brain/internal/domain"
)

const (
	maxProviderResponseBytes = 1 << 20
	maxProviderErrorBytes    = 8 << 10

	InstructionsTemplate = `You are the SecureBrain demo assistant for the Brain named {{DISPLAY_NAME}}
({{CANONICAL_ID}}). Answer helpfully and concisely. This is a simulated
Brain-aware experience. You have not been given, cannot inspect, and must not claim
knowledge of files uploaded to this Brain. If asked what is in those files, state
that this v0 chat is not grounded in uploaded content. Do not imply that you can
modify Brains, files, routes, permissions, Services, or transfers.`
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Model           string
	Instructions    string
	Input           []Message
	MaxOutputTokens int
}

type ChatClient interface {
	Respond(ctx context.Context, request Request) (string, error)
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("OpenAI base URL must be an absolute http(s) URL")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, httpClient: httpClient}, nil
}

func BrainInstructions(displayName, canonicalID string) string {
	return strings.NewReplacer(
		"{{DISPLAY_NAME}}", displayName,
		"{{CANONICAL_ID}}", canonicalID,
	).Replace(InstructionsTemplate)
}

func (c *Client) Respond(ctx context.Context, request Request) (string, error) {
	if strings.TrimSpace(request.Model) == "" || request.MaxOutputTokens <= 0 {
		return "", &domain.Error{Code: domain.CodeInvalidRequest, Message: "A model and positive output-token limit are required."}
	}
	payload := responsesRequest{
		Model:           request.Model,
		Store:           false,
		Instructions:    request.Instructions,
		Input:           request.Input,
		MaxOutputTokens: request.MaxOutputTokens,
		Text: responsesText{
			Verbosity: "low",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", chatProviderError("encode request", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(body))
		if reqErr != nil {
			return "", chatProviderError("create request", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			if attempt == 0 && waitForRetry(ctx) {
				continue
			}
			return "", chatProviderError("send request", doErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			discardBounded(resp.Body, maxProviderErrorBytes)
			resp.Body.Close()
			return "", chatProviderError("provider response", fmt.Errorf("HTTP status %d", resp.StatusCode))
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProviderResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return "", chatProviderError("read response", readErr)
		}
		if len(responseBody) > maxProviderResponseBytes {
			return "", chatProviderError("read response", fmt.Errorf("response exceeds configured bound"))
		}
		text, parseErr := parseOutputText(responseBody)
		if parseErr != nil {
			return "", chatProviderError("parse response", parseErr)
		}
		return text, nil
	}
	panic("unreachable")
}

type responsesRequest struct {
	Model           string        `json:"model"`
	Store           bool          `json:"store"`
	Instructions    string        `json:"instructions"`
	Input           []Message     `json:"input"`
	MaxOutputTokens int           `json:"max_output_tokens"`
	Text            responsesText `json:"text"`
}

type responsesText struct {
	Verbosity string `json:"verbosity"`
}

type responsesResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func parseOutputText(body []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var response responsesResponse
	if err := decoder.Decode(&response); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("response contains trailing JSON")
		}
		return "", err
	}
	var text strings.Builder
	for _, item := range response.Output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("response contained no assistant output text")
	}
	return text.String(), nil
}

func chatProviderError(operation string, cause error) *domain.Error {
	return &domain.Error{
		Code:    domain.CodeChatProviderError,
		Message: "Chat is temporarily unavailable.",
		Cause:   fmt.Errorf("%s: %w", operation, cause),
	}
}

func discardBounded(reader io.Reader, limit int64) {
	_, _ = io.Copy(io.Discard, io.LimitReader(reader, limit))
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
