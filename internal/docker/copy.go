package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
)

// CopyToContainer copies content (a tar archive) into the container at destPath.
func (c *Client) CopyToContainer(ctx context.Context, name, destPath string, content io.Reader) error {
	err := c.cli.CopyToContainer(ctx, name, destPath, content, container.CopyToContainerOptions{})
	if err != nil {
		return fmt.Errorf("docker: copy to %s:%s: %w", name, destPath, err)
	}
	return nil
}

// CopyFromContainer copies a file from the container and returns a reader.
// The caller must close the returned ReadCloser.
func (c *Client) CopyFromContainer(ctx context.Context, name, srcPath string) (io.ReadCloser, error) {
	rc, _, err := c.cli.CopyFromContainer(ctx, name, srcPath)
	if err != nil {
		return nil, fmt.Errorf("docker: copy from %s:%s: %w", name, srcPath, err)
	}
	return rc, nil
}

// CopyFileToContainer is a convenience function that copies a single file
// (given as bytes) into the container at the specified directory.
func (c *Client) CopyFileToContainer(ctx context.Context, name, destDir, fileName string, data []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name: fileName,
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("docker: tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("docker: tar write: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("docker: tar close: %w", err)
	}

	return c.CopyToContainer(ctx, name, destDir, &buf)
}

// ExtractFileFromContainer copies a file out of a container and returns its contents.
func (c *Client) ExtractFileFromContainer(ctx context.Context, name, srcPath string) ([]byte, error) {
	rc, err := c.CopyFromContainer(ctx, name, srcPath)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// Docker returns a tar archive — extract the first file.
	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return nil, fmt.Errorf("docker: tar read from %s:%s: %w", name, srcPath, err)
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("docker: read file %s:%s: %w", name, srcPath, err)
	}
	return data, nil
}

// CopyHostFileToContainer copies a file from the host filesystem into a container.
func CopyHostFileToContainer(ctx context.Context, ops Ops, hostPath, containerName, destDir string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read host file %s: %w", hostPath, err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filepath.Base(hostPath),
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}

	return ops.CopyToContainer(ctx, containerName, destDir, &buf)
}
