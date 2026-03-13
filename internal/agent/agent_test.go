package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"alice", false},
		{"bob-1", false},
		{"Agent_2", false},
		{"myAgent", false},
		{"Gonzales", false},
		{"R2D2", false},
		{"alice-bob", false},              // one hyphen ok
		{"my_agent", false},               // one underscore ok

		{"", true},                        // empty
		{"ab", true},                      // too short (canonical "ab" < 3)
		{"a", true},                       // too short
		{"a----", true},                   // multiple hyphens
		{"a-b-c", true},                   // two hyphens
		{"a_b_c", true},                   // two underscores
		{"a__b", true},                    // consecutive underscores
		{"a-b_c", true},                   // mixed separator
		{"agent-", true},                  // ends with hyphen
		{"agent_", true},                  // ends with underscore
		{"1agent", true},                  // starts with digit
		{"-agent", true},                  // starts with hyphen
		{"_agent", true},                  // starts with underscore
		{"agent!", true},                  // special char
		{"agent name", true},              // space
		{"mux", true},                     // reserved
		{"help", true},                    // reserved
		{"list", true},                    // reserved
		{"init", true},                    // reserved
		{"abcdefghijklmnopqrstu", true},   // too long (21 chars)
		{"abcdefghijklmnopqrst", false},   // exactly 20 chars
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestContainerName(t *testing.T) {
	home := t.TempDir()

	got := ContainerName(home, "alice")
	// Should be xnc-<6hex>-alice
	if !strings.HasPrefix(got, "xnc-") || !strings.HasSuffix(got, "-alice") {
		t.Errorf("ContainerName = %q, want xnc-<id>-alice", got)
	}
	// Length: "xnc-" (4) + 6 hex + "-" (1) + "alice" (5) = 16
	if len(got) != 16 {
		t.Errorf("ContainerName length = %d, want 16", len(got))
	}
}

func TestInstanceID(t *testing.T) {
	home := t.TempDir()

	id1 := InstanceID(home)
	if len(id1) != 6 {
		t.Fatalf("InstanceID length = %d, want 6", len(id1))
	}

	// Same home returns same ID.
	id2 := InstanceID(home)
	if id1 != id2 {
		t.Errorf("InstanceID changed: %q vs %q", id1, id2)
	}

	// Different home returns different ID (with high probability).
	home2 := t.TempDir()
	id3 := InstanceID(home2)
	if id1 == id3 {
		t.Logf("warning: two random IDs collided (unlikely but possible): %s", id1)
	}
}

func TestContainerPrefix(t *testing.T) {
	home := t.TempDir()
	prefix := ContainerPrefix(home)
	if !strings.HasPrefix(prefix, "xnc-") || !strings.HasSuffix(prefix, "-") {
		t.Errorf("ContainerPrefix = %q, want xnc-<id>-", prefix)
	}
	// Length: "xnc-" (4) + 6 hex + "-" (1) = 11
	if len(prefix) != 11 {
		t.Errorf("ContainerPrefix length = %d, want 11", len(prefix))
	}
}

func TestDirAndExists(t *testing.T) {
	home := t.TempDir()

	if Exists(home, "alice") {
		t.Fatal("alice should not exist yet")
	}

	// Create agent structure.
	dir := Dir(home, "alice")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)

	if !Exists(home, "alice") {
		t.Fatal("alice should exist now")
	}
}

func TestListAll(t *testing.T) {
	home := t.TempDir()

	// Empty home.
	agents, err := ListAll(home)
	if err != nil {
		t.Fatalf("ListAll empty: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("expected 0 agents, got %d", len(agents))
	}

	// Create two agents.
	for _, name := range []string{"charlie", "alice"} {
		dir := Dir(home, name)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)
		WriteMeta(dir, "EMOJI", "🍎")
		WriteMeta(dir, "CREATED", "2026-01-01T00:00:00Z")
	}

	agents, err = ListAll(home)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	// Sorted alphabetically.
	if agents[0].Name != "alice" {
		t.Errorf("expected first agent 'alice', got %q", agents[0].Name)
	}
	if agents[1].Name != "charlie" {
		t.Errorf("expected second agent 'charlie', got %q", agents[1].Name)
	}
	if agents[0].Emoji != "🍎" {
		t.Errorf("expected emoji 🍎, got %q", agents[0].Emoji)
	}
}

func TestCanonicalName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"alice", "alice"},
		{"Alice", "alice"},
		{"ALICE", "alice"},
		{"Perez1", "perez1"},
		{"Perez-1", "perez1"},
		{"Perez_1", "perez1"},
		{"perez_1", "perez1"},
		{"my-Agent_2", "myagent2"},
		{"Bob", "bob"},
	}
	for _, tt := range tests {
		if got := CanonicalName(tt.input); got != tt.want {
			t.Errorf("CanonicalName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConflictsWith(t *testing.T) {
	home := t.TempDir()

	// Create "perez1" agent (Dir canonicalizes to perez1).
	dir := Dir(home, "Perez1")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)

	// Since Dir() now canonicalizes, Perez-1/Perez_1/perez1 all map to
	// the same directory "perez1", so Exists() catches them directly.
	// ConflictsWith is for truly different canonical names that are still
	// confusable (currently none, but the mechanism remains).
	if !Exists(home, "Perez-1") {
		t.Error("expected Perez-1 to exist (same canonical dir as Perez1)")
	}
	if !Exists(home, "Perez_1") {
		t.Error("expected Perez_1 to exist (same canonical dir as Perez1)")
	}
	if !Exists(home, "perez1") {
		t.Error("expected perez1 to exist (same canonical dir as Perez1)")
	}

	// Alice should not exist.
	if Exists(home, "alice") {
		t.Error("expected alice not to exist")
	}
}

func TestSetupNameConflict(t *testing.T) {
	home := t.TempDir()

	// Create Perez1 (stored as canonical "perez1").
	if err := Setup(home, "Perez1", SetupOpts{}); err != nil {
		t.Fatalf("Setup Perez1: %v", err)
	}

	// Perez-1 should be rejected (same canonical dir).
	err := Setup(home, "Perez-1", SetupOpts{})
	if err == nil {
		t.Fatal("expected error creating Perez-1 when Perez1 exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}

	// perez1 should be rejected.
	err = Setup(home, "perez1", SetupOpts{})
	if err == nil {
		t.Fatal("expected error creating perez1 when Perez1 exists")
	}

	// Totally different name should work.
	if err := Setup(home, "alice", SetupOpts{}); err != nil {
		t.Fatalf("Setup alice: %v", err)
	}
}

func TestSetupComplete(t *testing.T) {
	home := t.TempDir()

	// Empty dir — not complete.
	if SetupComplete(home) {
		t.Fatal("empty dir should not be complete")
	}

	// Make it look like an xnc home.
	InstanceID(home)
	if SetupComplete(home) {
		t.Fatal("xnc home with no agents should not be complete")
	}

	// Create an agent (no key).
	if err := Setup(home, "nokey", SetupOpts{}); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if SetupComplete(home) {
		t.Fatal("agent with no API key should not be complete")
	}

	// Set a provider key.
	if err := ConfigSet(Dir(home, "nokey"), "openai_key", "sk-test123"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}
	if !SetupComplete(home) {
		t.Fatal("agent with API key should be complete")
	}
}

func TestHasProviderKey(t *testing.T) {
	home := t.TempDir()
	InstanceID(home)

	if err := Setup(home, "agent1", SetupOpts{}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// No key set.
	if HasProviderKey(home, "agent1") {
		t.Error("should not have provider key initially")
	}

	// Set anthropic key.
	if err := ConfigSet(Dir(home, "agent1"), "anthropic_key", "sk-ant-test"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}
	if !HasProviderKey(home, "agent1") {
		t.Error("should have provider key after setting anthropic_key")
	}
}

func TestDefaultHome(t *testing.T) {
	// Reset env.
	orig := os.Getenv("XNC_HOME")
	origOld := os.Getenv("XNULLCLAW_HOME")
	defer func() {
		os.Setenv("XNC_HOME", orig)
		os.Setenv("XNULLCLAW_HOME", origOld)
	}()

	os.Setenv("XNC_HOME", "/custom/path")
	os.Setenv("XNULLCLAW_HOME", "")
	if got := DefaultHome(); got != "/custom/path" {
		t.Errorf("expected /custom/path, got %q", got)
	}

	os.Setenv("XNC_HOME", "")
	os.Setenv("XNULLCLAW_HOME", "/old/path")
	if got := DefaultHome(); got != "/old/path" {
		t.Errorf("expected /old/path, got %q", got)
	}
}

func TestRuntimeMode(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"", "local"},
		{"local", "local"},
		{"docker", "docker"},
		{"kubernetes", "kubernetes"},
		{"banana", "local"},     // invalid → fallback
		{"DOCKER", "local"},     // case-sensitive
		{"k8s", "local"},        // not a valid mode
	}
	for _, tt := range tests {
		t.Run("env="+tt.env, func(t *testing.T) {
			t.Setenv("XNC_RUNTIME", tt.env)
			if got := RuntimeMode(); got != tt.want {
				t.Errorf("RuntimeMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNetworkName(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"", ""},
		{"xnc-net", "xnc-net"},
		{"my_network", "my_network"},
		{"bad network!", ""},  // invalid: spaces and special chars
		{"/evil", ""},         // invalid: starts with slash
		{"a", "a"},            // valid: single char network name
	}
	for _, tt := range tests {
		t.Run("env="+tt.env, func(t *testing.T) {
			t.Setenv("XNC_NETWORK", tt.env)
			if got := NetworkName(); got != tt.want {
				t.Errorf("NetworkName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNullclawRegistry(t *testing.T) {
	if NullclawRegistry != "ghcr.io/nullclaw/nullclaw" {
		t.Errorf("NullclawRegistry = %q", NullclawRegistry)
	}
	if NullclawLatestRef != NullclawRegistry+":latest" {
		t.Errorf("NullclawLatestRef = %q", NullclawLatestRef)
	}
}

func TestHostHome(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "")
		if got := HostHome(); got != "" {
			t.Errorf("HostHome() = %q, want empty", got)
		}
	})
	t.Run("absolute_path", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "/home/user/.xnc")
		if got := HostHome(); got != "/home/user/.xnc" {
			t.Errorf("HostHome() = %q, want %q", got, "/home/user/.xnc")
		}
	})
	t.Run("relative_path_rejected", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "relative/path")
		if got := HostHome(); got != "" {
			t.Errorf("HostHome() = %q, want empty (relative path should be rejected)", got)
		}
	})
	t.Run("dot_path_rejected", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "./local")
		if got := HostHome(); got != "" {
			t.Errorf("HostHome() = %q, want empty (relative path should be rejected)", got)
		}
	})
	t.Run("tilde_path_rejected", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "~/.xnc")
		if got := HostHome(); got != "" {
			t.Errorf("HostHome() = %q, want empty (tilde path is not absolute)", got)
		}
	})
	t.Run("traversal_cleaned", func(t *testing.T) {
		t.Setenv("XNC_HOST_HOME", "/home/user/../etc")
		got := HostHome()
		// filepath.Clean resolves ".." lexically: /home/user/../etc → /home/etc
		if got != "/home/etc" {
			t.Errorf("HostHome() = %q, want /home/etc (path traversal should be cleaned)", got)
		}
	})
}
