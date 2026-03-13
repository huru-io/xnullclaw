package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetup(t *testing.T) {
	home := t.TempDir()

	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// Agent should exist.
	if !Exists(home, "alice") {
		t.Fatal("alice should exist after setup")
	}

	// Check directory structure.
	dir := Dir(home, "alice")
	for _, sub := range []string{
		"data/.nullclaw",
		"data/workspace",
	} {
		path := filepath.Join(dir, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected dir %s to exist: %v", sub, err)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory", sub)
		}
	}

	// Check config.json exists and is valid JSON.
	cfgData, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if len(cfgData) < 10 {
		t.Error("config.json too small")
	}

	// Check .meta has CREATED and EMOJI.
	meta, err := ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta["CREATED"] == "" {
		t.Error("CREATED not set in .meta")
	}
	if meta["EMOJI"] == "" {
		t.Error("EMOJI not set in .meta")
	}

	// Check webhook auth token was generated.
	token, err := ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if token == "" {
		t.Error("expected auth token to be generated during setup")
	}

	// Check gateway.paired_tokens in config.
	val, err := ConfigGet(dir, "gateway.paired_tokens")
	if err != nil {
		t.Fatalf("ConfigGet paired_tokens: %v", err)
	}
	tokens, ok := val.([]any)
	if !ok || len(tokens) != 1 {
		t.Fatalf("expected 1 paired token, got %v", val)
	}
	if tokens[0] != HashToken(token) {
		t.Error("paired_tokens hash doesn't match stored token")
	}
}

func TestSetupWithBraveKey(t *testing.T) {
	home := t.TempDir()

	if err := Setup(home, "alice", SetupOpts{BraveKey: "BSA-test-123"}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	dir := Dir(home, "alice")
	val, err := ConfigGet(dir, "brave_key")
	if err != nil {
		t.Fatalf("ConfigGet brave_key: %v", err)
	}
	if val != "BSA-test-123" {
		t.Errorf("expected 'BSA-test-123', got %v", val)
	}
}

func TestSetupConfigPermissions(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	dir := Dir(home, "alice")
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("stat config.json: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

func TestSetupDuplicate(t *testing.T) {
	home := t.TempDir()

	Setup(home, "alice", SetupOpts{})
	err := Setup(home, "alice", SetupOpts{})
	if err == nil {
		t.Fatal("expected error for duplicate setup")
	}
}

func TestSetupInvalidName(t *testing.T) {
	home := t.TempDir()

	err := Setup(home, "1bad", SetupOpts{})
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestSetupReservedName(t *testing.T) {
	home := t.TempDir()

	err := Setup(home, "mux", SetupOpts{})
	if err == nil {
		t.Fatal("expected error for reserved name")
	}
}
