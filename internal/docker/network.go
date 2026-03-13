package docker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// EnsureNetwork creates a Docker bridge network if it doesn't already exist.
// Idempotent — returns nil if the network already exists.
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	_, err := c.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil // already exists
	}
	if !errdefs.IsNotFound(err) {
		return fmt.Errorf("docker: inspect network %s: %w", name, err)
	}

	_, err = c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		// Handle TOCTOU race: another process may have created the network
		// between our inspect and create calls. Treat conflict as success.
		if errdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("docker: create network %s: %w", name, err)
	}
	return nil
}

// ConnectNetwork attaches a container to a Docker network.
// Idempotent — returns nil if the container is already connected.
func (c *Client) ConnectNetwork(ctx context.Context, networkName, containerID string) error {
	err := c.cli.NetworkConnect(ctx, networkName, containerID, nil)
	if err != nil {
		// "already exists" or "endpoint already joined" → idempotent success.
		msg := err.Error()
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "already connected") {
			return nil
		}
		return fmt.Errorf("docker: connect %s to network %s: %w", containerID, networkName, err)
	}
	return nil
}

// SelfContainerID returns the container ID of the current process,
// or empty string if not running inside a Docker container.
// Uses /proc/self/mountinfo which is available in all Linux containers.
func SelfContainerID() string {
	// Method 1: Docker sets the hostname to the container ID (short form).
	hostname, err := os.Hostname()
	if err == nil && len(hostname) == 12 && isHex(hostname) {
		return hostname
	}

	// Method 2: Check /.dockerenv — present in Docker containers.
	// The hostname might be overridden, but the container ID is in /proc/self/cgroup.
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return "" // not in a container
	}

	// Try /proc/self/cgroup first (works on cgroups v1 and some v2 setups).
	if id := containerIDFromCgroup(); id != "" {
		return id
	}

	// Fallback: /proc/self/mountinfo (cgroups v2 on modern kernels like Ubuntu 22.04+).
	// On cgroups v2, /proc/self/cgroup just contains "0::/" but mountinfo has:
	// "... /docker/containers/<id>/... ..."
	if id := containerIDFromMountinfo(); id != "" {
		return id
	}

	return ""
}

// containerIDFromCgroup extracts a container ID from /proc/self/cgroup.
func containerIDFromCgroup() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// cgroup v1: "N:name:/docker/<id>"
		// cgroup v2: "0::/docker/<id>" or "0::/system.slice/docker-<id>.scope"
		if idx := strings.LastIndex(line, "/docker/"); idx != -1 {
			id := line[idx+len("/docker/"):]
			if len(id) >= 12 && isHex(id[:12]) {
				return id[:12]
			}
		}
		if idx := strings.Index(line, "/docker-"); idx != -1 {
			rest := line[idx+len("/docker-"):]
			if dotIdx := strings.Index(rest, "."); dotIdx >= 12 && isHex(rest[:12]) {
				return rest[:12]
			}
		}
	}
	return ""
}

// containerIDFromMountinfo extracts a container ID from /proc/self/mountinfo.
// Looks for "/docker/containers/<hex-id>/..." and returns the first 12 chars.
func containerIDFromMountinfo() string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	const marker = "/docker/containers/"
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, marker); idx != -1 {
			rest := line[idx+len(marker):]
			// Full container ID is 64 hex chars; we extract the first 12 (short form).
			if slashIdx := strings.Index(rest, "/"); slashIdx >= 12 && isHex(rest[:12]) {
				return rest[:12]
			}
		}
	}
	return ""
}

// isHex reports whether s is a non-empty lowercase hex string.
// Docker container IDs are always lowercase hex, so we reject uppercase
// to avoid false positives from hostnames or other identifiers.
func isHex(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
