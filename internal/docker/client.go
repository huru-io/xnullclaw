package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
)

// Client wraps the Docker SDK client and implements the Ops interface.
type Client struct {
	cli *client.Client
}

// NewClient creates a Docker client from the default environment.
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker: create client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close closes the Docker client connection.
func (c *Client) Close() error {
	return c.cli.Close()
}

// Ping verifies the Docker daemon is reachable.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker: daemon not reachable: %w", err)
	}
	return nil
}
