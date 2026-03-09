package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/loop"
	"github.com/jotavich/xnullclaw/mux/memory"
	"github.com/jotavich/xnullclaw/mux/prompt"
	"github.com/jotavich/xnullclaw/mux/tools"
)

// mockChatClient is a programmable ChatClient for integration tests.
type mockChatClient struct {
	responses []loop.ChatResponse
	calls     int
	lastReq   loop.ChatRequest
}

func (m *mockChatClient) Complete(ctx context.Context, req loop.ChatRequest) (loop.ChatResponse, error) {
	m.lastReq = req
	idx := m.calls
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.calls++
	return m.responses[idx], nil
}

// -----------------------------------------------------------------------
// Integration: Config → Store → Prompt → Loop (simple text response)
// -----------------------------------------------------------------------

func TestIntegration_SimpleTextResponse(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	// Seed some context.
	store.AddFact(memory.Fact{Type: "preference", Content: "User prefers concise answers"})
	store.AddMessage(memory.Message{Role: "user", Content: "hello", Stream: "conversation"})

	assembler := memory.NewAssembler(store)
	builder := prompt.New(cfg)

	// Assemble context for this turn.
	ctxData, err := assembler.Assemble("what agents do I have")
	if err != nil {
		t.Fatalf("assemble failed: %v", err)
	}

	systemPrompt := builder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)
	if systemPrompt == "" {
		t.Fatal("system prompt is empty")
	}
	if !strings.Contains(systemPrompt, "orchestrator") {
		t.Fatal("system prompt missing core role")
	}
	if !strings.Contains(systemPrompt, "mux") {
		t.Fatal("system prompt missing persona name")
	}

	// Setup mock LLM with simple text response.
	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{Text: "You have no agents running.", InputTokens: 100, OutputTokens: 20},
		},
	}

	muxLoop := loop.New(cfg, mock)
	muxLoop.SetSystemPrompt(systemPrompt)

	response, err := muxLoop.Run(context.Background(), "what agents do I have")
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}
	if response != "You have no agents running." {
		t.Fatalf("unexpected response: %q", response)
	}
	if mock.calls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", mock.calls)
	}

	// Verify the request had the system prompt and user message.
	if mock.lastReq.SystemPrompt != systemPrompt {
		t.Fatal("system prompt not passed to LLM")
	}
	if len(mock.lastReq.Messages) == 0 {
		t.Fatal("no messages passed to LLM")
	}
}

// -----------------------------------------------------------------------
// Integration: Loop with tool calls (remember → recall → text)
// -----------------------------------------------------------------------

func TestIntegration_LoopWithToolCalls(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg.Save(cfgPath)

	wrapperPath := filepath.Join(tmpDir, "xnullclaw")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)

	// Register tools.
	registry := tools.NewRegistry()
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)

	// Setup mock LLM: first response calls "remember", second returns text.
	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{
				ToolCalls: []loop.ToolCall{
					{
						ID:   "call_1",
						Name: "remember",
						Args: map[string]any{
							"fact":       "User likes Go",
							"importance": "preference",
						},
					},
				},
				InputTokens:  200,
				OutputTokens: 50,
			},
			{
				Text:         "Got it! I'll remember that you like Go.",
				InputTokens:  250,
				OutputTokens: 30,
			},
		},
	}

	muxLoop := loop.New(cfg, mock)

	// Register tools from registry into the loop.
	for _, def := range registry.Definitions() {
		toolName := def.Name
		muxLoop.RegisterTool(loop.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		}, func(ctx context.Context, args map[string]any) (string, error) {
			return registry.Execute(ctx, toolName, args)
		})
	}

	response, err := muxLoop.Run(context.Background(), "Remember that I like Go")
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}
	if !strings.Contains(response, "remember") {
		t.Logf("response: %s", response)
	}
	if mock.calls != 2 {
		t.Fatalf("expected 2 LLM calls (tool call + text), got %d", mock.calls)
	}

	// Verify the fact was actually stored.
	facts, err := store.SearchFacts("Go", "", 10)
	if err != nil {
		t.Fatalf("search facts failed: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("expected fact to be stored after remember tool call")
	}
	found := false
	for _, f := range facts {
		if strings.Contains(f.Content, "Go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("stored fact doesn't contain 'Go'")
	}
}

// -----------------------------------------------------------------------
// Integration: Persona change via tool → affects system prompt
// -----------------------------------------------------------------------

func TestIntegration_PersonaChangesPrompt(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg.Save(cfgPath)

	builder := prompt.New(cfg)

	// Initial prompt should reflect default persona.
	assembler := memory.NewAssembler(store)
	ctxData, _ := assembler.Assemble("")
	prompt1 := builder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)

	if !strings.Contains(prompt1, "mux") {
		t.Fatal("initial prompt should contain 'mux' as persona name")
	}

	// Simulate a persona tool call that changes the name.
	registry := tools.NewRegistry()
	wrapperPath := filepath.Join(tmpDir, "xnullclaw")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)

	_, err := registry.Execute(context.Background(), "set_persona", map[string]any{
		"field": "name",
		"value": "jarvis",
	})
	if err != nil {
		t.Fatalf("set_persona failed: %v", err)
	}

	// Re-build prompt — should now say jarvis.
	ctxData, _ = assembler.Assemble("")
	prompt2 := builder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)

	if !strings.Contains(prompt2, "jarvis") {
		t.Fatalf("prompt should contain 'jarvis' after persona change, got:\n%s", prompt2)
	}
}

// -----------------------------------------------------------------------
// Integration: Passthrough rules appear in system prompt
// -----------------------------------------------------------------------

func TestIntegration_PassthroughRulesInPrompt(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg.Save(cfgPath)

	wrapperPath := filepath.Join(tmpDir, "xnullclaw")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)

	registry := tools.NewRegistry()
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)

	// Add a passthrough rule.
	_, err := registry.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "global",
		"rule":  "Always correct Spanish grammar",
	})
	if err != nil {
		t.Fatalf("set_passthrough_rule failed: %v", err)
	}

	// Assemble context and build prompt.
	assembler := memory.NewAssembler(store)
	builder := prompt.New(cfg)
	ctxData, err := assembler.Assemble("hola")
	if err != nil {
		t.Fatalf("assemble failed: %v", err)
	}

	sysPrompt := builder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)

	if !strings.Contains(sysPrompt, "passthrough rules") {
		t.Fatalf("expected 'passthrough rules' in system prompt, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "Spanish grammar") {
		t.Fatalf("expected rule content in system prompt, got:\n%s", sysPrompt)
	}
}

// -----------------------------------------------------------------------
// Integration: Agent state shows in prompt
// -----------------------------------------------------------------------

func TestIntegration_AgentStateInPrompt(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	// Insert an agent state.
	now := time.Now()
	emoji := "🐙"
	status := "running"
	role := "code assistant"
	model := "gpt-4o"
	store.UpsertAgentState(memory.AgentState{
		Agent:           "bob",
		Emoji:           &emoji,
		Status:          &status,
		Role:            &role,
		Model:           &model,
		LastInteraction: &now,
		Updated:         now,
	})

	assembler := memory.NewAssembler(store)
	builder := prompt.New(cfg)

	ctxData, err := assembler.Assemble("ask bob")
	if err != nil {
		t.Fatalf("assemble failed: %v", err)
	}

	if len(ctxData.Agents) != 1 {
		t.Fatalf("expected 1 agent in context, got %d", len(ctxData.Agents))
	}

	sysPrompt := builder.Build(ctxData.Agents, ctxData.Facts, ctxData.Compactions, ctxData.Rules)

	if !strings.Contains(sysPrompt, "bob") {
		t.Fatalf("expected 'bob' in prompt, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "🐙") {
		t.Fatalf("expected emoji in prompt, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "running") {
		t.Fatalf("expected 'running' status in prompt, got:\n%s", sysPrompt)
	}
}

// -----------------------------------------------------------------------
// Integration: Cost tracking through observability hooks
// -----------------------------------------------------------------------

func TestIntegration_CostTracking(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{Text: "Hello!", InputTokens: 500, OutputTokens: 50},
		},
	}

	muxLoop := loop.New(cfg, mock)

	// Wire cost tracking hook (same as main.go does).
	var costRecorded bool
	muxLoop.OnModelCall = func(inputTokens, outputTokens int, costUSD float64) {
		model := cfg.OpenAI.Model
		err := store.AddCost(memory.Cost{
			Category:     "loop",
			Model:        &model,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			CostUSD:      costUSD,
		})
		if err != nil {
			t.Errorf("failed to record cost: %v", err)
		}
		costRecorded = true
	}

	_, err := muxLoop.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}

	if !costRecorded {
		t.Fatal("OnModelCall hook not fired")
	}

	// Verify cost was stored in DB.
	now := time.Now()
	summary, err := store.CostSummary(now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("cost summary failed: %v", err)
	}
	if summary["loop"] == 0 {
		t.Fatal("expected loop cost > 0")
	}
}

// -----------------------------------------------------------------------
// Integration: Tool call observability hook fires
// -----------------------------------------------------------------------

func TestIntegration_ToolCallObservability(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg.Save(cfgPath)
	wrapperPath := filepath.Join(tmpDir, "xnullclaw")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)

	registry := tools.NewRegistry()
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)

	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{
				ToolCalls: []loop.ToolCall{
					{ID: "call_1", Name: "get_persona", Args: map[string]any{}},
				},
				InputTokens: 100, OutputTokens: 20,
			},
			{Text: "Here's your persona config.", InputTokens: 200, OutputTokens: 30},
		},
	}

	muxLoop := loop.New(cfg, mock)
	for _, def := range registry.Definitions() {
		toolName := def.Name
		muxLoop.RegisterTool(loop.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		}, func(ctx context.Context, args map[string]any) (string, error) {
			return registry.Execute(ctx, toolName, args)
		})
	}

	var toolCallNames []string
	muxLoop.OnToolCall = func(name string, args map[string]any, duration time.Duration) {
		toolCallNames = append(toolCallNames, name)
	}

	_, err := muxLoop.Run(context.Background(), "show persona")
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}

	if len(toolCallNames) != 1 || toolCallNames[0] != "get_persona" {
		t.Fatalf("expected tool call 'get_persona', got %v", toolCallNames)
	}
}

// -----------------------------------------------------------------------
// Integration: Multiple tool calls in single turn (parallel execution)
// -----------------------------------------------------------------------

func TestIntegration_ParallelToolCalls(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfg.Save(cfgPath)
	wrapperPath := filepath.Join(tmpDir, "xnullclaw")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)

	registry := tools.NewRegistry()
	tools.RegisterAll(registry, cfg, cfgPath, store, wrapperPath)

	// Model returns 2 tool calls in one response.
	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{
				ToolCalls: []loop.ToolCall{
					{ID: "call_a", Name: "remember", Args: map[string]any{
						"fact": "User prefers dark mode", "importance": "preference",
					}},
					{ID: "call_b", Name: "remember", Args: map[string]any{
						"fact": "User uses Go", "importance": "knowledge",
					}},
				},
				InputTokens: 100, OutputTokens: 40,
			},
			{Text: "Noted both preferences!", InputTokens: 200, OutputTokens: 20},
		},
	}

	muxLoop := loop.New(cfg, mock)
	for _, def := range registry.Definitions() {
		toolName := def.Name
		muxLoop.RegisterTool(loop.ToolDef{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  def.Parameters,
		}, func(ctx context.Context, args map[string]any) (string, error) {
			return registry.Execute(ctx, toolName, args)
		})
	}

	response, err := muxLoop.Run(context.Background(), "Remember I like dark mode and I use Go")
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}
	if response != "Noted both preferences!" {
		t.Fatalf("unexpected response: %q", response)
	}

	// Both facts should be stored.
	facts, err := store.SearchFacts("dark", "", 10)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("expected 'dark mode' fact to be stored")
	}
	facts, err = store.SearchFacts("Go", "", 10)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("expected 'Go' fact to be stored")
	}
}

// -----------------------------------------------------------------------
// Integration: Full turn with message persistence
// -----------------------------------------------------------------------

func TestIntegration_MessagePersistence(t *testing.T) {
	cfg := config.DefaultConfig()
	store := newTestStore(t)
	defer store.Close()

	mock := &mockChatClient{
		responses: []loop.ChatResponse{
			{Text: "I'm doing well, thanks!", InputTokens: 100, OutputTokens: 20},
		},
	}

	muxLoop := loop.New(cfg, mock)

	// Simulate what main.go does: store user msg, run loop, store assistant msg.
	userMsg := "How are you?"
	store.AddMessage(memory.Message{Role: "user", Content: userMsg, Stream: "conversation"})

	response, err := muxLoop.Run(context.Background(), userMsg)
	if err != nil {
		t.Fatalf("loop run failed: %v", err)
	}

	store.AddMessage(memory.Message{Role: "assistant", Content: response, Stream: "conversation"})

	// Verify messages are stored.
	msgs, err := store.RecentMessages("conversation", 10)
	if err != nil {
		t.Fatalf("RecentMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "How are you?" {
		t.Fatalf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "I'm doing well, thanks!" {
		t.Fatalf("unexpected second message: %+v", msgs[1])
	}
}

// -----------------------------------------------------------------------
// Integration: Config load/save roundtrip
// -----------------------------------------------------------------------

func TestIntegration_ConfigRoundtrip(t *testing.T) {
	cfg := config.DefaultConfig()

	// Modify some fields.
	cfg.Persona.Name = "testbot"
	cfg.Persona.Dimensions.Humor = 0.95
	cfg.Costs.DailyBudgetUSD = 15.0
	cfg.OpenAI.Model = "gpt-4o-mini"

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.Persona.Name != "testbot" {
		t.Fatalf("name: got %q, want 'testbot'", loaded.Persona.Name)
	}
	if loaded.Persona.Dimensions.Humor != 0.95 {
		t.Fatalf("humor: got %f, want 0.95", loaded.Persona.Dimensions.Humor)
	}
	if loaded.Costs.DailyBudgetUSD != 15.0 {
		t.Fatalf("daily budget: got %f, want 15.0", loaded.Costs.DailyBudgetUSD)
	}
	if loaded.OpenAI.Model != "gpt-4o-mini" {
		t.Fatalf("model: got %q, want 'gpt-4o-mini'", loaded.OpenAI.Model)
	}
}

// -----------------------------------------------------------------------
// Integration: OpenAI adapter types compile and marshal correctly
// -----------------------------------------------------------------------

func TestIntegration_OpenAIAdapterConstruction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OpenAI.APIKey = "test-key"
	cfg.OpenAI.Model = "gpt-4o"

	adapter := newOpenAIAdapter(cfg)
	if adapter.apiKey != "test-key" {
		t.Fatalf("expected API key 'test-key', got %q", adapter.apiKey)
	}
	if adapter.model != "gpt-4o" {
		t.Fatalf("expected model 'gpt-4o', got %q", adapter.model)
	}
	if adapter.baseURL != "https://api.openai.com/v1" {
		t.Fatalf("expected OpenAI base URL, got %q", adapter.baseURL)
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

func newTestStore(t *testing.T) *memory.Store {
	t.Helper()
	store, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return store
}
