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
	// 2 gateway vars + 1 brave key = 3
	if len(env) != 3 {
		t.Fatalf("expected 3 env vars, got %d: %v", len(env), env)
	}
	if env[0] != "NULLCLAW_GATEWAY_HOST=0.0.0.0" {
		t.Errorf("env[0] = %q, want gateway host", env[0])
	}
	if env[2] != "BRAVE_API_KEY=BSA-test-key-123" {
		t.Errorf("env[2] = %q, want brave key", env[2])
	}
}

func TestContainerEnv_EmptyKey(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	cfg["http_request"].(map[string]any)["brave_api_key"] = ""
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	env := ContainerEnv(dir)
	// Only the 2 gateway vars (no brave key since it's empty).
	if len(env) != 2 {
		t.Errorf("expected 2 gateway env vars, got %v", env)
	}
}

func TestContainerEnv_MissingKey(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	env := ContainerEnv(dir)
	// Only the 2 gateway vars (no brave key in default config).
	if len(env) != 2 {
		t.Errorf("expected 2 gateway env vars, got %v", env)
	}
}

func TestContainerEnv_MissingConfig(t *testing.T) {
	dir := t.TempDir()
	// No config.json — gateway vars still present, config-driven vars skipped.
	env := ContainerEnv(dir)
	if len(env) != 2 {
		t.Errorf("expected 2 gateway env vars even without config, got %v", env)
	}
}

func TestStartOpts(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{})

	opts := StartOpts("nullclaw:latest", home, "alice", true, "")
	if opts.Image != "nullclaw:latest" {
		t.Errorf("unexpected image: %s", opts.Image)
	}
	if len(opts.Cmd) != 1 || opts.Cmd[0] != ContainerCmd {
		t.Errorf("unexpected cmd: %v", opts.Cmd)
	}
	if !opts.ExposePort {
		t.Error("expected ExposePort=true")
	}
	if !strings.HasSuffix(opts.AgentDir, "alice") {
		t.Errorf("unexpected agent dir: %s", opts.AgentDir)
	}
	// 2 gateway vars + 1 web token = 3 (Setup creates a .token file).
	if len(opts.Env) != 3 {
		t.Errorf("expected 3 env vars, got %v", opts.Env)
	}
	if !strings.HasPrefix(opts.Env[2], "NULLCLAW_WEB_TOKEN=") {
		t.Errorf("env[2] = %q, want NULLCLAW_WEB_TOKEN=...", opts.Env[2])
	}
	// Empty network name.
	if opts.NetworkName != "" {
		t.Errorf("NetworkName = %q, want empty", opts.NetworkName)
	}
}

func TestStartOpts_WithNetworkName(t *testing.T) {
	home := t.TempDir()
	Setup(home, "bob", SetupOpts{})

	opts := StartOpts("nullclaw:latest", home, "bob", true, "xnc-net")
	if opts.NetworkName != "xnc-net" {
		t.Errorf("NetworkName = %q, want %q", opts.NetworkName, "xnc-net")
	}
}

func TestStartOpts_HostHome(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{})

	// Without XNC_HOST_HOME: AgentDir uses container-local path.
	t.Setenv("XNC_HOST_HOME", "")
	opts := StartOpts("nullclaw:latest", home, "alice", true, "")
	if !strings.HasPrefix(opts.AgentDir, home) {
		t.Errorf("without HostHome, AgentDir should use home; got %q", opts.AgentDir)
	}

	// With XNC_HOST_HOME: AgentDir uses host-side path for Docker daemon.
	t.Setenv("XNC_HOST_HOME", "/host/path/.xnc")
	opts = StartOpts("nullclaw:latest", home, "alice", true, "xnc-net")
	if !strings.HasPrefix(opts.AgentDir, "/host/path/.xnc") {
		t.Errorf("with HostHome, AgentDir should use host path; got %q", opts.AgentDir)
	}
	if !strings.HasSuffix(opts.AgentDir, "alice") {
		t.Errorf("AgentDir should end with agent name; got %q", opts.AgentDir)
	}
}

func TestStartOpts_WithBraveKey(t *testing.T) {
	home := t.TempDir()
	Setup(home, "alice", SetupOpts{BraveKey: "BSA-test-key"})

	opts := StartOpts("nullclaw:latest", home, "alice", false, "")
	if opts.ExposePort {
		t.Error("expected ExposePort=false when passed false")
	}
	// 2 gateway vars + 1 web token + 1 brave key = 4
	if len(opts.Env) != 4 {
		t.Fatalf("expected 4 env vars, got %d: %v", len(opts.Env), opts.Env)
	}
	if !strings.HasPrefix(opts.Env[2], "NULLCLAW_WEB_TOKEN=") {
		t.Errorf("env[2] = %q, want NULLCLAW_WEB_TOKEN=...", opts.Env[2])
	}
	if opts.Env[3] != "BRAVE_API_KEY=BSA-test-key" {
		t.Errorf("env[3] = %q, want brave key", opts.Env[3])
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
