package docker

import (
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
	expectedSecOpts := []string{"no-new-privileges:true", "seccomp=default"}
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

func TestWithPort(t *testing.T) {
	cfg, hostCfg := HardenedConfig("/tmp/test", "img", nil)

	WithPort(cfg, hostCfg)

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
	if len(flags) != 9 {
		t.Errorf("expected 9 security flags, got %d", len(flags))
	}
}

func TestMockOpsImplementsOps(t *testing.T) {
	// Compile-time check that MockOps satisfies Ops.
	var _ Ops = &MockOps{}
}
