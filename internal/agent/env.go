package agent

import "github.com/jotavich/xnullclaw/internal/docker"

// ContainerCmd is the command passed to nullclaw inside the container.
const ContainerCmd = "gateway"

// ContainerEnv reads environment variables from the agent's config
// that need to be passed into the container at start time.
// The mappings are driven by ConfigKey.EnvVar — any config key with
// a non-empty EnvVar field is automatically included.
func ContainerEnv(agentDir string) []string {
	var env []string
	for _, ck := range ConfigKeys {
		if ck.EnvVar == "" {
			continue
		}
		if val, err := ConfigGet(agentDir, ck.Name); err == nil {
			if s, ok := val.(string); ok && s != "" {
				env = append(env, ck.EnvVar+"="+s)
			}
		}
	}
	return env
}

// StartOpts returns ContainerOpts for launching an agent container.
// exposePort enables the gateway HTTP port (Docker auto-assigns host port).
// networkName attaches the container to a Docker network (empty = default bridge).
func StartOpts(image, home, name string, exposePort bool, networkName string) docker.ContainerOpts {
	agentDir := Dir(home, name)

	// For Docker bind mounts, use host-side path if available (DooD mode).
	// The Docker daemon resolves mount source paths on the HOST filesystem,
	// not inside the mux container. XNC_HOST_HOME provides the host path.
	// On bare metal (no XNC_HOST_HOME), mountDir == agentDir.
	mountDir := agentDir
	if hostHome := HostHome(); hostHome != "" {
		mountDir = Dir(hostHome, name)
	}

	return docker.ContainerOpts{
		Image:       image,
		Cmd:         []string{ContainerCmd},
		AgentName:   CanonicalName(name),
		AgentDir:    mountDir,
		ExposePort:  exposePort,
		Env:         ContainerEnv(agentDir), // read config from container-local path
		NetworkName: networkName,
	}
}
