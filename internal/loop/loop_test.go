package loop

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
)

// ---------------------------------------------------------------------------
// Mock LLM client
// ---------------------------------------------------------------------------

// mockClient is a ChatClient that returns pre-configured responses in sequence.
// Each call to Complete pops the next response from the queue.
type mockClient struct {
	responses []ChatResponse
	calls     []ChatRequest // recorded requests
	callIdx   int
}

func (m *mockClient) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	m.calls = append(m.calls, req)
	if m.callIdx >= len(m.responses) {
		return ChatResponse{}, fmt.Errorf("mock: no more responses (call %d)", m.callIdx)
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.OpenAI.Model = "gpt-4o-mini"
	cfg.OpenAI.Temperature = 0.5
	return cfg
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestSimpleTextResponse(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{Text: "Hello! How can I help?", InputTokens: 50, OutputTokens: 10},
		},
	}
	m := New(testConfig(), mc)
	m.SetSystemPrompt("You are a helpful assistant.")

	resp, err := m.Run(context.Background(), "hi there")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Hello! How can I help?" {
		t.Errorf("got %q, want %q", resp, "Hello! How can I help?")
	}

	// Verify message history: user + assistant.
	msgs := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hi there" {
		t.Errorf("user message mismatch: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Hello! How can I help?" {
		t.Errorf("assistant message mismatch: %+v", msgs[1])
	}

	// Verify the request included the system prompt.
	if len(mc.calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(mc.calls))
	}
	if mc.calls[0].SystemPrompt != "You are a helpful assistant." {
		t.Errorf("system prompt not passed: %q", mc.calls[0].SystemPrompt)
	}
}

func TestSingleToolCall(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			// First response: model requests a tool call.
			{
				ToolCalls: []ToolCall{
					{ID: "call_1", Name: "get_time", Args: map[string]any{}},
				},
				InputTokens: 100, OutputTokens: 20,
			},
			// Second response: model produces text after seeing tool result.
			{
				Text:         "The current time is 12:00 PM.",
				InputTokens:  150,
				OutputTokens: 15,
			},
		},
	}

	m := New(testConfig(), mc)

	// Register the tool.
	m.RegisterTool(
		ToolDef{
			Name:        "get_time",
			Description: "Get the current time",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "12:00 PM UTC", nil
		},
	)

	resp, err := m.Run(context.Background(), "what time is it?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "The current time is 12:00 PM." {
		t.Errorf("got %q, want %q", resp, "The current time is 12:00 PM.")
	}

	// Verify messages: user, assistant(tool_calls), tool(result), assistant(text).
	msgs := m.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Errorf("expected assistant with tool calls: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Errorf("expected tool result message: %+v", msgs[2])
	}
	if msgs[2].Content != "12:00 PM UTC" {
		t.Errorf("tool result content: got %q, want %q", msgs[2].Content, "12:00 PM UTC")
	}
	if msgs[3].Role != "assistant" || msgs[3].Content != "The current time is 12:00 PM." {
		t.Errorf("final assistant message: %+v", msgs[3])
	}

	// Verify two LLM calls were made.
	if len(mc.calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(mc.calls))
	}
}

func TestMultipleToolCallsInParallel(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			// Model requests two tool calls at once.
			{
				ToolCalls: []ToolCall{
					{ID: "call_a", Name: "weather", Args: map[string]any{"city": "NYC"}},
					{ID: "call_b", Name: "weather", Args: map[string]any{"city": "LA"}},
				},
				InputTokens: 100, OutputTokens: 30,
			},
			// Model produces final text.
			{
				Text:         "NYC is 70F, LA is 80F.",
				InputTokens:  200,
				OutputTokens: 20,
			},
		},
	}

	m := New(testConfig(), mc)

	// Track parallel execution via atomic counter.
	var concurrent int64
	var maxConcurrent int64

	m.RegisterTool(
		ToolDef{
			Name:        "weather",
			Description: "Get weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			cur := atomic.AddInt64(&concurrent, 1)
			// Update max if this is the highest we've seen.
			for {
				old := atomic.LoadInt64(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
					break
				}
			}
			// Small sleep to ensure parallel execution window overlaps.
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&concurrent, -1)

			city, _ := args["city"].(string)
			switch city {
			case "NYC":
				return "70F", nil
			case "LA":
				return "80F", nil
			default:
				return "unknown", nil
			}
		},
	)

	resp, err := m.Run(context.Background(), "weather in NYC and LA?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "NYC is 70F, LA is 80F." {
		t.Errorf("got %q", resp)
	}

	// Verify both tools ran in parallel (max concurrent >= 2).
	if atomic.LoadInt64(&maxConcurrent) < 2 {
		t.Errorf("expected parallel execution, max concurrent was %d", maxConcurrent)
	}

	// Verify messages: user + assistant(2 tool calls) + 2 tool results + assistant(text).
	msgs := m.Messages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	// Tool results should preserve order.
	if msgs[2].ToolCallID != "call_a" || msgs[2].Content != "70F" {
		t.Errorf("first tool result: %+v", msgs[2])
	}
	if msgs[3].ToolCallID != "call_b" || msgs[3].Content != "80F" {
		t.Errorf("second tool result: %+v", msgs[3])
	}
}

func TestMaxIterationsExceeded(t *testing.T) {
	// Model always returns tool calls, never produces text.
	infinite := make([]ChatResponse, 25)
	for i := range infinite {
		infinite[i] = ChatResponse{
			ToolCalls: []ToolCall{
				{ID: fmt.Sprintf("call_%d", i), Name: "noop", Args: map[string]any{}},
			},
			InputTokens: 10, OutputTokens: 5,
		}
	}

	mc := &mockClient{responses: infinite}
	m := New(testConfig(), mc)
	m.SetMaxIter(3) // low limit for test speed

	m.RegisterTool(
		ToolDef{Name: "noop", Description: "does nothing"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	)

	_, err := m.Run(context.Background(), "loop forever")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "max iterations (3) exceeded") {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have made exactly 3 LLM calls.
	if len(mc.calls) != 3 {
		t.Errorf("expected 3 LLM calls, got %d", len(mc.calls))
	}
}

func TestToolExecutionError(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "call_err", Name: "flaky", Args: map[string]any{}},
				},
				InputTokens: 50, OutputTokens: 10,
			},
			// Model handles the error and responds.
			{
				Text:         "Sorry, that tool failed.",
				InputTokens:  100,
				OutputTokens: 10,
			},
		},
	}

	m := New(testConfig(), mc)
	m.RegisterTool(
		ToolDef{Name: "flaky", Description: "a tool that fails"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "", fmt.Errorf("connection timeout")
		},
	)

	resp, err := m.Run(context.Background(), "try the flaky tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Sorry, that tool failed." {
		t.Errorf("got %q", resp)
	}

	// The tool result message should contain the error.
	msgs := m.Messages()
	toolMsg := msgs[2] // user, assistant(tool_calls), tool(result)
	if toolMsg.Role != "tool" {
		t.Fatalf("expected tool message, got %s", toolMsg.Role)
	}
	if !strings.Contains(toolMsg.Content, "connection timeout") {
		t.Errorf("tool error not propagated: %q", toolMsg.Content)
	}
}

func TestUnknownToolError(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "call_unk", Name: "nonexistent", Args: map[string]any{}},
				},
				InputTokens: 50, OutputTokens: 10,
			},
			{
				Text:         "I tried to use a tool that doesn't exist.",
				InputTokens:  100,
				OutputTokens: 10,
			},
		},
	}

	m := New(testConfig(), mc)
	// No tools registered.

	resp, err := m.Run(context.Background(), "use nonexistent tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "I tried to use a tool that doesn't exist." {
		t.Errorf("got %q", resp)
	}

	msgs := m.Messages()
	toolMsg := msgs[2]
	if !strings.Contains(toolMsg.Content, "unknown tool: nonexistent") {
		t.Errorf("unknown tool error not reported: %q", toolMsg.Content)
	}
}

func TestObservabilityHooks(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "call_1", Name: "ping", Args: map[string]any{}},
				},
				InputTokens: 100, OutputTokens: 20,
			},
			{
				Text:         "pong",
				InputTokens:  150,
				OutputTokens: 10,
			},
		},
	}

	m := New(testConfig(), mc)
	m.RegisterTool(
		ToolDef{Name: "ping", Description: "ping"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "pong", nil
		},
	)

	var toolCalls []string
	var modelCalls int
	var totalCost float64

	m.OnToolCall = func(name string, args map[string]any, result string, duration time.Duration, err error) {
		toolCalls = append(toolCalls, name)
	}
	m.OnModelCall = func(inputTokens, outputTokens int, costUSD float64) {
		modelCalls++
		totalCost += costUSD
	}

	_, err := m.Run(context.Background(), "ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(toolCalls) != 1 || toolCalls[0] != "ping" {
		t.Errorf("tool call hook: got %v", toolCalls)
	}
	if modelCalls != 2 {
		t.Errorf("model call hook: expected 2, got %d", modelCalls)
	}
	if totalCost <= 0 {
		t.Errorf("cost should be positive, got %f", totalCost)
	}
}

func TestLoadAndRestoreMessages(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{Text: "I remember!", InputTokens: 50, OutputTokens: 5},
		},
	}

	m := New(testConfig(), mc)

	// Simulate loading conversation history from persistence.
	history := []Message{
		{Role: "user", Content: "my name is Alice", Timestamp: time.Now().Add(-time.Hour)},
		{Role: "assistant", Content: "Nice to meet you, Alice!", Timestamp: time.Now().Add(-time.Hour)},
	}
	m.LoadMessages(history)

	resp, err := m.Run(context.Background(), "do you remember my name?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "I remember!" {
		t.Errorf("got %q", resp)
	}

	// Verify the LLM received the full history.
	if len(mc.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mc.calls))
	}
	reqMsgs := mc.calls[0].Messages
	if len(reqMsgs) != 3 { // 2 loaded + 1 new user message
		t.Errorf("expected 3 messages in request, got %d", len(reqMsgs))
	}

	// Verify Messages() returns a copy.
	msgs := m.Messages()
	if len(msgs) != 4 { // 2 loaded + user + assistant
		t.Errorf("expected 4 messages total, got %d", len(msgs))
	}
	msgs[0].Content = "tampered"
	if m.Messages()[0].Content == "tampered" {
		t.Error("Messages() should return a copy, not a reference")
	}
}

func TestContextCancellation(t *testing.T) {
	mc := &mockClient{
		responses: []ChatResponse{
			{
				ToolCalls: []ToolCall{
					{ID: "call_slow", Name: "slow", Args: map[string]any{}},
				},
				InputTokens: 50, OutputTokens: 10,
			},
		},
	}

	m := New(testConfig(), mc)
	m.RegisterTool(
		ToolDef{Name: "slow", Description: "a slow tool"},
		func(ctx context.Context, args map[string]any) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
				return "done", nil
			}
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel right after the first LLM call returns (tool will see cancelled ctx).
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := m.Run(ctx, "run slow tool")
	// The second LLM call should fail because context is cancelled.
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestEstimateCost(t *testing.T) {
	// gpt-4o: $2.50/M input, $10.00/M output
	cost := estimateCost("gpt-4o", 1000, 500)
	expected := (1000*2.50 + 500*10.00) / 1_000_000
	if cost != expected {
		t.Errorf("cost: got %f, want %f", cost, expected)
	}

	// Unknown model falls back to gpt-4o pricing.
	costUnknown := estimateCost("some-future-model", 1000, 500)
	if costUnknown != expected {
		t.Errorf("unknown model cost: got %f, want %f", costUnknown, expected)
	}
}

func TestMarshalUnmarshalToolCalls(t *testing.T) {
	calls := []ToolCall{
		{ID: "a", Name: "foo", Args: map[string]any{"x": float64(1)}},
		{ID: "b", Name: "bar", Args: map[string]any{"y": "hello"}},
	}

	data, err := MarshalToolCalls(calls)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	restored, err := UnmarshalToolCalls(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(restored))
	}
	if restored[0].Name != "foo" || restored[1].Name != "bar" {
		t.Errorf("names mismatch: %v", restored)
	}

	// Empty string returns nil.
	empty, err := UnmarshalToolCalls("")
	if err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if empty != nil {
		t.Errorf("expected nil for empty string, got %v", empty)
	}
}

func TestMultiStepToolChain(t *testing.T) {
	// Simulate a multi-step scenario: model calls tool A, sees result,
	// calls tool B, sees result, then produces text.
	mc := &mockClient{
		responses: []ChatResponse{
			// Step 1: call setup_agent.
			{
				ToolCalls: []ToolCall{
					{ID: "call_1", Name: "setup_agent", Args: map[string]any{"name": "alice"}},
				},
				InputTokens: 100, OutputTokens: 15,
			},
			// Step 2: call start_agent.
			{
				ToolCalls: []ToolCall{
					{ID: "call_2", Name: "start_agent", Args: map[string]any{"name": "alice"}},
				},
				InputTokens: 200, OutputTokens: 15,
			},
			// Step 3: final response.
			{
				Text:         "Agent alice is set up and running!",
				InputTokens:  300,
				OutputTokens: 10,
			},
		},
	}

	m := New(testConfig(), mc)
	m.RegisterTool(
		ToolDef{Name: "setup_agent", Description: "create agent"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "agent alice created", nil
		},
	)
	m.RegisterTool(
		ToolDef{Name: "start_agent", Description: "start agent"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "agent alice started", nil
		},
	)

	resp, err := m.Run(context.Background(), "set up and start alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "Agent alice is set up and running!" {
		t.Errorf("got %q", resp)
	}

	// 3 LLM calls total.
	if len(mc.calls) != 3 {
		t.Errorf("expected 3 LLM calls, got %d", len(mc.calls))
	}

	// Messages: user + (assistant+tool) + (assistant+tool) + assistant
	// = 1 + 2 + 2 + 1 = 6
	msgs := m.Messages()
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(msgs))
	}
}
