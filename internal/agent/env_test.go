package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerEnv_WithKey(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	cfg["http_request"].(map[string]any)["brave_api_key"] = "BSA-test-key-123"
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	env := ContainerEnv(dir)
	if len(env) != 1 {
		t.Fatalf("expected 1 env var, got %d", len(env))
	}
	if env[0] != "BRAVE_API_KEY=BSA-test-key-123" {
		t.Errorf("unexpected env var: %s", env[0])
	}
}

func TestContainerEnv_EmptyKey(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	cfg["http_request"].(map[string]any)["brave_api_key"] = ""
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	env := ContainerEnv(dir)
	if len(env) != 0 {
		t.Errorf("expected empty env for blank key, got %v", env)
	}
}

func TestContainerEnv_MissingKey(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	env := ContainerEnv(dir)
	if len(env) != 0 {
		t.Errorf("expected empty env for missing key, got %v", env)
	}
}

func TestContainerEnv_MissingConfig(t *testing.T) {
	dir := t.TempDir()
	// No config.json — should return nil, not panic.
	env := ContainerEnv(dir)
	if len(env) != 0 {
		t.Errorf("expected nil env for missing config, got %v", env)
	}
}

func TestStartOpts(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{})

	opts := StartOpts("nullclaw:latest", home, "alice", 8080)
	if opts.Image != "nullclaw:latest" {
		t.Errorf("unexpected image: %s", opts.Image)
	}
	if len(opts.Cmd) != 1 || opts.Cmd[0] != ContainerCmd {
		t.Errorf("unexpected cmd: %v", opts.Cmd)
	}
	if opts.Port != 8080 {
		t.Errorf("unexpected port: %d", opts.Port)
	}
	if !strings.HasSuffix(opts.AgentDir, "alice") {
		t.Errorf("unexpected agent dir: %s", opts.AgentDir)
	}
	// No brave key → no env vars.
	if len(opts.Env) != 0 {
		t.Errorf("expected no env vars, got %v", opts.Env)
	}
}

func TestStartOpts_WithBraveKey(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{BraveKey: "BSA-test-key"})

	opts := StartOpts("nullclaw:latest", home, "alice", 0)
	if len(opts.Env) != 1 {
		t.Fatalf("expected 1 env var, got %d: %v", len(opts.Env), opts.Env)
	}
	if opts.Env[0] != "BRAVE_API_KEY=BSA-test-key" {
		t.Errorf("unexpected env var: %s", opts.Env[0])
	}
}

func TestRedactKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "****"},
		{"short", "****"},
		{"12345678", "****"},
		{"123456789", "1234*6789"},
		{"sk-1234567890abcdef", "sk-1***********cdef"},
	}
	for _, tt := range tests {
		got := RedactKey(tt.input)
		if got != tt.want {
			t.Errorf("RedactKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
