// Package loop implements the mux's agentic tool-calling loop.
//
// The loop drives a multi-step conversation with an LLM. On each turn the model
// may return tool calls; these are executed (in parallel when there are several)
// and their results fed back so the model can decide on the next action.
//
// The LLM interaction is abstracted behind the ChatClient interface so the loop
// is testable without a real OpenAI key. A concrete adapter wrapping
// github.com/openai/openai-go lives in the parent package and is wired in main.
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/mux/config"
)

// ---------------------------------------------------------------------------
// LLM abstraction
// ---------------------------------------------------------------------------

// ChatRequest is what the loop sends to the LLM on each iteration.
type ChatRequest struct {
	Model        string
	Temperature  float64
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDef
}

// ChatResponse is what the LLM returns.
type ChatResponse struct {
	Text         string     // final text (empty when there are tool calls)
	ToolCalls    []ToolCall // tool invocations requested by the model
	InputTokens  int        // tokens consumed by the prompt
	OutputTokens int        // tokens produced by the model
}

// ChatClient abstracts the LLM API so the loop can be tested with a mock.
// The production implementation wraps openai-go's client.Chat.Completions.New
// or client.Responses.New, depending on the SDK version.
type ChatClient interface {
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// ToolDef represents an OpenAI function tool definition.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolExecutor executes a tool call and returns the result string.
type ToolExecutor func(ctx context.Context, args map[string]any) (string, error)

// Message represents a conversation message in the rolling window.
type Message struct {
	Role       string     `json:"role"`        // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // assistant messages with tool calls
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool result messages
	Timestamp  time.Time  `json:"timestamp"`
}

// ToolCall represents a single tool invocation requested by the model.
type ToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// toolResult pairs a tool call ID with its execution result.
type toolResult struct {
	ToolCallID string
	Content    string
	Err        error
}

// ---------------------------------------------------------------------------
// Mux — the agentic loop orchestrator
// ---------------------------------------------------------------------------

// DefaultMaxIter is the default maximum number of loop iterations per turn.
const DefaultMaxIter = 20

// DefaultToolTimeout is the per-tool-call execution timeout.
const DefaultToolTimeout = 60 * time.Second

// Mux is the agentic loop orchestrator. It drives a multi-step conversation
// with the LLM, executing tool calls between iterations until the model
// produces a final text response.
type Mux struct {
	client        ChatClient
	cfg           *config.Config
	tools         []ToolDef
	toolExecutors map[string]ToolExecutor
	messages      []Message
	systemPrompt  string
	maxIter       int
	toolTimeout   time.Duration

	// Observability hooks (optional).
	OnToolCall  func(name string, args map[string]any, duration time.Duration)
	OnModelCall func(inputTokens, outputTokens int, costUSD float64)
}

// New creates a new Mux with the given config and LLM client.
func New(cfg *config.Config, client ChatClient) *Mux {
	return &Mux{
		client:        client,
		cfg:           cfg,
		tools:         nil,
		toolExecutors: make(map[string]ToolExecutor),
		messages:      nil,
		maxIter:       DefaultMaxIter,
		toolTimeout:   DefaultToolTimeout,
	}
}

// RegisterTool adds a tool to the mux. The executor is called when the model
// invokes the tool by name.
func (m *Mux) RegisterTool(def ToolDef, executor ToolExecutor) {
	m.tools = append(m.tools, def)
	m.toolExecutors[def.Name] = executor
}

// SetSystemPrompt sets the system prompt used for subsequent Run calls.
// Typically called before each run with the dynamically assembled context
// (persona + agent roster + facts + compaction summaries).
func (m *Mux) SetSystemPrompt(prompt string) {
	m.systemPrompt = prompt
}

// SetMaxIter overrides the default max iterations per turn.
func (m *Mux) SetMaxIter(n int) {
	if n > 0 {
		m.maxIter = n
	}
}

// SetToolTimeout overrides the default per-tool execution timeout.
func (m *Mux) SetToolTimeout(d time.Duration) {
	if d > 0 {
		m.toolTimeout = d
	}
}

// Messages returns the current conversation history (for persistence).
func (m *Mux) Messages() []Message {
	out := make([]Message, len(m.messages))
	copy(out, m.messages)
	return out
}

// LoadMessages restores conversation history (e.g. on restart from memory store).
func (m *Mux) LoadMessages(msgs []Message) {
	m.messages = make([]Message, len(msgs))
	copy(m.messages, msgs)
}

// ---------------------------------------------------------------------------
// Run — the core agentic loop
// ---------------------------------------------------------------------------

// Run processes a user message through the agentic loop, returning the final
// text response from the model. The loop iterates up to maxIter times; each
// iteration calls the LLM, checks for tool calls, executes them in parallel,
// appends results, and loops until the model produces a text-only response.
func (m *Mux) Run(ctx context.Context, userMessage string) (string, error) {
	// 1. Append the user message.
	m.messages = append(m.messages, Message{
		Role:      "user",
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// 2. Agentic loop.
	for i := 0; i < m.maxIter; i++ {
		// Build request.
		req := ChatRequest{
			Model:        m.cfg.OpenAI.Model,
			Temperature:  m.cfg.OpenAI.Temperature,
			SystemPrompt: m.systemPrompt,
			Messages:     m.messages,
			Tools:        m.tools,
		}

		// Call LLM.
		resp, err := m.client.Complete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("loop: llm call failed (iter %d): %w", i, err)
		}

		// Fire model-call hook.
		if m.OnModelCall != nil {
			cost := estimateCost(m.cfg.OpenAI.Model, resp.InputTokens, resp.OutputTokens)
			m.OnModelCall(resp.InputTokens, resp.OutputTokens, cost)
		}

		// No tool calls → final text response.
		if len(resp.ToolCalls) == 0 {
			text := resp.Text
			m.messages = append(m.messages, Message{
				Role:      "assistant",
				Content:   text,
				Timestamp: time.Now(),
			})
			return text, nil
		}

		// Append assistant message with tool calls (content may be empty).
		m.messages = append(m.messages, Message{
			Role:      "assistant",
			Content:   resp.Text,
			ToolCalls: resp.ToolCalls,
			Timestamp: time.Now(),
		})

		// Execute tool calls in parallel.
		results := m.executeToolCalls(ctx, resp.ToolCalls)

		// Append tool results.
		for _, r := range results {
			content := r.Content
			if r.Err != nil {
				content = fmt.Sprintf("error: %v", r.Err)
			}
			m.messages = append(m.messages, Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: r.ToolCallID,
				Timestamp:  time.Now(),
			})
		}

		// Continue loop — model sees results and decides next action.
	}

	return "", fmt.Errorf("loop: max iterations (%d) exceeded", m.maxIter)
}

// executeToolCalls runs all tool calls in parallel and returns their results
// in the same order as the input.
func (m *Mux) executeToolCalls(ctx context.Context, calls []ToolCall) []toolResult {
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup

	for i, tc := range calls {
		wg.Add(1)
		go func(idx int, call ToolCall) {
			defer wg.Done()

			executor, ok := m.toolExecutors[call.Name]
			if !ok {
				results[idx] = toolResult{
					ToolCallID: call.ID,
					Err:        fmt.Errorf("unknown tool: %s", call.Name),
				}
				return
			}

			// Per-tool timeout.
			toolCtx, cancel := context.WithTimeout(ctx, m.toolTimeout)
			defer cancel()

			start := time.Now()
			content, err := executor(toolCtx, call.Args)
			duration := time.Since(start)

			results[idx] = toolResult{
				ToolCallID: call.ID,
				Content:    content,
				Err:        err,
			}

			// Fire tool-call hook.
			if m.OnToolCall != nil {
				m.OnToolCall(call.Name, call.Args, duration)
			}
		}(i, tc)
	}

	wg.Wait()
	return results
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// estimateCost returns a rough USD cost estimate based on model and token counts.
// Prices are approximate and should be kept up to date.
func estimateCost(model string, inputTokens, outputTokens int) float64 {
	// Per-million-token pricing (approximate, as of 2025).
	type pricing struct {
		input  float64
		output float64
	}
	prices := map[string]pricing{
		"gpt-4o":      {2.50, 10.00},
		"gpt-4o-mini": {0.15, 0.60},
		"gpt-4-turbo": {10.00, 30.00},
		"gpt-4":       {30.00, 60.00},
		"gpt-3.5-turbo": {0.50, 1.50},
	}

	p, ok := prices[model]
	if !ok {
		// Default to gpt-4o pricing as a reasonable fallback.
		p = prices["gpt-4o"]
	}

	return (float64(inputTokens)*p.input + float64(outputTokens)*p.output) / 1_000_000
}

// MarshalToolCalls serializes tool calls to JSON (for persistence).
func MarshalToolCalls(calls []ToolCall) (string, error) {
	data, err := json.Marshal(calls)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// UnmarshalToolCalls deserializes tool calls from JSON (for loading from DB).
func UnmarshalToolCalls(data string) ([]ToolCall, error) {
	if data == "" {
		return nil, nil
	}
	var calls []ToolCall
	if err := json.Unmarshal([]byte(data), &calls); err != nil {
		return nil, err
	}
	return calls, nil
}
