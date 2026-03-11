package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
)

// ImageExists checks whether a Docker image exists locally.
func (c *Client) ImageExists(ctx context.Context, img string) (bool, error) {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		if isImageNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("docker: image inspect %s: %w", img, err)
	}
	return true, nil
}

// ImageInspect returns information about a local Docker image.
func (c *Client) ImageInspect(ctx context.Context, img string) (*ImageInfo, error) {
	raw, _, err := c.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		return nil, fmt.Errorf("docker: image inspect %s: %w", img, err)
	}

	created, _ := time.Parse(time.RFC3339Nano, raw.Created)
	return &ImageInfo{
		ID:      raw.ID[:19], // sha256:xxxx
		Tags:    raw.RepoTags,
		Size:    raw.Size,
		Created: created,
	}, nil
}

// ImageBuild builds a Docker image from a context directory.
func (c *Client) ImageBuild(ctx context.Context, contextDir string, opts BuildOpts) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(tarDir(contextDir, pw))
	}()

	buildOpts := types.ImageBuildOptions{
		Tags:    opts.Tags,
		NoCache: opts.NoCache,
		Remove:  true,
	}

	resp, err := c.cli.ImageBuild(ctx, pr, buildOpts)
	if err != nil {
		pr.Close()
		return fmt.Errorf("docker: build image: %w", err)
	}
	defer resp.Body.Close()

	// Stream build output to stderr for visibility.
	if _, err := io.Copy(os.Stderr, resp.Body); err != nil {
		return fmt.Errorf("docker: read build output: %w", err)
	}

	return nil
}

// ImageRemove removes a local Docker image.
func (c *Client) ImageRemove(ctx context.Context, img string) error {
	_, err := c.cli.ImageRemove(ctx, img, image.RemoveOptions{})
	if err != nil {
		return fmt.Errorf("docker: remove image %s: %w", img, err)
	}
	return nil
}

func isImageNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "No such image")
}

// tarDir writes a tar archive of the directory to w.
func tarDir(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}
