package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/memory"
)

// testStore creates an in-memory SQLite store for testing.
func testStore(t *testing.T) *memory.Store {
	t.Helper()
	store, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestRegistryBasic(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:        "echo",
		Description: "Echoes the input",
		Parameters:  map[string]any{"type": "object"},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		s, _ := stringArg(args, "text")
		return s, nil
	})

	if len(r.Definitions()) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(r.Definitions()))
	}
	if r.Definitions()[0].Name != "echo" {
		t.Fatalf("expected 'echo', got %q", r.Definitions()[0].Name)
	}

	result, err := r.Execute(context.Background(), "echo", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Fatalf("expected 'hello', got %q", result)
	}

	_, err = r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRememberAndRecall(t *testing.T) {
	store := testStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	ctx := context.Background()

	// Remember a fact.
	result, err := r.Execute(ctx, "remember", map[string]any{
		"fact":       "User prefers dark mode",
		"importance": "preference",
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}
	if result != "remembered" {
		t.Fatalf("expected 'remembered', got %q", result)
	}

	// Remember another.
	_, err = r.Execute(ctx, "remember", map[string]any{
		"fact":       "Always use Go for backend",
		"importance": "rule",
	})
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}

	// Recall.
	result, err = r.Execute(ctx, "recall", map[string]any{"query": "dark"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if result == "No matching facts found." {
		t.Fatal("expected to find 'dark mode' fact")
	}
	if !contains(result, "dark mode") {
		t.Fatalf("expected result to contain 'dark mode', got: %s", result)
	}

	// Recall with no match.
	result, err = r.Execute(ctx, "recall", map[string]any{"query": "zzzznonexistent"})
	if err != nil {
		t.Fatalf("recall failed: %v", err)
	}
	if result != "No matching facts found." {
		t.Fatalf("expected no results, got: %s", result)
	}
}

func TestRememberInvalidImportance(t *testing.T) {
	store := testStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	_, err := r.Execute(context.Background(), "remember", map[string]any{
		"fact":       "test",
		"importance": "invalid_type",
	})
	if err == nil {
		t.Fatal("expected error for invalid importance")
	}
}

func TestGetConversationSummary(t *testing.T) {
	store := testStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	// No compactions yet.
	result, err := r.Execute(context.Background(), "get_conversation_summary", map[string]any{})
	if err != nil {
		t.Fatalf("get_conversation_summary failed: %v", err)
	}
	if result != "No conversation summaries available yet." {
		t.Fatalf("expected no summaries, got: %s", result)
	}
}

func TestPassthroughRules(t *testing.T) {
	store := testStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	ctx := context.Background()

	// List empty.
	result, err := r.Execute(ctx, "list_passthrough_rules", map[string]any{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if result != "No passthrough rules configured." {
		t.Fatalf("expected no rules, got: %s", result)
	}

	// Add a global rule.
	result, err = r.Execute(ctx, "set_passthrough_rule", map[string]any{
		"scope": "global",
		"rule":  "Fix obvious typos silently",
	})
	if err != nil {
		t.Fatalf("set rule failed: %v", err)
	}
	if !contains(result, "global") {
		t.Fatalf("expected global scope in result, got: %s", result)
	}

	// Add an agent-specific rule.
	_, err = r.Execute(ctx, "set_passthrough_rule", map[string]any{
		"scope": "bob",
		"rule":  "Always use formal English for bob",
	})
	if err != nil {
		t.Fatalf("set rule failed: %v", err)
	}

	// List rules.
	result, err = r.Execute(ctx, "list_passthrough_rules", map[string]any{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !contains(result, "Fix obvious typos") {
		t.Fatalf("expected global rule in list, got: %s", result)
	}
	if !contains(result, "bob") {
		t.Fatalf("expected bob rule in list, got: %s", result)
	}
}

func TestPersonaTools(t *testing.T) {
	cfg := config.DefaultConfig()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	r := NewRegistry()
	registerPersonaTools(r, cfg, cfgPath, "/nonexistent/wrapper")

	ctx := context.Background()

	// Get persona.
	result, err := r.Execute(ctx, "get_persona", map[string]any{})
	if err != nil {
		t.Fatalf("get_persona failed: %v", err)
	}
	if !contains(result, "mux") {
		t.Fatalf("expected 'mux' in persona, got: %s", result)
	}

	// Set persona field.
	_, err = r.Execute(ctx, "set_persona", map[string]any{
		"field": "name",
		"value": "jarvis",
	})
	if err != nil {
		t.Fatalf("set_persona failed: %v", err)
	}
	if cfg.Persona.Name != "jarvis" {
		t.Fatalf("expected persona name 'jarvis', got %q", cfg.Persona.Name)
	}

	// Verify persisted to disk.
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if loaded.Persona.Name != "jarvis" {
		t.Fatalf("expected persisted name 'jarvis', got %q", loaded.Persona.Name)
	}

	// Set dimension.
	_, err = r.Execute(ctx, "set_persona_dimension", map[string]any{
		"dimension": "humor",
		"value":     0.9,
	})
	if err != nil {
		t.Fatalf("set_persona_dimension failed: %v", err)
	}
	if cfg.Persona.Dimensions.Humor != 0.9 {
		t.Fatalf("expected humor 0.9, got %f", cfg.Persona.Dimensions.Humor)
	}

	// Invalid dimension value (out of range).
	_, err = r.Execute(ctx, "set_persona_dimension", map[string]any{
		"dimension": "humor",
		"value":     1.5,
	})
	if err == nil {
		t.Fatal("expected error for out-of-range dimension value")
	}

	// Invalid dimension name.
	_, err = r.Execute(ctx, "set_persona_dimension", map[string]any{
		"dimension": "nonexistent",
		"value":     0.5,
	})
	if err == nil {
		t.Fatal("expected error for invalid dimension name")
	}

	// Apply preset.
	_, err = r.Execute(ctx, "apply_persona_preset", map[string]any{
		"preset": "creative",
	})
	if err != nil {
		t.Fatalf("apply_persona_preset failed: %v", err)
	}
	if cfg.Persona.Dimensions.Creativity != 0.9 {
		t.Fatalf("expected creativity 0.9 after 'creative' preset, got %f", cfg.Persona.Dimensions.Creativity)
	}

	// Reset.
	_, err = r.Execute(ctx, "reset_persona", map[string]any{})
	if err != nil {
		t.Fatalf("reset_persona failed: %v", err)
	}
	if cfg.Persona.Name != "mux" {
		t.Fatalf("expected name 'mux' after reset, got %q", cfg.Persona.Name)
	}
}

func TestCostTools(t *testing.T) {
	cfg := config.DefaultConfig()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	store := testStore(t)

	// Add some cost entries.
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 0.01})
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 0.02})
	store.AddCost(memory.Cost{Category: "compaction", CostUSD: 0.005})

	// Use a dummy wrapper path — get_agent_costs will fail but get_costs and set_budget won't.
	r := NewRegistry()
	registerCostTools(r, cfg, cfgPath, store, "/nonexistent/wrapper")

	ctx := context.Background()

	// Get costs.
	result, err := r.Execute(ctx, "get_costs", map[string]any{"period": "today"})
	if err != nil {
		t.Fatalf("get_costs failed: %v", err)
	}
	if !contains(result, "mux_costs") {
		t.Fatalf("expected 'mux_costs' in result, got: %s", result)
	}
	if !contains(result, "loop") {
		t.Fatalf("expected 'loop' category in result, got: %s", result)
	}

	// Set budget.
	_, err = r.Execute(ctx, "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": 10.0,
		"period":    "daily",
	})
	if err != nil {
		t.Fatalf("set_budget failed: %v", err)
	}
	if cfg.Costs.DailyBudgetUSD != 10.0 {
		t.Fatalf("expected daily budget 10.0, got %f", cfg.Costs.DailyBudgetUSD)
	}

	// Verify persisted.
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if loaded.Costs.DailyBudgetUSD != 10.0 {
		t.Fatalf("expected persisted daily budget 10.0, got %f", loaded.Costs.DailyBudgetUSD)
	}
}

func TestRegisterAll(t *testing.T) {
	cfg := config.DefaultConfig()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	store := testStore(t)
	r := NewRegistry()

	// Create a dummy wrapper script.
	wrapperPath := filepath.Join(tmpDir, "xnc")
	os.WriteFile(wrapperPath, []byte("#!/bin/sh\necho ok"), 0755)

	RegisterAll(r, cfg, cfgPath, store, wrapperPath)

	defs := r.Definitions()
	if len(defs) == 0 {
		t.Fatal("expected registered tools, got 0")
	}

	// Check we have the expected tool categories.
	names := make(map[string]bool)
	for _, d := range defs {
		names[d.Name] = true
	}

	expectedTools := []string{
		"remember", "recall", "get_conversation_summary",
		"set_persona", "set_persona_dimension", "get_persona", "reset_persona", "apply_persona_preset", "list_voices",
		"get_costs", "set_budget",
		"set_passthrough_rule", "remove_passthrough_rule", "list_passthrough_rules",
		"send_to_agent", "list_agents", "agent_status", "provision_agent", "destroy_agent",
		"get_agent_persona", "update_agent_persona",
	}
	for _, name := range expectedTools {
		if !names[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	t.Logf("Registered %d tools total", len(defs))
}

func TestArgHelpers(t *testing.T) {
	// stringArg
	s, err := stringArg(map[string]any{"k": "v"}, "k")
	if err != nil || s != "v" {
		t.Fatalf("stringArg: got %q, %v", s, err)
	}
	_, err = stringArg(map[string]any{}, "k")
	if err == nil {
		t.Fatal("stringArg: expected error for missing key")
	}
	_, err = stringArg(map[string]any{"k": 123}, "k")
	if err == nil {
		t.Fatal("stringArg: expected error for wrong type")
	}

	// float64Arg
	f, err := float64Arg(map[string]any{"k": 1.5}, "k")
	if err != nil || f != 1.5 {
		t.Fatalf("float64Arg: got %f, %v", f, err)
	}
	f, err = float64Arg(map[string]any{"k": 2}, "k")
	if err != nil || f != 2.0 {
		t.Fatalf("float64Arg int: got %f, %v", f, err)
	}
	_, err = float64Arg(map[string]any{}, "k")
	if err == nil {
		t.Fatal("float64Arg: expected error for missing key")
	}

	// stringSliceArg
	sl, err := stringSliceArg(map[string]any{"k": []any{"a", "b"}}, "k")
	if err != nil || len(sl) != 2 {
		t.Fatalf("stringSliceArg: got %v, %v", sl, err)
	}
	sl, err = stringSliceArg(map[string]any{"k": []string{"x"}}, "k")
	if err != nil || len(sl) != 1 {
		t.Fatalf("stringSliceArg []string: got %v, %v", sl, err)
	}

	// optionalStringArg
	s = optionalStringArg(map[string]any{"k": "v"}, "k", "default")
	if s != "v" {
		t.Fatalf("optionalStringArg present: got %q", s)
	}
	s = optionalStringArg(map[string]any{}, "k", "default")
	if s != "default" {
		t.Fatalf("optionalStringArg missing: got %q", s)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
