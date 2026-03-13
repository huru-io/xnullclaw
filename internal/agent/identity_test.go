package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEmojiPoolSize(t *testing.T) {
	if len(EmojiPool) != 107 {
		t.Errorf("expected 107 emojis, got %d", len(EmojiPool))
	}
}

func TestEmojiPoolUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range EmojiPool {
		if seen[e] {
			t.Errorf("duplicate emoji: %s", e)
		}
		seen[e] = true
	}
}

func TestEmojiForName(t *testing.T) {
	// Deterministic: same name always yields same emoji.
	e1 := EmojiForName("alice")
	e2 := EmojiForName("alice")
	if e1 != e2 {
		t.Errorf("expected same emoji for 'alice', got %s and %s", e1, e2)
	}

	// Canonical: Alice == alice == ALICE.
	e3 := EmojiForName("Alice")
	if e1 != e3 {
		t.Errorf("expected same emoji for 'alice' and 'Alice', got %s and %s", e1, e3)
	}

	// Different names should (usually) differ.
	e4 := EmojiForName("bob")
	if e1 == e4 {
		t.Log("alice and bob got the same emoji (unlikely but not a bug)")
	}

	// Hyphens/underscores ignored: alice-1 == alice1.
	e5 := EmojiForName("alice-1")
	e6 := EmojiForName("alice_1")
	if e5 != e6 {
		t.Errorf("expected same emoji for 'alice-1' and 'alice_1', got %s and %s", e5, e6)
	}
}

func TestNextEmoji(t *testing.T) {
	home := t.TempDir()

	// First emoji should be the deterministic one for "alice".
	got := NextEmoji(home, "alice")
	want := EmojiForName("alice")
	if got != want {
		t.Errorf("expected deterministic emoji %s for 'alice', got %s", want, got)
	}

	// Create an agent using that emoji.
	dir := filepath.Join(AgentsDir(home), "alice")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "EMOJI", got)

	// A different name should get its own deterministic emoji.
	got2 := NextEmoji(home, "bob")
	want2 := EmojiForName("bob")
	if got2 != want2 {
		t.Errorf("expected deterministic emoji %s for 'bob', got %s", want2, got2)
	}
}

func TestNextEmojiConflict(t *testing.T) {
	home := t.TempDir()

	// Pre-occupy the deterministic emoji for "alice".
	preferred := EmojiForName("alice")
	dir := filepath.Join(AgentsDir(home), "other")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "EMOJI", preferred)

	// NextEmoji should fall back to a different unused emoji.
	got := NextEmoji(home, "alice")
	if got == preferred {
		t.Errorf("expected fallback emoji, got preferred %s (which is taken)", preferred)
	}
	// Should still be a valid pool emoji.
	found := false
	for _, e := range EmojiPool {
		if e == got {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("emoji %s not in pool", got)
	}
}

func TestNextEmojiCompound(t *testing.T) {
	home := t.TempDir()

	// Occupy all 107 single emojis.
	for i, e := range EmojiPool {
		dir := filepath.Join(AgentsDir(home), fmt.Sprintf("agent%d", i))
		os.MkdirAll(dir, 0755)
		WriteMeta(dir, "EMOJI", e)
	}

	// Next emoji must be a compound (two emojis).
	got := NextEmoji(home, "overflow")
	if len([]rune(got)) < 2 {
		t.Errorf("expected compound emoji (2+ runes), got %s", got)
	}

	// Must not be any single pool emoji.
	for _, e := range EmojiPool {
		if got == e {
			t.Errorf("compound emoji should not be a single pool emoji: %s", got)
		}
	}

	// Deterministic: same name same compound.
	got2 := NextEmoji(home, "overflow")
	if got != got2 {
		t.Errorf("expected deterministic compound, got %s and %s", got, got2)
	}
}

func TestNextPort(t *testing.T) {
	home := t.TempDir()

	// First port should be 3001.
	if got := NextPort(home); got != 3001 {
		t.Errorf("expected 3001, got %d", got)
	}

	// Assign port 3001.
	dir := filepath.Join(AgentsDir(home), "agent1")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "HOST_PORT", "3001")

	// Next should be 3002.
	if got := NextPort(home); got != 3002 {
		t.Errorf("expected 3002, got %d", got)
	}

	// Assign port 3002.
	dir2 := filepath.Join(AgentsDir(home), "agent2")
	os.MkdirAll(dir2, 0755)
	WriteMeta(dir2, "HOST_PORT", "3002")

	// Next should be 3003.
	if got := NextPort(home); got != 3003 {
		t.Errorf("expected 3003, got %d", got)
	}
}

func TestAgentPort(t *testing.T) {
	home := t.TempDir()

	// No .meta → 0.
	if got := AgentPort(home, "alice"); got != 0 {
		t.Errorf("expected 0 for missing agent, got %d", got)
	}

	// Create agent with port.
	dir := filepath.Join(AgentsDir(home), "alice")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "HOST_PORT", "3001")

	if got := AgentPort(home, "alice"); got != 3001 {
		t.Errorf("expected 3001, got %d", got)
	}
}

func TestAgentPort_NoPort(t *testing.T) {
	home := t.TempDir()

	// Agent exists but no HOST_PORT.
	dir := filepath.Join(AgentsDir(home), "alice")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "NAME", "alice")

	if got := AgentPort(home, "alice"); got != 0 {
		t.Errorf("expected 0 for agent without port, got %d", got)
	}
}

func TestSuggestName(t *testing.T) {
	home := t.TempDir()

	// Should suggest "alice" first.
	got := SuggestName(home)
	if got != "alice" {
		t.Errorf("expected 'alice', got %q", got)
	}

	// Create alice.
	dir := Dir(home, "alice")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)

	// Should suggest "bob" next.
	got = SuggestName(home)
	if got != "bob" {
		t.Errorf("expected 'bob', got %q", got)
	}
}
