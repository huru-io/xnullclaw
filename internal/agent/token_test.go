package agent

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Must start with prefix.
	if !strings.HasPrefix(token, "zc_") {
		t.Errorf("token missing prefix: %s", token)
	}

	// Prefix + 64 hex chars = 67 total.
	if len(token) != 67 {
		t.Errorf("token length = %d, want 67", len(token))
	}

	// Hex portion must be valid hex.
	hexPart := token[len(tokenPrefix):]
	if _, err := hex.DecodeString(hexPart); err != nil {
		t.Errorf("hex portion invalid: %v", err)
	}
}

func TestGenerateToken_Uniqueness(t *testing.T) {
	t1, _ := GenerateToken()
	t2, _ := GenerateToken()
	if t1 == t2 {
		t.Error("two generated tokens should not be identical")
	}
}

func TestHashToken(t *testing.T) {
	hash := HashToken("zc_deadbeef")

	// SHA-256 → 64 hex chars.
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	if _, err := hex.DecodeString(hash); err != nil {
		t.Errorf("hash not valid hex: %v", err)
	}

	// Deterministic.
	if HashToken("zc_deadbeef") != hash {
		t.Error("hash should be deterministic")
	}

	// Different input → different hash.
	if HashToken("zc_other") == hash {
		t.Error("different inputs should produce different hashes")
	}
}

func TestWriteAndReadToken(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

	token := "zc_test1234"
	if err := WriteToken(dir, token); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	got, err := ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != token {
		t.Errorf("ReadToken = %q, want %q", got, token)
	}
}

func TestReadToken_NotExist(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken on missing file: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing token, got %q", got)
	}
}

func TestSetupWebhookAuth(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write a minimal config.json.
	cfg := map[string]any{"version": 1}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgData, 0600); err != nil {
		t.Fatal(err)
	}

	token, err := SetupWebhookAuth(dir)
	if err != nil {
		t.Fatalf("SetupWebhookAuth: %v", err)
	}

	// Token should be valid format.
	if !strings.HasPrefix(token, "zc_") || len(token) != 67 {
		t.Errorf("bad token format: %s", token)
	}

	// Token file should contain the plaintext token.
	stored, err := ReadToken(dir)
	if err != nil {
		t.Fatalf("ReadToken after setup: %v", err)
	}
	if stored != token {
		t.Errorf("stored token = %q, want %q", stored, token)
	}

	// Config should have gateway.paired_tokens with the hash.
	cfgData, err = os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(cfgData, &doc); err != nil {
		t.Fatal(err)
	}

	gw, ok := doc["gateway"].(map[string]any)
	if !ok {
		t.Fatal("missing gateway section in config")
	}

	tokens, ok := gw["paired_tokens"].([]any)
	if !ok || len(tokens) != 1 {
		t.Fatalf("paired_tokens: got %v", gw["paired_tokens"])
	}

	expectedHash := HashToken(token)
	if tokens[0] != expectedHash {
		t.Errorf("paired_tokens[0] = %v, want %s", tokens[0], expectedHash)
	}

	// require_pairing should be true.
	if gw["require_pairing"] != true {
		t.Errorf("require_pairing = %v, want true", gw["require_pairing"])
	}
}

