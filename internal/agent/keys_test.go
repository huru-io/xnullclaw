package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectKeys_EmptyHome(t *testing.T) {
	home := t.TempDir()
	keys := CollectKeys(home)
	if len(keys) != 0 {
		t.Errorf("expected empty map, got %v", keys)
	}
}

func TestCollectKeys_SingleAgent(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{
		OpenAIKey: "sk-test-openai",
		BraveKey:  "BSA-test-brave",
	})

	keys := CollectKeys(home)

	if keys["openai"] != "sk-test-openai" {
		t.Errorf("expected openai key, got %q", keys["openai"])
	}
	if keys["brave"] != "BSA-test-brave" {
		t.Errorf("expected brave key, got %q", keys["brave"])
	}
	if keys["anthropic"] != "" {
		t.Errorf("expected empty anthropic, got %q", keys["anthropic"])
	}
}

func TestCollectKeys_IncludesModel(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{Model: "gpt-5"})

	keys := CollectKeys(home)

	if keys["model"] != "gpt-5" {
		t.Errorf("expected model gpt-5, got %q", keys["model"])
	}
}

func TestCollectKeys_FirstWins(t *testing.T) {
	home := t.TempDir()

	// Create alice with openai key.
	Setup(home, "alice", SetupOpts{OpenAIKey: "sk-alice"})
	// Create bob with a different openai key.
	Setup(home, "bob", SetupOpts{OpenAIKey: "sk-bob"})

	keys := CollectKeys(home)

	// ListAll sorts alphabetically, so alice comes first.
	if keys["openai"] != "sk-alice" {
		t.Errorf("expected first agent's key (sk-alice), got %q", keys["openai"])
	}
}

func TestCollectKeys_SkipsEmptyValues(t *testing.T) {
	home := t.TempDir()

	// alice has no brave key, bob does.
	Setup(home, "alice", SetupOpts{OpenAIKey: "sk-test"})
	Setup(home, "bob", SetupOpts{OpenAIKey: "sk-test", BraveKey: "BSA-from-bob"})

	keys := CollectKeys(home)

	if keys["brave"] != "BSA-from-bob" {
		t.Errorf("expected brave key from bob, got %q", keys["brave"])
	}
}

func TestCollectKeys_CorruptConfig(t *testing.T) {
	home := t.TempDir()

	// Create alice normally, then bob with corrupt config.
	Setup(home, "alice", SetupOpts{OpenAIKey: "sk-alice"})
	Setup(home, "bob", SetupOpts{BraveKey: "BSA-bob"})
	// Corrupt bob's config.
	os.WriteFile(filepath.Join(Dir(home, "bob"), "config.json"), []byte("{invalid"), 0600)

	keys := CollectKeys(home)

	// alice's key should still be collected despite bob's corrupt config.
	if keys["openai"] != "sk-alice" {
		t.Errorf("expected openai key from alice, got %q", keys["openai"])
	}
	// bob's brave key should be missing due to corrupt config.
	if keys["brave"] != "" {
		t.Errorf("expected empty brave (corrupt config), got %q", keys["brave"])
	}
}
