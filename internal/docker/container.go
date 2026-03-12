package docker

import (
	"context"
	"fmt"
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

	if opts.Port > 0 {
		WithPort(hostCfg, opts.Port)
	}
	if opts.TTY {
		WithTTY(cfg)
		WithNoRestart(hostCfg)
	}
	if len(opts.Env) > 0 {
		WithEnv(cfg, opts.Env)
	}

	// Remove any existing stopped container with this name.
	// This ensures we always create fresh from the current image/config.
	if info, err := c.InspectContainer(ctx, name); err == nil && info.State != "running" {
		_ = c.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	}

	resp, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{}, nil, name)
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

// StopContainer stops a running container with a 10-second timeout.
func (c *Client) StopContainer(ctx context.Context, name string) error {
	timeout := 10
	err := c.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
	if err != nil {
		if isNotFound(err) {
			return nil // already gone
		}
		return fmt.Errorf("docker: stop container %s: %w", name, err)
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
