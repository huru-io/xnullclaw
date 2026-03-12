// Package llm provides the OpenAI chat completions adapter for the mux loop.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/loop"
)

// Retry defaults for transient server errors (5xx, 429).
const (
	defaultMaxRetries    = 3
	defaultRetryBaseDelay = 1 * time.Second
	defaultRetryMaxDelay  = 30 * time.Second
)

// serverError represents an HTTP error from the upstream API.
type serverError struct {
	StatusCode int
	Body       string
}

func (e *serverError) Error() string {
	return fmt.Sprintf("openai: API error %d: %s", e.StatusCode, e.Body)
}

// isRetryableStatus returns true for HTTP status codes that are likely transient.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,      // 429
		http.StatusInternalServerError,   // 500
		http.StatusBadGateway,            // 502
		http.StatusServiceUnavailable,    // 503
		http.StatusGatewayTimeout:        // 504
		return true
	}
	return false
}

// OpenAIAdapter implements loop.ChatClient using the OpenAI chat completions API.
type OpenAIAdapter struct {
	apiKey      string
	model       string
	temperature float64
	httpClient  *http.Client
	baseURL     string

	// Retry config for transient errors.
	maxRetries    int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration

	// mu protects unsupported.
	mu sync.Mutex
	// unsupported tracks parameters that a model has rejected,
	// so we skip them on subsequent calls without a round trip.
	// Key: model name, value: set of rejected parameter names.
	unsupported map[string]map[string]bool
}

// NewOpenAIAdapter creates a new adapter from config.
func NewOpenAIAdapter(cfg *config.Config) *OpenAIAdapter {
	base := cfg.OpenAI.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAIAdapter{
		apiKey:      cfg.OpenAI.APIKey,
		model:       cfg.OpenAI.Model,
		temperature: cfg.OpenAI.Temperature,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		baseURL:        base,
		maxRetries:     defaultMaxRetries,
		retryBaseDelay: defaultRetryBaseDelay,
		retryMaxDelay:  defaultRetryMaxDelay,
		unsupported:    make(map[string]map[string]bool),
	}
}

// Complete implements loop.ChatClient.
func (a *OpenAIAdapter) Complete(ctx context.Context, req loop.ChatRequest) (loop.ChatResponse, error) {
	var messages []oaiMessage

	if req.SystemPrompt != "" {
		messages = append(messages, oaiMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		})
	}

	for _, m := range req.Messages {
		msg := oaiMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Args)
				msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: oaiFunction{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
		}
		messages = append(messages, msg)
	}

	var oaiTools []oaiTool
	for _, t := range req.Tools {
		oaiTools = append(oaiTools, oaiTool{
			Type: "function",
			Function: oaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	body := a.buildRequest(req.Model, messages, oaiTools, req.Temperature)

	// Try up to 3 times, dropping unsupported parameters on each retry.
	const maxParamRetries = 3
	for attempt := 0; attempt < maxParamRetries; attempt++ {
		resp, retryParam, err := a.doRequestWithRetry(ctx, body)
		if err != nil {
			return loop.ChatResponse{}, err
		}
		if retryParam == "" {
			return resp, nil
		}
		// Model rejected a parameter — drop it and retry.
		a.markUnsupported(req.Model, retryParam)
		body = a.buildRequest(req.Model, messages, oaiTools, req.Temperature)
	}

	return loop.ChatResponse{}, fmt.Errorf("openai: too many unsupported-parameter retries for model %s", req.Model)
}

// buildRequest constructs an oaiRequest, skipping parameters that the model
// has previously rejected.
func (a *OpenAIAdapter) buildRequest(model string, messages []oaiMessage, tools []oaiTool, temperature float64) oaiRequest {
	a.mu.Lock()
	skip := a.unsupported[model]
	a.mu.Unlock()

	body := oaiRequest{
		Model:    model,
		Messages: messages,
	}
	if temperature > 0 && !skip["temperature"] {
		t := temperature
		body.Temperature = &t
	}
	if len(tools) > 0 && !skip["tools"] {
		body.Tools = tools
	}
	return body
}

// doRequest sends the HTTP request and parses the response.
// If the API returns a 400 for an unsupported parameter/value, it returns
// the parameter name in retryParam so the caller can retry without it.
// On success, retryParam is empty.
func (a *OpenAIAdapter) doRequest(ctx context.Context, body oaiRequest) (loop.ChatResponse, string, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return loop.ChatResponse{}, "", fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(bodyJSON))
	if err != nil {
		return loop.ChatResponse{}, "", fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return loop.ChatResponse{}, "", fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return loop.ChatResponse{}, "", fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode == http.StatusBadRequest {
		// Check if this is an unsupported parameter/value error we can retry.
		if param := parseUnsupportedParam(respBody); param != "" {
			return loop.ChatResponse{}, param, nil
		}
		return loop.ChatResponse{}, "", fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK {
		return loop.ChatResponse{}, "", &serverError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return loop.ChatResponse{}, "", fmt.Errorf("openai: parse response: %w", err)
	}

	result := loop.ChatResponse{
		InputTokens:  oaiResp.Usage.PromptTokens,
		OutputTokens: oaiResp.Usage.CompletionTokens,
	}

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		result.Text = choice.Message.Content

		for _, tc := range choice.Message.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					args = map[string]any{"_raw": tc.Function.Arguments}
				}
			}
			result.ToolCalls = append(result.ToolCalls, loop.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			})
		}
	}

	return result, "", nil
}

// doRequestWithRetry wraps doRequest with exponential backoff for transient errors.
func (a *OpenAIAdapter) doRequestWithRetry(ctx context.Context, body oaiRequest) (loop.ChatResponse, string, error) {
	var lastErr error
	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		if attempt > 0 {
			delay := a.backoffDelay(attempt)
			select {
			case <-ctx.Done():
				return loop.ChatResponse{}, "", ctx.Err()
			case <-time.After(delay):
			}
		}
		resp, retryParam, err := a.doRequest(ctx, body)
		if err == nil {
			return resp, retryParam, nil
		}
		// Retry only on transient server errors.
		var srvErr *serverError
		if errors.As(err, &srvErr) && isRetryableStatus(srvErr.StatusCode) {
			lastErr = err
			continue
		}
		// Non-retryable error — return immediately.
		return loop.ChatResponse{}, "", err
	}
	return loop.ChatResponse{}, "", fmt.Errorf("openai: max retries (%d) exceeded: %w", a.maxRetries, lastErr)
}

// backoffDelay returns the delay before the given retry attempt using
// exponential backoff with full jitter: uniform random in [0, min(base*2^(attempt-1), max)].
func (a *OpenAIAdapter) backoffDelay(attempt int) time.Duration {
	base := a.retryBaseDelay * time.Duration(1<<(attempt-1))
	if base > a.retryMaxDelay {
		base = a.retryMaxDelay
	}
	return time.Duration(rand.Int63n(int64(base) + 1))
}

// markUnsupported records that a model does not support a given parameter.
func (a *OpenAIAdapter) markUnsupported(model, param string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unsupported[model] == nil {
		a.unsupported[model] = make(map[string]bool)
	}
	a.unsupported[model][param] = true
}

// parseUnsupportedParam checks if an OpenAI error response indicates an
// unsupported parameter or unsupported value and returns the parameter name.
// Returns "" if the error is not of this type.
func parseUnsupportedParam(body []byte) string {
	var errResp struct {
		Error struct {
			Code  string `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		return ""
	}
	switch errResp.Error.Code {
	case "unsupported_parameter", "unsupported_value":
		return errResp.Error.Param
	}
	return ""
}

// ---------------------------------------------------------------------------
// OpenAI API types
// ---------------------------------------------------------------------------

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string          `json:"type"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiChoice struct {
	Message oaiMessage `json:"message"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
