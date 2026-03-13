package tools

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
)

// maxHostFileSize limits how much of a host file we'll read into memory
// for copying into a container (50 MB — generous for docs, prevents OOM on huge files).
const maxHostFileSize = 50 << 20

func registerFileTools(r *Registry, d Deps) {
	// send_file_to_agent — copy host file into container + notify
	r.Register(
		Definition{
			Name:        "send_file_to_agent",
			Description: "Deliver a file to an agent's workspace and send a message about it. file_path must be within the mux media_tmp directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":     map[string]any{"type": "string", "description": "Agent name"},
					"file_path": map[string]any{"type": "string", "description": "Path to the file on the host (must be within mux media_tmp)"},
					"message":   map[string]any{"type": "string", "description": "Message to accompany the file"},
				},
				"required": []string{"agent", "file_path", "message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			if err := agent.ValidateName(agentName); err != nil {
				return "", err
			}
			filePath, err := stringArg(args, "file_path")
			if err != nil {
				return "", err
			}
			// Restrict source paths to the mux media_tmp directory (user uploads).
			allowedDir := filepath.Join(d.Home, "mux", "media_tmp")
			cleanPath := filepath.Clean(filePath)
			if !strings.HasPrefix(cleanPath, allowedDir+string(filepath.Separator)) {
				return "", fmt.Errorf("file_path must be within %s", allowedDir)
			}
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}

			cn := agent.ContainerName(d.Home, agentName)

			// Create inbox dir.
			if _, err := d.Docker.ExecSync(ctx, cn, []string{"mkdir", "-p", "/nullclaw-data/inbox"}, nil); err != nil {
				return "", fmt.Errorf("mkdir inbox: %w", err)
			}

			// Copy file into container via tar.
			if err := copyHostFileToContainer(ctx, d, cn, filePath, "/nullclaw-data/inbox/"); err != nil {
				return "", fmt.Errorf("copy file: %w", err)
			}

			// Notify the agent.
			notice := fmt.Sprintf("%s\n\nFile delivered to /nullclaw-data/inbox/", message)
			if _, err := sendToAgent(ctx, d, agentName, notice); err != nil {
				return fmt.Sprintf("File copied but notification failed: %v", err), nil
			}

			return fmt.Sprintf("File delivered to %s's inbox and agent notified.", agentName), nil
		},
	)

	// get_agent_file
	r.Register(
		Definition{
			Name:        "get_agent_file",
			Description: "Retrieve a file from an agent's workspace to the host media_tmp directory",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":     map[string]any{"type": "string", "description": "Agent name"},
					"path":      map[string]any{"type": "string", "description": "Path to the file within the container (must be under /nullclaw-data)"},
					"dest_path": map[string]any{"type": "string", "description": "Destination path on the host (must be within mux media_tmp)"},
				},
				"required": []string{"agent", "path", "dest_path"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			if err := agent.ValidateName(agentName); err != nil {
				return "", err
			}
			path, err := stringArg(args, "path")
			if err != nil {
				return "", err
			}
			// Restrict container path to /nullclaw-data.
			cleanContainerPath := filepath.Clean(path)
			if !strings.HasPrefix(cleanContainerPath, "/nullclaw-data/") && cleanContainerPath != "/nullclaw-data" {
				return "", fmt.Errorf("container path must be under /nullclaw-data")
			}
			destPath, err := stringArg(args, "dest_path")
			if err != nil {
				return "", err
			}
			// Restrict dest_path to the mux media_tmp directory.
			allowedDir := filepath.Join(d.Home, "mux", "media_tmp")
			cleanDest := filepath.Clean(destPath)
			if !strings.HasPrefix(cleanDest, allowedDir+string(filepath.Separator)) {
				return "", fmt.Errorf("dest_path must be within %s", allowedDir)
			}

			cn := agent.ContainerName(d.Home, agentName)
			if err := extractFileFromContainer(ctx, d, cn, path, cleanDest); err != nil {
				return "", fmt.Errorf("extract file: %w", err)
			}

			return fmt.Sprintf("File retrieved from %s:%s to %s", agentName, path, cleanDest), nil
		},
	)

	// list_agent_files
	r.Register(
		Definition{
			Name:        "list_agent_files",
			Description: "List files in an agent's workspace directory",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
					"path":  map[string]any{"type": "string", "description": "Directory path within the container (default: /nullclaw-data)"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			if err := agent.ValidateName(agentName); err != nil {
				return "", err
			}
			path := optionalStringArg(args, "path", "/nullclaw-data")
			// Restrict to /nullclaw-data to prevent listing host filesystem.
			cleanPath := filepath.Clean(path)
			if !strings.HasPrefix(cleanPath, "/nullclaw-data") {
				return "", fmt.Errorf("path must be under /nullclaw-data")
			}

			cn := agent.ContainerName(d.Home, agentName)
			out, err := d.Docker.ExecSync(ctx, cn, []string{"ls", "-la", cleanPath}, nil)
			if err != nil {
				return "", fmt.Errorf("ls failed: %w", err)
			}

			return strings.TrimSpace(out), nil
		},
	)
}

// copyHostFileToContainer reads a host file and copies it into the container via tar.
func copyHostFileToContainer(ctx context.Context, d Deps, containerName, hostPath, destDir string) error {
	// Check file size before reading into memory.
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("stat host file: %w", err)
	}
	if info.Size() > maxHostFileSize {
		return fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxHostFileSize)
	}
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read host file: %w", err)
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

	return d.Docker.CopyToContainer(ctx, containerName, destDir, &buf)
}

// extractFileFromContainer copies a file out of a container and writes it to destPath.
func extractFileFromContainer(ctx context.Context, d Deps, containerName, srcPath, destPath string) error {
	rc, err := d.Docker.CopyFromContainer(ctx, containerName, srcPath)
	if err != nil {
		return err
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		return fmt.Errorf("tar read: %w", err)
	}

	data, err := io.ReadAll(io.LimitReader(tr, maxHostFileSize))
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	return os.WriteFile(destPath, data, 0644)
}
