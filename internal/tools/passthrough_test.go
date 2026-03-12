package tools

import (
	"context"
	"strings"
	"testing"
)

func TestSetPassthroughRule_Global(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	result, err := r.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "global",
		"rule":  "Always respond in Spanish",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "global") {
		t.Errorf("expected 'global' in result, got: %s", result)
	}
}

func TestSetPassthroughRule_Agent(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	result, err := r.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "alice",
		"rule":  "Always use formal tone",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alice") {
		t.Errorf("expected 'alice' in result, got: %s", result)
	}
}

func TestListPassthroughRules_Empty(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	result, err := r.Execute(context.Background(), "list_passthrough_rules", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No passthrough rules") {
		t.Errorf("expected empty message, got: %s", result)
	}
}

func TestListPassthroughRules_WithRules(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	r.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "global",
		"rule":  "Be concise",
	})
	r.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "alice",
		"rule":  "Use emoji",
	})

	result, err := r.Execute(context.Background(), "list_passthrough_rules", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Be concise") || !strings.Contains(result, "Use emoji") {
		t.Errorf("expected both rules in list, got: %s", result)
	}
	if !strings.Contains(result, "global") || !strings.Contains(result, "alice") {
		t.Errorf("expected scopes in list, got: %s", result)
	}
}

func TestRemovePassthroughRule(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerPassthroughTools(r, store)

	r.Execute(context.Background(), "set_passthrough_rule", map[string]any{
		"scope": "global",
		"rule":  "Be kind",
	})

	result, err := r.Execute(context.Background(), "remove_passthrough_rule", map[string]any{
		"rule_id": "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "removed") {
		t.Errorf("expected 'removed' in result, got: %s", result)
	}

	// Verify empty.
	listResult, _ := r.Execute(context.Background(), "list_passthrough_rules", map[string]any{})
	if !strings.Contains(listResult, "No passthrough rules") {
		t.Errorf("expected empty after removal, got: %s", listResult)
	}
}
