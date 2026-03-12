package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// namePool is a curated set of short, pronounceable, phonetically distinct names.
var namePool = []string{
	"Arlo", "Bex", "Clio", "Dax", "Echo",
	"Faye", "Gale", "Haze", "Iris", "Juno",
	"Knox", "Luna", "Mira", "Nyx", "Opal",
	"Pike", "Quinn", "Rune", "Sage", "Taro",
	"Uma", "Vale", "Wren", "Xyla", "Yuki", "Zara",
}

func pickAgentName(d Deps) string {
	existing := make(map[string]bool)
	agents, _ := agent.ListAll(d.Home)
	for _, a := range agents {
		existing[strings.ToLower(a.Name)] = true
	}

	perm := rand.Perm(len(namePool))
	for _, i := range perm {
		name := namePool[i]
		lower := strings.ToLower(name)
		if !existing[lower] && !strings.EqualFold(name, d.Cfg.Persona.Name) {
			return name
		}
	}

	for i := 1; ; i++ {
		name := fmt.Sprintf("Agent-%d", i)
		if !existing[strings.ToLower(name)] {
			return name
		}
	}
}

func isReservedName(cfg *config.Config, name string) bool {
	return strings.EqualFold(name, cfg.Persona.Name)
}

type personaVariant struct {
	Trait          string
	Warmth         float64
	Humor          float64
	Verbosity      float64
	Proactiveness  float64
	Formality      float64
	Empathy        float64
	Sarcasm        float64
	Autonomy       float64
	Interpretation float64
	Creativity     float64
}

var personaVariants = []personaVariant{
	{"friendly and straightforward", 0.7, 0.4, 0.4, 0.6, 0.3, 0.6, 0.1, 0.5, 0.2, 0.4},
	{"precise and analytical", 0.4, 0.2, 0.5, 0.5, 0.6, 0.3, 0.0, 0.4, 0.1, 0.3},
	{"warm and creative", 0.8, 0.5, 0.4, 0.7, 0.3, 0.7, 0.1, 0.6, 0.3, 0.8},
	{"calm and thorough", 0.5, 0.2, 0.7, 0.5, 0.5, 0.5, 0.0, 0.4, 0.2, 0.4},
	{"witty and concise", 0.5, 0.7, 0.2, 0.6, 0.4, 0.4, 0.3, 0.6, 0.3, 0.6},
	{"earnest and helpful", 0.7, 0.3, 0.5, 0.8, 0.5, 0.7, 0.0, 0.7, 0.2, 0.4},
	{"playful and inventive", 0.6, 0.6, 0.3, 0.7, 0.2, 0.5, 0.2, 0.7, 0.4, 0.9},
	{"methodical and reliable", 0.4, 0.2, 0.6, 0.5, 0.7, 0.4, 0.0, 0.4, 0.1, 0.3},
	{"curious and adaptable", 0.6, 0.4, 0.4, 0.6, 0.4, 0.5, 0.1, 0.6, 0.3, 0.7},
	{"direct and efficient", 0.3, 0.2, 0.2, 0.5, 0.5, 0.3, 0.1, 0.5, 0.1, 0.3},
}

func buildAgentSystemPrompt(name string, v personaVariant) string {
	values := []float64{
		v.Warmth, v.Humor, v.Verbosity, v.Proactiveness, v.Formality,
		v.Empathy, v.Sarcasm, v.Autonomy, v.Interpretation, v.Creativity,
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("You are %s, an AI assistant.", name))
	lines = append(lines, fmt.Sprintf("Your personality: %s.", v.Trait))
	lines = append(lines, "")
	lines = append(lines, "Communication style:")
	for i, desc := range config.DimensionDescriptors {
		lines = append(lines, "- "+config.PickDescription(values[i], desc))
	}
	return strings.Join(lines, "\n")
}

// sendToAgent sends a message to an agent asynchronously (fire-and-forget).
// The agent's response is written to its .outbox/ directory and picked up
// by the drain goroutine, which sends it directly to Telegram.
func sendToAgent(ctx context.Context, d Deps, agentName, message string) (string, error) {
	cn := agent.ContainerName(d.Home, agentName)
	// The shell command:
	// 1. Acquires the send lock (serializes concurrent sends)
	// 2. Creates the .outbox directory if needed
	// 3. Writes the agent response to a .pending file
	// 4. Renames to .msg atomically (only complete responses are drained)
	cmd := []string{
		"sh", "-c",
		`flock /tmp/.send.lock sh -c '
			mkdir -p /nullclaw-data/.outbox
			f=$(mktemp /nullclaw-data/.outbox/XXXXXXXXXX.pending)
			nullclaw agent -s mux > "$f" 2>&1
			mv "$f" "${f%.pending}.msg"
		'`,
	}
	if err := d.Docker.ExecFire(ctx, cn, cmd, strings.NewReader(message)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Message delivered to %s. Response will appear shortly.", agentName), nil
}

// startOpts returns ContainerOpts for starting an agent.
func startOpts(d Deps, name string, port int) docker.ContainerOpts {
	return agent.StartOpts(d.Image, d.Home, name, port)
}

// validatedAgentArg extracts and validates the "agent" argument from tool args.
func validatedAgentArg(args map[string]any) (string, error) {
	name, err := stringArg(args, "agent")
	if err != nil {
		return "", err
	}
	if err := agent.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func registerAgentTools(r *Registry, d Deps) {
	// send_to_agent
	r.Register(
		Definition{
			Name:        "send_to_agent",
			Description: "Send a message to a specific agent (async — response is delivered to Telegram automatically)",
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
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			message, err := stringArg(args, "message")
			if err != nil {
				return "", err
			}
			return sendToAgent(ctx, d, agentName, message)
		},
	)

	// send_to_all
	r.Register(
		Definition{
			Name:        "send_to_all",
			Description: "Send a message to ALL running agents in parallel (async — responses delivered to Telegram automatically)",
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

			prefix := agent.ContainerPrefix(d.Home)
			containers, err := d.Docker.ListContainers(ctx, prefix)
			if err != nil {
				return "", fmt.Errorf("list containers: %w", err)
			}

			var names []string
			for _, c := range containers {
				if c.State == "running" {
					n := strings.TrimPrefix(c.Name, prefix)
					names = append(names, n)
				}
			}

			if len(names) == 0 {
				return "No running agents found.", nil
			}

			return sendToMultiple(ctx, d, names, message)
		},
	)

	// send_to_some
	r.Register(
		Definition{
			Name:        "send_to_some",
			Description: "Send a message to a named subset of agents in parallel (async — responses delivered to Telegram automatically)",
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
			return sendToMultiple(ctx, d, agents, message)
		},
	)

	// list_agents
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
			agents, err := agent.ListAll(d.Home)
			if err != nil {
				return "", err
			}
			data, err := json.MarshalIndent(agents, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// agent_status
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
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			cn := agent.ContainerName(d.Home, agentName)
			info, err := d.Docker.InspectContainer(ctx, cn)
			if err != nil {
				return "", err
			}
			data, err := json.MarshalIndent(info, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// start_agent
	r.Register(
		Definition{
			Name:        "start_agent",
			Description: "Start an agent in mux mode",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			if !agent.HasProviderKey(d.Home, agentName) {
				return "", fmt.Errorf("agent %q has no API key configured", agentName)
			}
			cn := agent.ContainerName(d.Home, agentName)
			opts := startOpts(d, agentName, 0)
			if err := d.Docker.StartContainer(ctx, cn, opts); err != nil {
				return "", err
			}
			return fmt.Sprintf("Agent %s started in mux mode", agentName), nil
		},
	)

	// stop_agent
	r.Register(
		Definition{
			Name:        "stop_agent",
			Description: "Stop an agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			cn := agent.ContainerName(d.Home, agentName)
			if err := d.Docker.StopContainer(ctx, cn); err != nil {
				return "", err
			}
			return fmt.Sprintf("Agent %s stopped", agentName), nil
		},
	)

	// restart_agent
	r.Register(
		Definition{
			Name:        "restart_agent",
			Description: "Restart an agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			if !agent.HasProviderKey(d.Home, agentName) {
				return "", fmt.Errorf("agent %q has no API key configured", agentName)
			}
			cn := agent.ContainerName(d.Home, agentName)
			// Best-effort stop — container may already be stopped or not exist.
			if stopErr := d.Docker.StopContainer(ctx, cn); stopErr != nil {
				// Force-remove in case stop failed but container still exists.
				d.Docker.RemoveContainer(ctx, cn, true)
			}
			opts := startOpts(d, agentName, 0)
			if err := d.Docker.StartContainer(ctx, cn, opts); err != nil {
				return "", err
			}
			return fmt.Sprintf("Agent %s restarted", agentName), nil
		},
	)

	// destroy_agent
	r.Register(
		Definition{
			Name:        "destroy_agent",
			Description: "Permanently delete an agent and ALL its data. You MUST first ask the user to confirm. Only pass confirm=true after the user explicitly says yes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":   map[string]any{"type": "string", "description": "Agent name to destroy"},
					"confirm": map[string]any{"type": "boolean", "description": "Set to true only after user explicitly confirms"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}

			confirmed := false
			if v, ok := args["confirm"]; ok {
				if b, ok := v.(bool); ok {
					confirmed = b
				}
			}

			if !confirmed {
				return fmt.Sprintf("⚠️ You are about to PERMANENTLY DESTROY agent '%s'.\n"+
					"This will delete:\n"+
					"- Docker container\n"+
					"- Agent config and data directory\n"+
					"- All conversation history inside the agent\n"+
					"- Persona dimensions stored in mux\n"+
					"- Agent state from mux registry\n\n"+
					"This cannot be undone. Ask the user to confirm.", agentName), nil
			}

			var steps []string

			// Stop container.
			cn := agent.ContainerName(d.Home, agentName)
			d.Docker.StopContainer(ctx, cn)
			d.Docker.RemoveContainer(ctx, cn, true)
			steps = append(steps, "Stopped and removed container")

			// Destroy agent directory.
			agent.Destroy(d.Home, agentName)
			steps = append(steps, "Deleted agent directory")

			// Clean up mux-side data.
			if err := d.Store.DeleteAgentPersona(agentName); err == nil {
				steps = append(steps, "Deleted persona from mux")
			}
			if _, err := d.Store.DeleteAgentState(agentName); err == nil {
				steps = append(steps, "Deleted state from mux")
			}

			return fmt.Sprintf("Agent '%s' destroyed.\n%s", agentName, strings.Join(steps, "\n")), nil
		},
	)

	// clone_agent
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
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			if isReservedName(d.Cfg, agentName) {
				return "", fmt.Errorf("cannot create agent named %q — that is the mux bot's own name", agentName)
			}
			source, err := stringArg(args, "source")
			if err != nil {
				return "", err
			}
			if err := agent.Clone(d.Home, source, agentName, agent.CloneOpts{}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Cloned %s from %s", agentName, source), nil
		},
	)

	// rename_agent
	r.Register(
		Definition{
			Name:        "rename_agent",
			Description: "Rename an agent. The agent must be stopped first. Updates config, identity, and mux database.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":    map[string]any{"type": "string", "description": "Current agent name"},
					"new_name": map[string]any{"type": "string", "description": "New agent name"},
				},
				"required": []string{"agent", "new_name"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			oldName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			newName, err := stringArg(args, "new_name")
			if err != nil {
				return "", err
			}
			if isReservedName(d.Cfg, newName) {
				return "", fmt.Errorf("cannot rename to %q — that is the mux bot's own name", newName)
			}

			// Ensure agent is stopped.
			cn := agent.ContainerName(d.Home, oldName)
			running, _ := d.Docker.IsRunning(ctx, cn)
			if running {
				return "", fmt.Errorf("agent %q is running — stop it first", oldName)
			}

			// Filesystem rename.
			if err := agent.Rename(d.Home, oldName, newName); err != nil {
				return "", err
			}

			// Database rename.
			if err := d.Store.RenameAgent(oldName, newName); err != nil {
				return "", fmt.Errorf("database rename: %w (filesystem already renamed)", err)
			}

			var steps []string
			steps = append(steps, fmt.Sprintf("Renamed %s → %s", oldName, newName))

			// Start the agent and send identity-change message.
			if agent.HasProviderKey(d.Home, newName) {
				newCN := agent.ContainerName(d.Home, newName)
				opts := startOpts(d, newName, 0)
				if err := d.Docker.StartContainer(ctx, newCN, opts); err != nil {
					steps = append(steps, fmt.Sprintf("Warning: start failed: %v", err))
				} else {
					steps = append(steps, "Started with new name")
					msg := agent.IdentityChangeMessage(oldName, newName)
					if _, err := sendToAgent(ctx, d, newName, msg); err != nil {
						steps = append(steps, fmt.Sprintf("Warning: identity message failed: %v", err))
					} else {
						steps = append(steps, "Identity update sent — response will appear shortly")
					}
				}
			}

			return strings.Join(steps, "\n"), nil
		},
	)

	// provision_agent
	r.Register(
		Definition{
			Name:        "provision_agent",
			Description: "Create a new agent, auto-configure it, assign a personality, start it, and send a greeting. The agent's response will appear in Telegram shortly.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name (optional — auto-generated if omitted)"},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName := optionalStringArg(args, "agent", "")
			if agentName == "" {
				agentName = pickAgentName(d)
			}
			if isReservedName(d.Cfg, agentName) {
				return "", fmt.Errorf("cannot create agent named %q — that is the mux bot's own name", agentName)
			}

			variant := personaVariants[len(agentName)%len(personaVariants)]
			var steps []string
			steps = append(steps, fmt.Sprintf("Name: %s", agentName))

			// 1. Setup with full configuration.
			sysPrompt := buildAgentSystemPrompt(agentName, variant)
			setupOpts := agent.SetupOpts{
				SystemPrompt:  sysPrompt,
				TelegramAllow: d.Cfg.Telegram.AllowFrom,
			}
			if d.Cfg.OpenAI.APIKey != "" {
				setupOpts.OpenAIKey = d.Cfg.OpenAI.APIKey
			}

			// Propagate keys from existing agents (single-tenant: all share the same keys).
			existingKeys := agent.CollectKeys(d.Home)
			if setupOpts.OpenAIKey == "" {
				setupOpts.OpenAIKey = existingKeys["openai"]
			}
			setupOpts.AnthropicKey = existingKeys["anthropic"]
			setupOpts.OpenRouterKey = existingKeys["openrouter"]
			setupOpts.BraveKey = existingKeys["brave"]

			agent.Setup(d.Home, agentName, setupOpts)
			steps = append(steps, "Created agent directory")
			steps = append(steps, fmt.Sprintf("Personality: %s", variant.Trait))

			// Store persona in mux SQLite.
			if err := d.Store.UpsertAgentPersona(memory.AgentPersona{
				Agent: agentName, Trait: variant.Trait,
				Warmth: variant.Warmth, Humor: variant.Humor, Verbosity: variant.Verbosity,
				Proactiveness: variant.Proactiveness, Formality: variant.Formality,
				Empathy: variant.Empathy, Sarcasm: variant.Sarcasm, Autonomy: variant.Autonomy,
				Interpretation: variant.Interpretation, Creativity: variant.Creativity,
			}); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: persona store failed: %v", err))
			}

			// 4. Start in mux mode.
			cn := agent.ContainerName(d.Home, agentName)
			opts := startOpts(d, agentName, 0)
			if err := d.Docker.StartContainer(ctx, cn, opts); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: start failed: %v", err))
				return strings.Join(steps, "\n"), nil
			}
			steps = append(steps, "Started in mux mode")

			// 5. Hello (async — response will be drained to Telegram).
			ownerName := d.Cfg.Persona.OwnerName
			if ownerName == "" {
				ownerName = "Controller"
			}
			greeting := fmt.Sprintf(
				"Hello. You are %s, and I am %s, a proxy for the human controller. "+
					"The human controller is named %s. You will address the human directly, not me. "+
					"Say hello back briefly, in character.",
				agentName, d.Cfg.Persona.Name, ownerName)
			if _, err := sendToAgent(ctx, d, agentName, greeting); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: hello failed: %v", err))
			} else {
				steps = append(steps, "Greeting sent — response will appear shortly")
			}

			return strings.Join(steps, "\n"), nil
		},
	)

	// get_agent_config
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
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			dir := agent.Dir(d.Home, agentName)
			all, err := agent.ConfigGetAllRedacted(dir)
			if err != nil {
				return "", err
			}
			data, err := json.MarshalIndent(all, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// update_agent_config
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
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			key, err := stringArg(args, "key")
			if err != nil {
				return "", err
			}
			// Reject redacted keys (API keys, tokens) — prevent LLM from overwriting secrets.
			if ck, ok := agent.LookupConfigKey(key); ok && ck.Redacted {
				return "", fmt.Errorf("cannot set %q via tool — use xnc config instead", key)
			}
			value, err := stringArg(args, "value")
			if err != nil {
				return "", err
			}
			dir := agent.Dir(d.Home, agentName)
			if err := agent.ConfigSet(dir, key, value); err != nil {
				return "", err
			}
			return fmt.Sprintf("Config %s set for %s", key, agentName), nil
		},
	)

	// get_agent_persona
	r.Register(
		Definition{
			Name:        "get_agent_persona",
			Description: "Get an agent's personality dimensions and trait descriptor",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}
			p, err := d.Store.GetAgentPersona(agentName)
			if err != nil {
				return "", err
			}
			if p == nil {
				return fmt.Sprintf("No persona stored for agent %q.", agentName), nil
			}
			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// update_agent_persona
	r.Register(
		Definition{
			Name:        "update_agent_persona",
			Description: "Update personality dimensions for an agent. Regenerates system_prompt. Requires restart.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":          map[string]any{"type": "string", "description": "Agent name"},
					"trait":          map[string]any{"type": "string", "description": "Short personality descriptor"},
					"warmth":         map[string]any{"type": "number", "description": "0.0-1.0"},
					"humor":          map[string]any{"type": "number", "description": "0.0-1.0"},
					"verbosity":      map[string]any{"type": "number", "description": "0.0-1.0"},
					"proactiveness":  map[string]any{"type": "number", "description": "0.0-1.0"},
					"formality":      map[string]any{"type": "number", "description": "0.0-1.0"},
					"empathy":        map[string]any{"type": "number", "description": "0.0-1.0"},
					"sarcasm":        map[string]any{"type": "number", "description": "0.0-1.0"},
					"autonomy":       map[string]any{"type": "number", "description": "0.0-1.0"},
					"interpretation": map[string]any{"type": "number", "description": "0.0-1.0"},
					"creativity":     map[string]any{"type": "number", "description": "0.0-1.0"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentName, err := validatedAgentArg(args)
			if err != nil {
				return "", err
			}

			p, err := d.Store.GetAgentPersona(agentName)
			if err != nil {
				return "", err
			}
			if p == nil {
				p = &memory.AgentPersona{
					Agent: agentName, Trait: "balanced",
					Warmth: 0.5, Humor: 0.5, Verbosity: 0.5, Proactiveness: 0.5,
					Formality: 0.5, Empathy: 0.5, Sarcasm: 0.5, Autonomy: 0.5,
					Interpretation: 0.5, Creativity: 0.5,
				}
			}

			var changed []string
			applyDim := func(name string, target *float64) {
				if v, err := float64Arg(args, name); err == nil {
					if v < 0.0 || v > 1.0 {
						return
					}
					*target = v
					changed = append(changed, fmt.Sprintf("%s=%.1f", name, v))
				}
			}
			applyDim("warmth", &p.Warmth)
			applyDim("humor", &p.Humor)
			applyDim("verbosity", &p.Verbosity)
			applyDim("proactiveness", &p.Proactiveness)
			applyDim("formality", &p.Formality)
			applyDim("empathy", &p.Empathy)
			applyDim("sarcasm", &p.Sarcasm)
			applyDim("autonomy", &p.Autonomy)
			applyDim("interpretation", &p.Interpretation)
			applyDim("creativity", &p.Creativity)

			if trait := optionalStringArg(args, "trait", ""); trait != "" {
				p.Trait = config.SanitizeText(trait, 200)
				changed = append(changed, fmt.Sprintf("trait=%q", p.Trait))
			}

			if len(changed) == 0 {
				return "No dimensions changed.", nil
			}

			if err := d.Store.UpsertAgentPersona(*p); err != nil {
				return "", fmt.Errorf("failed to store persona: %w", err)
			}

			variant := personaVariant{
				Trait: p.Trait, Warmth: p.Warmth, Humor: p.Humor,
				Verbosity: p.Verbosity, Proactiveness: p.Proactiveness,
				Formality: p.Formality, Empathy: p.Empathy, Sarcasm: p.Sarcasm,
				Autonomy: p.Autonomy, Interpretation: p.Interpretation,
				Creativity: p.Creativity,
			}
			sysPrompt := buildAgentSystemPrompt(agentName, variant)
			dir := agent.Dir(d.Home, agentName)
			agent.ConfigSet(dir, "system_prompt", sysPrompt)

			return fmt.Sprintf("Updated %s persona: %s\nSystem prompt regenerated. Restart agent to apply.", agentName, strings.Join(changed, ", ")), nil
		},
	)
}

// sendToMultiple sends a message to multiple agents in parallel (fire-and-forget).
func sendToMultiple(ctx context.Context, d Deps, names []string, message string) (string, error) {
	type result struct {
		Agent  string `json:"agent"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}

	results := make([]result, len(names))
	var wg sync.WaitGroup

	for i, n := range names {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			_, err := sendToAgent(ctx, d, name, message)
			if err != nil {
				results[idx] = result{Agent: name, Status: "error", Error: err.Error()}
			} else {
				results[idx] = result{Agent: name, Status: "delivered"}
			}
		}(i, n)
	}

	wg.Wait()

	data, _ := json.MarshalIndent(results, "", "  ")
	return string(data), nil
}
