package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
)

// ContainerLogs returns a reader for a container's log output.
// The caller must close the returned ReadCloser.
func (c *Client) ContainerLogs(ctx context.Context, name string, opts LogOpts) (io.ReadCloser, error) {
	logOpts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
	}
	if opts.Tail != "" {
		logOpts.Tail = opts.Tail
	}
	if opts.Since != "" {
		logOpts.Since = opts.Since
	}

	rc, err := c.cli.ContainerLogs(ctx, name, logOpts)
	if err != nil {
		return nil, fmt.Errorf("docker: logs %s: %w", name, err)
	}
	return rc, nil
}
