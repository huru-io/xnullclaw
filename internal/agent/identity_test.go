package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmojiPoolSize(t *testing.T) {
	if len(EmojiPool) != 40 {
		t.Errorf("expected 40 emojis, got %d", len(EmojiPool))
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

func TestNextEmoji(t *testing.T) {
	home := t.TempDir()

	// First emoji should be the first in the pool.
	got := NextEmoji(home)
	if got != EmojiPool[0] {
		t.Errorf("expected first emoji %s, got %s", EmojiPool[0], got)
	}

	// Create an agent using that emoji.
	dir := filepath.Join(home, "agent1")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "EMOJI", EmojiPool[0])

	// Next should be second in pool.
	got = NextEmoji(home)
	if got != EmojiPool[1] {
		t.Errorf("expected second emoji %s, got %s", EmojiPool[1], got)
	}
}

func TestNextPort(t *testing.T) {
	home := t.TempDir()

	// First port should be 3001.
	if got := NextPort(home); got != 3001 {
		t.Errorf("expected 3001, got %d", got)
	}

	// Assign port 3001.
	dir := filepath.Join(home, "agent1")
	os.MkdirAll(dir, 0755)
	WriteMeta(dir, "HOST_PORT", "3001")

	// Next should be 3002.
	if got := NextPort(home); got != 3002 {
		t.Errorf("expected 3002, got %d", got)
	}

	// Assign port 3002.
	dir2 := filepath.Join(home, "agent2")
	os.MkdirAll(dir2, 0755)
	WriteMeta(dir2, "HOST_PORT", "3002")

	// Next should be 3003.
	if got := NextPort(home); got != 3003 {
		t.Errorf("expected 3003, got %d", got)
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
