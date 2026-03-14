package docker

import (
	"os/user"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

// HardenedConfig returns container and host configs with full security hardening.
// This matches the bash wrapper's docker_hardened_args():
//   - read-only rootfs
//   - cap-drop ALL
//   - no-new-privileges
//   - tmpfs /tmp (noexec, 64MB)
//   - 128MB memory, 0.25 CPU, 64 PIDs
//   - host user (no root)
//   - data + config mounts
func HardenedConfig(agentDir, image string, cmd []string) (*container.Config, *container.HostConfig) {
	uid := currentUID()

	cfg := &container.Config{
		Image: image,
		Cmd:   cmd,
		User:  uid,
	}

	hostCfg := &container.HostConfig{
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},

		// Resource limits
		Resources: container.Resources{
			Memory:     128 * 1024 * 1024, // 128 MB
			MemorySwap: 128 * 1024 * 1024, // same as memory (no swap)
			NanoCPUs:   250000000,          // 0.25 CPU
			PidsLimit:  int64Ptr(64),
		},

		// tmpfs for /tmp
		Tmpfs: map[string]string{
			"/tmp": "size=64M,noexec,nosuid",
		},

		// Mounts: data volume + read-only config
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeBind,
				Source: filepath.Join(agentDir, "data"),
				Target: "/nullclaw-data",
				BindOptions: &mount.BindOptions{
					Propagation: mount.PropagationRPrivate,
				},
			},
			{
				Type:     mount.TypeBind,
				Source:   filepath.Join(agentDir, "config.json"),
				Target:   "/nullclaw-data/.nullclaw/config.json",
				ReadOnly: true,
			},
		},

		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
	}

	return cfg, hostCfg
}

// gatewayPort is the container port used by the nullclaw gateway.
const gatewayPort nat.Port = "3000/tcp"

// webChannelPort is the container port for the nullclaw web channel WebSocket.
const webChannelPort nat.Port = "32123/tcp"

// WebChannelPort is the numeric web channel port (used by bridge connections).
const WebChannelPort = 32123

// WithPort declares the exposed gateway and web channel ports.
// In local mode (networkName=""), it adds localhost-only dynamic host port
// mappings for both. In docker mode, it only declares the exposed ports —
// containers communicate via Docker network DNS.
func WithPort(cfg *container.Config, hostCfg *container.HostConfig, networkName string) {
	cfg.ExposedPorts = nat.PortSet{
		gatewayPort:    struct{}{},
		webChannelPort: struct{}{},
	}
	if networkName != "" {
		// Docker mode: containers use network DNS, no host port mapping.
		return
	}
	// Local mode: bind to localhost with dynamic port.
	hostCfg.PortBindings = nat.PortMap{
		gatewayPort: {
			{HostIP: "127.0.0.1", HostPort: ""},
		},
		webChannelPort: {
			{HostIP: "127.0.0.1", HostPort: ""},
		},
	}
}

// WithTTY sets interactive/TTY mode on the container config.
func WithTTY(cfg *container.Config) {
	cfg.Tty = true
	cfg.OpenStdin = true
	cfg.AttachStdin = true
	cfg.AttachStdout = true
	cfg.AttachStderr = true
}

// WithEnv adds environment variables to the container config.
func WithEnv(cfg *container.Config, env []string) {
	cfg.Env = append(cfg.Env, env...)
}

// WithNoRestart sets the restart policy to "no" (for one-shot/interactive containers).
func WithNoRestart(hostCfg *container.HostConfig) {
	hostCfg.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
}

func currentUID() string {
	u, err := user.Current()
	if err != nil {
		return "1000:1000"
	}
	return u.Uid + ":" + u.Gid
}

func int64Ptr(i int64) *int64 {
	return &i
}

// SecurityFlags returns the hardening flags as a human-readable list,
// useful for debugging and status output.
func SecurityFlags() []string {
	return []string{
		"read-only rootfs",
		"cap-drop ALL",
		"no-new-privileges",
		"tmpfs /tmp (noexec, 64MB)",
		"memory limit: 128MB",
		"CPU limit: 0.25",
		"PID limit: 64",
		"host user (no root)",
	}
}

