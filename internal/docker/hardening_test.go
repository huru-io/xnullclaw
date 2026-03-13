package docker

import (
	"context"
	"fmt"
	"testing"
)

func TestHardenedConfig(t *testing.T) {
	cfg, hostCfg := HardenedConfig("/home/test/.xnc/alice", "nullclaw:latest", []string{"agent"})

	// Container config.
	if cfg.Image != "nullclaw:latest" {
		t.Errorf("expected image 'nullclaw:latest', got %q", cfg.Image)
	}
	if len(cfg.Cmd) != 1 || cfg.Cmd[0] != "agent" {
		t.Errorf("expected cmd [agent], got %v", cfg.Cmd)
	}
	if cfg.User == "" {
		t.Error("expected user to be set")
	}

	// Host config security.
	if !hostCfg.ReadonlyRootfs {
		t.Error("expected read-only rootfs")
	}
	if len(hostCfg.CapDrop) != 1 || hostCfg.CapDrop[0] != "ALL" {
		t.Errorf("expected cap-drop ALL, got %v", hostCfg.CapDrop)
	}
	expectedSecOpts := []string{"no-new-privileges:true"}
	if len(hostCfg.SecurityOpt) != len(expectedSecOpts) {
		t.Errorf("expected security opts %v, got %v", expectedSecOpts, hostCfg.SecurityOpt)
	} else {
		for i, opt := range expectedSecOpts {
			if hostCfg.SecurityOpt[i] != opt {
				t.Errorf("security opt [%d]: expected %q, got %q", i, opt, hostCfg.SecurityOpt[i])
			}
		}
	}

	// Resource limits.
	if hostCfg.Resources.Memory != 128*1024*1024 {
		t.Errorf("expected 128MB memory, got %d", hostCfg.Resources.Memory)
	}
	if hostCfg.Resources.MemorySwap != 128*1024*1024 {
		t.Errorf("expected 128MB swap, got %d", hostCfg.Resources.MemorySwap)
	}
	if hostCfg.Resources.NanoCPUs != 250000000 {
		t.Errorf("expected 0.25 CPU, got %d", hostCfg.Resources.NanoCPUs)
	}
	if hostCfg.Resources.PidsLimit == nil || *hostCfg.Resources.PidsLimit != 64 {
		t.Error("expected PID limit 64")
	}

	// tmpfs.
	if v, ok := hostCfg.Tmpfs["/tmp"]; !ok || v != "size=64M,noexec,nosuid" {
		t.Errorf("expected tmpfs /tmp, got %v", hostCfg.Tmpfs)
	}

	// Mounts.
	if len(hostCfg.Mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(hostCfg.Mounts))
	}
	if hostCfg.Mounts[0].Target != "/nullclaw-data" {
		t.Errorf("expected data mount at /nullclaw-data, got %q", hostCfg.Mounts[0].Target)
	}
	if !hostCfg.Mounts[1].ReadOnly {
		t.Error("config mount should be read-only")
	}
}

func TestWithPort_LocalMode(t *testing.T) {
	cfg, hostCfg := HardenedConfig("/tmp/test", "img", nil)

	WithPort(cfg, hostCfg, "")

	// Check ExposedPorts on container config.
	if _, ok := cfg.ExposedPorts["3000/tcp"]; !ok {
		t.Error("expected ExposedPorts to contain 3000/tcp")
	}

	bindings, ok := hostCfg.PortBindings["3000/tcp"]
	if !ok {
		t.Fatal("expected port binding for 3000/tcp")
	}
	if len(bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(bindings))
	}
	if bindings[0].HostIP != "127.0.0.1" {
		t.Errorf("expected localhost binding, got %q", bindings[0].HostIP)
	}
	// HostPort="" means Docker auto-assigns an ephemeral port.
	if bindings[0].HostPort != "" {
		t.Errorf("expected empty HostPort (dynamic), got %q", bindings[0].HostPort)
	}
}

func TestWithPort_DockerMode(t *testing.T) {
	cfg, hostCfg := HardenedConfig("/tmp/test", "img", nil)

	WithPort(cfg, hostCfg, "xnc-net")

	// ExposedPorts should still be declared.
	if _, ok := cfg.ExposedPorts["3000/tcp"]; !ok {
		t.Error("expected ExposedPorts to contain 3000/tcp even in docker mode")
	}

	// PortBindings should NOT be set — containers communicate via Docker network DNS.
	if hostCfg.PortBindings != nil && len(hostCfg.PortBindings) > 0 {
		t.Errorf("expected no PortBindings in docker mode, got %v", hostCfg.PortBindings)
	}
}

func TestWithTTY(t *testing.T) {
	cfg, _ := HardenedConfig("/tmp/test", "img", nil)

	if cfg.Tty {
		t.Error("TTY should be false by default")
	}

	WithTTY(cfg)

	if !cfg.Tty || !cfg.OpenStdin || !cfg.AttachStdin || !cfg.AttachStdout {
		t.Error("expected TTY + stdin + attach to be enabled")
	}
}

func TestWithEnv(t *testing.T) {
	cfg, _ := HardenedConfig("/tmp/test", "img", nil)

	WithEnv(cfg, []string{"KEY=value", "FOO=bar"})

	if len(cfg.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(cfg.Env))
	}
}

func TestSecurityFlags(t *testing.T) {
	flags := SecurityFlags()
	if len(flags) != 8 {
		t.Errorf("expected 8 security flags, got %d", len(flags))
	}
}

func TestMockOpsImplementsOps(t *testing.T) {
	// Compile-time check that MockOps satisfies Ops.
	var _ Ops = &MockOps{}
}

func TestMockEnsureNetwork(t *testing.T) {
	called := false
	var gotName string
	mock := &MockOps{
		EnsureNetworkFn: func(_ context.Context, name string) error {
			called = true
			gotName = name
			return nil
		},
	}

	if err := mock.EnsureNetwork(context.Background(), "xnc-net"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("EnsureNetworkFn was not called")
	}
	if gotName != "xnc-net" {
		t.Errorf("name = %q, want %q", gotName, "xnc-net")
	}
}

func TestMockEnsureNetwork_Error(t *testing.T) {
	mock := &MockOps{
		EnsureNetworkFn: func(_ context.Context, name string) error {
			return fmt.Errorf("network error: %s", name)
		},
	}

	err := mock.EnsureNetwork(context.Background(), "bad-net")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "network error: bad-net" {
		t.Errorf("error = %q, want %q", err.Error(), "network error: bad-net")
	}
}

func TestMockEnsureNetwork_Default(t *testing.T) {
	// When EnsureNetworkFn is nil, default returns nil.
	mock := &MockOps{}
	if err := mock.EnsureNetwork(context.Background(), "test"); err != nil {
		t.Fatalf("default should return nil, got: %v", err)
	}
}

func TestMockConnectNetwork(t *testing.T) {
	var gotNet, gotID string
	mock := &MockOps{
		ConnectNetworkFn: func(_ context.Context, networkName, containerID string) error {
			gotNet = networkName
			gotID = containerID
			return nil
		},
	}

	if err := mock.ConnectNetwork(context.Background(), "xnc-net", "abc123def456"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotNet != "xnc-net" {
		t.Errorf("networkName = %q, want %q", gotNet, "xnc-net")
	}
	if gotID != "abc123def456" {
		t.Errorf("containerID = %q, want %q", gotID, "abc123def456")
	}
}

func TestMockConnectNetwork_Default(t *testing.T) {
	mock := &MockOps{}
	if err := mock.ConnectNetwork(context.Background(), "net", "id"); err != nil {
		t.Fatalf("default should return nil, got: %v", err)
	}
}

func TestSelfContainerID_OnHost(t *testing.T) {
	// When running tests on the host (not in a container),
	// SelfContainerID should return empty string.
	id := SelfContainerID()
	// We can't assert it's empty (CI may run in Docker), but it should
	// be either empty or a 12-char hex string.
	if id != "" && (len(id) != 12 || !isHex(id)) {
		t.Errorf("SelfContainerID() = %q, want empty or 12-char hex", id)
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abc123def456", true},
		{"ABCDEF012345", true},
		{"0123456789ab", true},
		{"not-hex-!!", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isHex(tt.s); got != tt.want {
			t.Errorf("isHex(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}
