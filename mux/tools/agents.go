// Package tools — agents.go implements agent communication and lifecycle tools.
package tools

import (
	"context"
	"fmt"
	"strings"
)

func registerAgentTools(r *Registry, wrapperPath string) {
	// -----------------------------------------------------------------------
	// send_to_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "send_to_agent",
			Description: "Send a message to a specific agent and return its response",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":   map[string]any{"type": "string", "description": "Agent name"},
					"message": map[string]any{"type": "string", "description": "Message to send"},
				},
				"required": []string{"agent", "message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}
			return runWrapperWithStdin(ctx, wrapperPath, message, agent, "send")
		},
	)

	// -----------------------------------------------------------------------
	// send_to_all
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "send_to_all",
			Description: "Send a message to ALL running agents in parallel and return all responses",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string", "description": "Message to send to all agents"},
				},
				"required": []string{"message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}
			return runWrapperWithStdin(ctx, wrapperPath, message, "send-all")
		},
	)

	// -----------------------------------------------------------------------
	// send_to_some
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "send_to_some",
			Description: "Send a message to a named subset of agents in parallel",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agents":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of agent names"},
					"message": map[string]any{"type": "string", "description": "Message to send"},
				},
				"required": []string{"agents", "message"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agents, err := stringSliceArg(args, "agents")
			if err != nil {
				return "", err
			}
			if len(agents) == 0 {
				return "", fmt.Errorf("agents list must not be empty")
			}
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}
			agentList := strings.Join(agents, ",")
			return runWrapperWithStdin(ctx, wrapperPath, message, "send-some", agentList)
		},
	)

	// -----------------------------------------------------------------------
	// list_agents
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "list_agents",
			Description: "List all agents and their current status",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			return runWrapper(ctx, wrapperPath, "list", "--json")
		},
	)

	// -----------------------------------------------------------------------
	// agent_status
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "agent_status",
			Description: "Get detailed status of a specific agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "status", "--json")
		},
	)

	// -----------------------------------------------------------------------
	// start_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "start_agent",
			Description: "Start an agent via xnullclaw (in mux mode)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "start", "--mux")
		},
	)

	// -----------------------------------------------------------------------
	// stop_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "stop_agent",
			Description: "Stop an agent via xnullclaw",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "stop")
		},
	)

	// -----------------------------------------------------------------------
	// restart_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "restart_agent",
			Description: "Restart an agent via xnullclaw",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "restart")
		},
	)

	// -----------------------------------------------------------------------
	// setup_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "setup_agent",
			Description: "Create a new agent with default config",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name to create"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "setup")
		},
	)

	// -----------------------------------------------------------------------
	// clone_agent
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "clone_agent",
			Description: "Clone an agent from another existing agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":  map[string]any{"type": "string", "description": "New agent name"},
					"source": map[string]any{"type": "string", "description": "Source agent to clone from"},
				},
				"required": []string{"agent", "source"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			source, err := stringArg(args, "source")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "clone", source)
		},
	)

	// -----------------------------------------------------------------------
	// get_agent_config
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_agent_config",
			Description: "Read an agent's current configuration",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "config", "get")
		},
	)

	// -----------------------------------------------------------------------
	// update_agent_config
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "update_agent_config",
			Description: "Modify an agent's config. Requires restart to take effect.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
					"key":   map[string]any{"type": "string", "description": "Config key to update"},
					"value": map[string]any{"type": "string", "description": "New value"},
				},
				"required": []string{"agent", "key", "value"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			key, err := stringArg(args, "key")
			if err != nil {
				return "", err
			}
			value, err := stringArg(args, "value")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "config", "set", key, value)
		},
	)
}
