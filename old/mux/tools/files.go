// Package tools — files.go implements file management tools (send, get, list).
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const containerPrefix = "xnc"

func containerName(agent string) string {
	return containerPrefix + "-" + agent
}

func registerFileTools(r *Registry, wrapperPath string) {
	// -----------------------------------------------------------------------
	// send_file_to_agent — docker cp host file into container, notify agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "send_file_to_agent",
			Description: "Deliver a file to an agent's workspace and send a message about it",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":     map[string]any{"type": "string", "description": "Agent name"},
					"file_path": map[string]any{"type": "string", "description": "Path to the file on the host"},
					"message":   map[string]any{"type": "string", "description": "Message to accompany the file"},
				},
				"required": []string{"agent", "file_path", "message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			filePath, err := stringArg(args, "file_path")
			if err != nil {
				return "", err
			}
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}

			cname := containerName(agent)

			// Copy file into container's /nullclaw-data/inbox/ directory.
			// Create inbox dir first.
			mkdirCmd := exec.CommandContext(ctx, "docker", "exec", cname, "mkdir", "-p", "/nullclaw-data/inbox")
			if out, err := mkdirCmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("mkdir inbox: %s: %s", err, string(out))
			}

			// docker cp hostfile container:/nullclaw-data/inbox/
			cpCmd := exec.CommandContext(ctx, "docker", "cp", filePath, cname+":/nullclaw-data/inbox/")
			if out, err := cpCmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("docker cp: %s: %s", err, string(out))
			}

			// Notify the agent about the file via send.
			notice := fmt.Sprintf("%s\n\nFile delivered to /nullclaw-data/inbox/", message)
			_, err = runWrapperWithStdin(ctx, wrapperPath, notice, agent, "send")
			if err != nil {
				return fmt.Sprintf("File copied but notification failed: %v", err), nil
			}

			return fmt.Sprintf("File delivered to %s's inbox and agent notified.", agent), nil
		},
	)

	// -----------------------------------------------------------------------
	// get_agent_file — docker cp file out of container to host temp dir
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_agent_file",
			Description: "Retrieve a file from an agent's workspace to the host",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":     map[string]any{"type": "string", "description": "Agent name"},
					"path":      map[string]any{"type": "string", "description": "Path to the file within the container (e.g. /nullclaw-data/output.png)"},
					"dest_path": map[string]any{"type": "string", "description": "Destination path on the host"},
				},
				"required": []string{"agent", "path", "dest_path"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			path, err := stringArg(args, "path")
			if err != nil {
				return "", err
			}
			destPath, err := stringArg(args, "dest_path")
			if err != nil {
				return "", err
			}

			cname := containerName(agent)

			// docker cp container:/path destPath
			cpCmd := exec.CommandContext(ctx, "docker", "cp", cname+":"+path, destPath)
			if out, err := cpCmd.CombinedOutput(); err != nil {
				return "", fmt.Errorf("docker cp: %s: %s", err, string(out))
			}

			return fmt.Sprintf("File retrieved from %s:%s to %s", agent, path, destPath), nil
		},
	)

	// -----------------------------------------------------------------------
	// list_agent_files — docker exec ls inside container
	// -----------------------------------------------------------------------
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
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			path := optionalStringArg(args, "path", "/nullclaw-data")

			cname := containerName(agent)

			lsCmd := exec.CommandContext(ctx, "docker", "exec", cname, "ls", "-la", path)
			out, err := lsCmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("ls failed: %s: %s", err, strings.TrimSpace(string(out)))
			}

			return strings.TrimSpace(string(out)), nil
		},
	)
}
