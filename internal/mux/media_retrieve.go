// media_retrieve.go — retrieves container files to the host for Telegram delivery.
package mux

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/media"
)

// maxRetrieveSize limits files retrieved from containers to 50MB.
const maxRetrieveSize = 50 << 20

// retrieveContainerFile copies a file from a container to the mux media_tmp
// directory and returns the host path. Used by bridge and drainer to resolve
// container paths in media markers before sending to Telegram.
func retrieveContainerFile(ctx context.Context, dk docker.Ops, home, agentName, containerPath, mediaTmpDir string) (string, error) {
	// Validate container path.
	clean := filepath.Clean(containerPath)
	if !strings.HasPrefix(clean, "/nullclaw-data/") {
		return "", fmt.Errorf("retrieve: path must be under /nullclaw-data: %s", containerPath)
	}

	cn := agent.ContainerName(home, agentName)

	rc, err := dk.CopyFromContainer(ctx, cn, clean)
	if err != nil {
		return "", fmt.Errorf("retrieve from %s: %w", agentName, err)
	}
	defer rc.Close()

	// Docker/K8s returns a tar archive — extract the first file.
	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil {
		return "", fmt.Errorf("retrieve tar header: %w", err)
	}

	if hdr.Size > maxRetrieveSize {
		return "", fmt.Errorf("retrieve: file too large (%d bytes, max %d)", hdr.Size, maxRetrieveSize)
	}

	// Write to media_tmp with unique name to prevent collisions.
	base := filepath.Base(clean)
	destPath := filepath.Join(mediaTmpDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), base))

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("retrieve create: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, io.LimitReader(tr, maxRetrieveSize+1)); err != nil {
		os.Remove(destPath)
		return "", fmt.Errorf("retrieve write: %w", err)
	}

	return destPath, nil
}

// resolveContainerAttachments replaces container paths in media attachments
// with host paths by retrieving files from the container.
// Attachments that fail retrieval are logged and removed from the slice.
func resolveContainerAttachments(ctx context.Context, dk docker.Ops, home, agentName, mediaTmpDir string, attachments []media.Attachment, logErr func(string, ...any)) []media.Attachment {
	resolved := make([]media.Attachment, 0, len(attachments))
	for _, att := range attachments {
		if !strings.HasPrefix(att.Path, "/nullclaw-data/") {
			resolved = append(resolved, att)
			continue
		}
		hostPath, err := retrieveContainerFile(ctx, dk, home, agentName, att.Path, mediaTmpDir)
		if err != nil {
			logErr("retrieve attachment failed", "path", att.Path, "agent", agentName, "error", err)
			continue
		}
		att.Path = hostPath
		resolved = append(resolved, att)
	}
	return resolved
}
