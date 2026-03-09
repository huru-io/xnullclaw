// Package tools — files.go implements file management tools (save, list, send).
package tools

import (
	"context"
)

func registerFileTools(r *Registry, wrapperPath string) {
	// -----------------------------------------------------------------------
	// send_file_to_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "send_file_to_agent",
			Description: "Deliver a file to an agent's inbox and send a message about it",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":     map[string]any{"type": "string", "description": "Agent name"},
					"file_path": map[string]any{"type": "string", "description": "Path to the file to send"},
					"message":   map[string]any{"type": "string", "description": "Message to accompany the file"},
				},
				"required": []string{"agent", "file_path", "message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			// Phase 1 stub: send-file not yet in wrapper.
			return "send_file not yet implemented", nil
		},
	)

	// -----------------------------------------------------------------------
	// get_agent_file
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_agent_file",
			Description: "Retrieve a file from an agent's workspace",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
					"path":  map[string]any{"type": "string", "description": "Path to the file within the agent's workspace"},
				},
				"required": []string{"agent", "path"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			// Phase 1 stub: get_file not yet in wrapper.
			return "get_file not yet implemented", nil
		},
	)

	// -----------------------------------------------------------------------
	// list_agent_files
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "list_agent_files",
			Description: "List files in an agent's workspace directory",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
					"path":  map[string]any{"type": "string", "description": "Directory path within the agent's workspace"},
				},
				"required": []string{"agent", "path"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			// Phase 1 stub: ls not yet in wrapper.
			return "list_agent_files not yet implemented", nil
		},
	)
}
