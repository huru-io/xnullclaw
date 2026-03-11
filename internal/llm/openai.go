// Package llm provides the OpenAI chat completions adapter for the mux loop.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/loop"
)

// OpenAIAdapter implements loop.ChatClient using the OpenAI chat completions API.
type OpenAIAdapter struct {
	apiKey      string
	model       string
	temperature float64
	httpClient  *http.Client
	baseURL     string
}

// NewOpenAIAdapter creates a new adapter from config.
func NewOpenAIAdapter(cfg *config.Config) *OpenAIAdapter {
	return &OpenAIAdapter{
		apiKey:      cfg.OpenAI.APIKey,
		model:       cfg.OpenAI.Model,
		temperature: cfg.OpenAI.Temperature,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
		baseURL: "https://api.openai.com/v1",
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

	body := oaiRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
	}
	if len(oaiTools) > 0 {
		body.Tools = oaiTools
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/chat/completions", bytes.NewReader(bodyJSON))
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return loop.ChatResponse{}, fmt.Errorf("openai: API error %d: %s", resp.StatusCode, string(respBody))
	}

	var oaiResp oaiResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return loop.ChatResponse{}, fmt.Errorf("openai: parse response: %w", err)
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

	return result, nil
}

// ---------------------------------------------------------------------------
// OpenAI API types
// ---------------------------------------------------------------------------

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Temperature float64      `json:"temperature"`
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
