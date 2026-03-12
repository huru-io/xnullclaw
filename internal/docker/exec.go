package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// execFireWriteTimeout caps how long the stdin pipe goroutine can block.
// Messages are small, so 30s is generous — prevents leaked goroutines
// if a container's stdin buffer is full or the connection is dead.
const execFireWriteTimeout = 30 * time.Second

// ExecFire runs a command inside a container, delivers stdin, and returns
// immediately without waiting for the command to finish or reading its output.
// The command continues running inside the container after this returns.
func (c *Client) ExecFire(ctx context.Context, name string, cmd []string, stdin io.Reader) error {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  stdin != nil,
		AttachStdout: false,
		AttachStderr: false,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return fmt.Errorf("docker: exec fire in %s: %w", name, err)
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("docker: exec fire attach in %s: %w", name, err)
	}

	if stdin != nil {
		go func() {
			attachResp.Conn.SetWriteDeadline(time.Now().Add(execFireWriteTimeout))
			io.Copy(attachResp.Conn, stdin)
			attachResp.CloseWrite()
			attachResp.Close()
		}()
	} else {
		attachResp.Close()
	}

	return nil
}

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

	// Write stdin if provided (with deadline to prevent goroutine leaks).
	if stdin != nil {
		go func() {
			attachResp.Conn.SetWriteDeadline(time.Now().Add(execFireWriteTimeout))
			io.Copy(attachResp.Conn, stdin)
			attachResp.CloseWrite()
		}()
	}

	// Demultiplex stdout/stderr (Docker multiplexes when TTY=false).
	// Cap at 2MB to prevent a runaway command from exhausting host memory.
	const maxExecOutput = 2 << 20 // 2 MB
	var stdout, stderr bytes.Buffer
	limited := io.LimitReader(attachResp.Reader, maxExecOutput)
	if _, err := stdcopy.StdCopy(&stdout, &stderr, limited); err != nil {
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
