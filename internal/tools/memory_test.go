package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRemember_HappyPath(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	result, err := r.Execute(context.Background(), "remember", map[string]any{
		"fact":     "User prefers dark mode",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "remembered" {
		t.Errorf("got %q, want %q", result, "remembered")
	}
}

func TestRemember_InvalidCategory(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	_, err := r.Execute(context.Background(), "remember", map[string]any{
		"fact":     "something",
		"category": "invalid_category",
	})
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
	if !strings.Contains(err.Error(), "invalid category") {
		t.Errorf("expected 'invalid category' in error, got: %v", err)
	}
}

func TestRecall_NoResults(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	result, err := r.Execute(context.Background(), "recall", map[string]any{
		"query": "nonexistent_fact_xyz",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No matching") {
		t.Errorf("expected 'No matching' message, got: %s", result)
	}
}

func TestRecall_WithResults(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	// Store a fact first.
	r.Execute(context.Background(), "remember", map[string]any{
		"fact":     "User name is Alice",
		"category": "knowledge",
	})

	result, err := r.Execute(context.Background(), "recall", map[string]any{
		"query": "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected 'Alice' in recall result, got: %s", result)
	}
}

func TestGetConversationSummary_Empty(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerMemoryTools(r, store)

	result, err := r.Execute(context.Background(), "get_conversation_summary", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No conversation summaries") {
		t.Errorf("expected empty result message, got: %s", result)
	}
}
