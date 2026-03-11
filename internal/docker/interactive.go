package docker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/api/types/container"
)

// AttachInteractive attaches stdin/stdout/stderr to a running container
// for interactive CLI use.
func (c *Client) AttachInteractive(ctx context.Context, name string, cmd []string) error {
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	execResp, err := c.cli.ContainerExecCreate(ctx, name, execCfg)
	if err != nil {
		return fmt.Errorf("docker: exec create in %s: %w", name, err)
	}

	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return fmt.Errorf("docker: exec attach in %s: %w", name, err)
	}
	defer attachResp.Close()

	// Pipe stdin → container.
	go func() {
		io.Copy(attachResp.Conn, os.Stdin)
		attachResp.CloseWrite()
	}()

	// Pipe container → stdout.
	io.Copy(os.Stdout, attachResp.Reader)

	return nil
}
