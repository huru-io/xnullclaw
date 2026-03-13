package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// IsRunning checks whether a container with the given name is running.
func (c *Client) IsRunning(ctx context.Context, name string) (bool, error) {
	info, err := c.InspectContainer(ctx, name)
	if err != nil {
		// Container doesn't exist → not running.
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return info.State == "running", nil
}

// StartContainer creates and starts a container.
// If a stopped container with the same name exists, it is removed first
// so the new container picks up any image or config changes.
func (c *Client) StartContainer(ctx context.Context, name string, opts ContainerOpts) error {
	cfg, hostCfg := HardenedConfig(opts.AgentDir, opts.Image, opts.Cmd)

	if opts.ExposePort {
		WithPort(cfg, hostCfg)
	}
	if opts.TTY {
		WithTTY(cfg)
		WithNoRestart(hostCfg)
	}
	if len(opts.Env) > 0 {
		WithEnv(cfg, opts.Env)
	}

	// Remove any existing non-running container with this name.
	// Retries if the container is still transitioning (e.g. stopping).
	if info, err := c.InspectContainer(ctx, name); err == nil && info.State != "running" {
		_ = c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	}

	var resp container.CreateResponse
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = c.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{}, nil, name)
		if err == nil {
			break
		}
		// "name already in use" means the old container is still being removed.
		if strings.Contains(err.Error(), "already in use") {
			_ = c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}
		return fmt.Errorf("docker: create container %s: %w", name, err)
	}
	if err != nil {
		return fmt.Errorf("docker: create container %s: %w", name, err)
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		// Clean up the created container on start failure.
		_ = c.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("docker: start container %s: %w", name, err)
	}

	return nil
}

// StopContainer stops a running container with a 10-second timeout
// and waits for it to fully exit before returning.
func (c *Client) StopContainer(ctx context.Context, name string) error {
	timeout := 10
	err := c.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
	if err != nil {
		if isNotFound(err) {
			return nil // already gone
		}
		return fmt.Errorf("docker: stop container %s: %w", name, err)
	}

	// Wait for the container to fully exit (up to 15s beyond the stop timeout).
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	statusCh, errCh := c.cli.ContainerWait(waitCtx, name, container.WaitConditionNotRunning)
	select {
	case <-statusCh:
	case err := <-errCh:
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("docker: wait for stop %s: %w", name, err)
		}
	case <-waitCtx.Done():
		// Timed out waiting — force kill.
		_ = c.cli.ContainerKill(ctx, name, "KILL")
	}
	return nil
}

// RemoveContainer removes a container (optionally forced).
func (c *Client) RemoveContainer(ctx context.Context, name string, force bool) error {
	err := c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: force})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("docker: remove container %s: %w", name, err)
	}
	return nil
}

// MappedPort returns the host port mapped to the gateway container port.
// Returns 0 if no mapping exists (container not running or port not exposed).
func (c *Client) MappedPort(ctx context.Context, name string) (int, error) {
	raw, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("docker: inspect %s: %w", name, err)
	}
	bindings, ok := raw.NetworkSettings.Ports[gatewayPort]
	if !ok || len(bindings) == 0 {
		return 0, nil
	}
	hp := bindings[0].HostPort
	if hp == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(hp)
	if err != nil {
		return 0, fmt.Errorf("docker: parse host port %q for %s: %w", hp, name, err)
	}
	return port, nil
}

// InspectContainer returns info about a container.
func (c *Client) InspectContainer(ctx context.Context, name string) (*ContainerInfo, error) {
	raw, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("docker: inspect container %s: %w", name, err)
	}

	info := &ContainerInfo{
		Name:  strings.TrimPrefix(raw.Name, "/"),
		ID:    raw.ID[:12],
		Image: raw.Config.Image,
		State: raw.State.Status,
	}

	if raw.State.StartedAt != "" {
		info.StartedAt, _ = parseDockerTime(raw.State.StartedAt)
	}

	// Build human-readable status.
	switch raw.State.Status {
	case "running":
		info.Status = "running"
	case "exited":
		info.Status = fmt.Sprintf("exited (%d)", raw.State.ExitCode)
	default:
		info.Status = raw.State.Status
	}

	// Extract port mappings.
	for port, bindings := range raw.NetworkSettings.Ports {
		for _, b := range bindings {
			info.Ports = append(info.Ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, port))
		}
	}

	return info, nil
}

// ListContainers returns all containers whose name starts with the given prefix.
func (c *Client) ListContainers(ctx context.Context, prefix string) ([]ContainerInfo, error) {
	f := filters.NewArgs()
	f.Add("name", "^"+prefix)

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("docker: list containers: %w", err)
	}

	var result []ContainerInfo
	for _, ct := range containers {
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		result = append(result, ContainerInfo{
			Name:   name,
			ID:     ct.ID[:12],
			Image:  ct.Image,
			State:  ct.State,
			Status: ct.Status,
		})
	}
	return result, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No such container")
}

func parseDockerTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
