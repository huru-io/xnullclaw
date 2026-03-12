package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecSync runs a command inside a container and returns stdout+stderr.
// If stdin is non-nil, it is piped to the command's stdin.
func (c *Client) ExecSync(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  stdin != nil,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return "", fmt.Errorf("docker: exec create in %s: %w", name, err)
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("docker: exec attach in %s: %w", name, err)
	}
	defer attachResp.Close()

	// Write stdin if provided.
	if stdin != nil {
		go func() {
			io.Copy(attachResp.Conn, stdin)
			attachResp.CloseWrite()
		}()
	}

	// Demultiplex stdout/stderr (Docker multiplexes when TTY=false).
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader); err != nil {
		return "", fmt.Errorf("docker: exec read in %s: %w", name, err)
	}

	// Combine stdout + stderr for the result.
	combined := stdout.String()
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += stderr.String()
	}

	// Check exit code.
	inspect, err := c.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return combined, fmt.Errorf("docker: exec inspect in %s: %w", name, err)
	}
	if inspect.ExitCode != 0 {
		return combined, fmt.Errorf("docker: exec in %s: exit code %d", name, inspect.ExitCode)
	}

	return combined, nil
}
