package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func registerSkillTools(r *Registry, d Deps) {
	// list_skills — list installed skills for an agent or shared
	r.Register(
		Definition{
			Name:        "list_skills",
			Description: "List installed skills for an agent or the shared skill repository",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{
						"type":        "string",
						"description": "Agent name. If empty, lists shared skills.",
					},
				},
				"required": []string{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName := optionalStringArg(args, "agent", "")

			var dir string
			var scope string
			if agentName != "" {
				if !agent.Exists(d.Home, agentName) {
					return "", fmt.Errorf("agent %q does not exist", agentName)
				}
				dir = agent.AgentSkillsDir(d.Home, agentName)
				scope = agentName
			} else {
				dir = agent.SharedSkillsDir(d.Home)
				scope = "shared"
			}

			skills, err := agent.ListSkills(dir)
			if err != nil {
				return "", fmt.Errorf("list skills: %w", err)
			}

			if len(skills) == 0 {
				return fmt.Sprintf("No skills installed (%s).", scope), nil
			}

			var lines []string
			lines = append(lines, fmt.Sprintf("Skills (%s): %d", scope, len(skills)))
			for _, s := range skills {
				line := "  " + s.Name
				if s.Version != "" {
					line += " v" + s.Version
				}
				if s.Description != "" {
					line += " — " + s.Description
				}
				lines = append(lines, line)
			}
			return strings.Join(lines, "\n"), nil
		},
	)

	// install_skill — install a skill from a local file path
	r.Register(
		Definition{
			Name: "install_skill",
			Description: "Install a skill from a local file path (directory, .zip, or .md file) within the xnc home directory. " +
				"Use scope 'shared' to install for all agents, or specify an agent name. Source path must be under XNC_HOME.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "Path to the skill source (directory, .zip file, or .md file)",
					},
					"agent": map[string]any{
						"type":        "string",
						"description": "Target agent name. If empty, installs to shared repository.",
					},
					"sync_to_agents": map[string]any{
						"type":        "boolean",
						"description": "When installing to shared, also copy to all existing agents (default: true)",
					},
				},
				"required": []string{"source"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			source, err := stringArg(args, "source")
			if err != nil {
				return "", err
			}

			// Security: restrict source to paths under xnc home.
			// Use EvalSymlinks to resolve symlinks and prevent symlink-based escapes.
			absSource, err := filepath.EvalSymlinks(source)
			if err != nil {
				return "", fmt.Errorf("invalid source path: %w", err)
			}
			absHome, err := filepath.EvalSymlinks(d.Home)
			if err != nil {
				return "", fmt.Errorf("invalid home path: %w", err)
			}
			if !strings.HasPrefix(absSource, absHome+"/") {
				return "", fmt.Errorf("install_skill: source path must be within xnc home (%s)", d.Home)
			}

			agentName := optionalStringArg(args, "agent", "")
			sync := true
			if v, ok := args["sync_to_agents"]; ok {
				if b, ok := v.(bool); ok {
					sync = b
				}
			}

			if agentName != "" {
				// Install to specific agent.
				if !agent.Exists(d.Home, agentName) {
					return "", fmt.Errorf("agent %q does not exist", agentName)
				}
				dir := agent.AgentSkillsDir(d.Home, agentName)
				name, err := agent.InstallSkill(dir, source)
				if err != nil {
					return "", fmt.Errorf("install skill: %w", err)
				}
				return fmt.Sprintf("Installed skill %q for agent %s.", name, agentName), nil
			}

			// Install to shared.
			sharedDir := agent.SharedSkillsDir(d.Home)
			name, err := agent.InstallSkill(sharedDir, source)
			if err != nil {
				return "", fmt.Errorf("install skill: %w", err)
			}

			result := fmt.Sprintf("Installed skill %q to shared repository.", name)

			if sync {
				count, err := agent.SyncSharedToAgents(d.Home, name)
				if err != nil {
					result += fmt.Sprintf(" Warning: sync to agents failed: %v", err)
				} else if count > 0 {
					result += fmt.Sprintf(" Synced to %d agent(s).", count)
				}
			}

			return result, nil
		},
	)

	// remove_skill — remove a skill
	r.Register(
		Definition{
			Name:        "remove_skill",
			Description: "Remove a skill from an agent or the shared repository",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Skill name to remove",
					},
					"agent": map[string]any{
						"type":        "string",
						"description": "Agent name. If empty, removes from shared repository.",
					},
					"remove_from_agents": map[string]any{
						"type":        "boolean",
						"description": "When removing from shared, also remove from all agents (default: false)",
					},
				},
				"required": []string{"name"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			skillName, err := stringArg(args, "name")
			if err != nil {
				return "", err
			}
			agentName := optionalStringArg(args, "agent", "")
			removeAll := false
			if v, ok := args["remove_from_agents"]; ok {
				if b, ok := v.(bool); ok {
					removeAll = b
				}
			}

			if agentName != "" {
				if !agent.Exists(d.Home, agentName) {
					return "", fmt.Errorf("agent %q does not exist", agentName)
				}
				dir := agent.AgentSkillsDir(d.Home, agentName)
				if err := agent.RemoveSkill(dir, skillName); err != nil {
					return "", err
				}
				return fmt.Sprintf("Removed skill %q from %s.", skillName, agentName), nil
			}

			// Remove from shared.
			sharedDir := agent.SharedSkillsDir(d.Home)
			if err := agent.RemoveSkill(sharedDir, skillName); err != nil {
				return "", err
			}

			result := fmt.Sprintf("Removed skill %q from shared repository.", skillName)

			if removeAll {
				count, err := agent.RemoveSkillFromAllAgents(d.Home, skillName)
				if err != nil {
					result += fmt.Sprintf(" Warning: removal from agents failed: %v", err)
				} else if count > 0 {
					result += fmt.Sprintf(" Also removed from %d agent(s).", count)
				}
			}

			return result, nil
		},
	)
}
