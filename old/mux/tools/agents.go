// Package tools — agents.go implements agent communication and lifecycle tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/memory"
)

// namePool is a curated set of short, pronounceable, phonetically distinct names.
// Designed to be easily distinguishable when spoken aloud or read quickly.
var namePool = []string{
	"Arlo", "Bex", "Clio", "Dax", "Echo",
	"Faye", "Gale", "Haze", "Iris", "Juno",
	"Knox", "Luna", "Mira", "Nyx", "Opal",
	"Pike", "Quinn", "Rune", "Sage", "Taro",
	"Uma", "Vale", "Wren", "Xyla", "Yuki", "Zara",
}

// pickAgentName selects an available name from the pool, avoiding existing agents
// and the mux bot's own name. Falls back to "Agent-<N>" if pool is exhausted.
func pickAgentName(ctx context.Context, cfg *config.Config, wrapperPath string) string {
	// Get list of existing agents.
	existing := make(map[string]bool)
	out, err := runWrapper(ctx, wrapperPath, "list", "--json")
	if err == nil && out != "" {
		// Try to parse JSON array of agent names or objects.
		var agents []map[string]any
		if json.Unmarshal([]byte(out), &agents) == nil {
			for _, a := range agents {
				if name, ok := a["name"].(string); ok {
					existing[strings.ToLower(name)] = true
				}
			}
		}
		// Also try plain lines.
		if len(existing) == 0 {
			for _, line := range strings.Split(out, "\n") {
				name := strings.TrimSpace(line)
				if name != "" {
					existing[strings.ToLower(name)] = true
				}
			}
		}
	}

	// Shuffle and pick first available.
	perm := rand.Perm(len(namePool))
	for _, i := range perm {
		name := namePool[i]
		lower := strings.ToLower(name)
		if !existing[lower] && !strings.EqualFold(name, cfg.Persona.Name) {
			return name
		}
	}

	// Pool exhausted — generate a numbered fallback.
	for i := 1; ; i++ {
		name := fmt.Sprintf("Agent-%d", i)
		if !existing[strings.ToLower(name)] {
			return name
		}
	}
}

// isReservedName checks if the agent name conflicts with the mux bot name.
func isReservedName(cfg *config.Config, name string) bool {
	return strings.EqualFold(name, cfg.Persona.Name)
}

// personaVariant holds a complete set of personality dimensions for an agent.
// All 10 dimensions match the mux's own PersonaDimensions (0.0–1.0).
type personaVariant struct {
	Trait          string // short trait descriptor for the greeting
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

// personaVariants defines a pool of personality profiles.
// Each is subtly different so agents feel distinct from each other.
//                                               War  Hum  Ver  Pro  For  Emp  Sar  Aut  Int  Cre
var personaVariants = []personaVariant{
	{"friendly and straightforward", /*   */ 0.7, 0.4, 0.4, 0.6, 0.3, 0.6, 0.1, 0.5, 0.2, 0.4},
	{"precise and analytical",      /*   */ 0.4, 0.2, 0.5, 0.5, 0.6, 0.3, 0.0, 0.4, 0.1, 0.3},
	{"warm and creative",           /*   */ 0.8, 0.5, 0.4, 0.7, 0.3, 0.7, 0.1, 0.6, 0.3, 0.8},
	{"calm and thorough",           /*   */ 0.5, 0.2, 0.7, 0.5, 0.5, 0.5, 0.0, 0.4, 0.2, 0.4},
	{"witty and concise",           /*   */ 0.5, 0.7, 0.2, 0.6, 0.4, 0.4, 0.3, 0.6, 0.3, 0.6},
	{"earnest and helpful",         /*   */ 0.7, 0.3, 0.5, 0.8, 0.5, 0.7, 0.0, 0.7, 0.2, 0.4},
	{"playful and inventive",       /*   */ 0.6, 0.6, 0.3, 0.7, 0.2, 0.5, 0.2, 0.7, 0.4, 0.9},
	{"methodical and reliable",     /*   */ 0.4, 0.2, 0.6, 0.5, 0.7, 0.4, 0.0, 0.4, 0.1, 0.3},
	{"curious and adaptable",       /*   */ 0.6, 0.4, 0.4, 0.6, 0.4, 0.5, 0.1, 0.6, 0.3, 0.7},
	{"direct and efficient",        /*   */ 0.3, 0.2, 0.2, 0.5, 0.5, 0.3, 0.1, 0.5, 0.1, 0.3},
}

// dimensionLabel describes each dimension at three levels (low/mid/high).
type dimensionLabel struct {
	name string
	low  string
	mid  string
	high string
}

var agentDimensionLabels = []dimensionLabel{
	{"warmth", "Be clinical and matter-of-fact", "Be friendly but professional", "Be warm, caring, and personal"},
	{"humor", "Never joke or use humor", "Use occasional humor when appropriate", "Be playful, use jokes and wit freely"},
	{"verbosity", "Be extremely terse — minimum words", "Balance brevity and detail", "Be thorough and detailed in explanations"},
	{"proactiveness", "Only respond when explicitly asked", "Suggest actions when clearly relevant", "Actively anticipate needs and volunteer information"},
	{"formality", "Be casual, slang is fine", "Professional but relaxed", "Be formal and proper at all times"},
	{"empathy", "Be matter-of-fact, skip emotional acknowledgment", "Acknowledge feelings when relevant", "Be emotionally attuned and supportive"},
	{"sarcasm", "Never be sarcastic", "Light irony occasionally", "Use sharp wit and sarcasm freely"},
	{"autonomy", "Always ask before taking action", "Act on clear intent, ask when ambiguous", "Take initiative freely, act first"},
	{"interpretation", "Take messages literally", "Fix obvious typos silently", "Actively refine and clarify messages"},
	{"creativity", "Be straightforward and predictable", "Balance conventional and novel approaches", "Prefer creative and surprising solutions"},
}

// buildAgentSystemPrompt generates a natural-language system prompt from a personaVariant.
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
	for i, label := range agentDimensionLabels {
		val := values[i]
		var desc string
		switch {
		case val < 0.33:
			desc = label.low
		case val > 0.66:
			desc = label.high
		default:
			desc = label.mid
		}
		lines = append(lines, "- "+desc)
	}
	return strings.Join(lines, "\n")
}

func registerAgentTools(r *Registry, cfg *config.Config, wrapperPath string, store *memory.Store) {
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
			Description: "Start an agent via xnc (in mux mode)",
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
			Description: "Stop an agent via xnc",
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
			Description: "Restart an agent via xnc",
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
	// destroy_agent — two-step: first call without confirm to get warning,
	//                 then call with confirm=true after user says yes.
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "destroy_agent",
			Description: "Permanently delete an agent and ALL its data (container, config, memories, persona). This is IRREVERSIBLE. You MUST first ask the user to confirm by showing them what will be destroyed. Only pass confirm=true after the user explicitly says yes.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":   map[string]any{"type": "string", "description": "Agent name to destroy"},
					"confirm": map[string]any{"type": "boolean", "description": "Set to true only after user explicitly confirms destruction"},
				},
				"required": []string{"agent"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}

			// Check confirm flag.
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
					"This cannot be undone. Ask the user to confirm.", agent), nil
			}

			var steps []string

			// 1. Stop container if running.
			runWrapper(ctx, wrapperPath, agent, "stop")
			steps = append(steps, "Stopped container")

			// 2. Destroy via wrapper (removes container + data directory).
			if out, err := runWrapper(ctx, wrapperPath, agent, "destroy", "--yes"); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: wrapper destroy: %v", err))
			} else {
				steps = append(steps, strings.TrimSpace(out))
			}

			// 3. Clean up mux-side data.
			if err := store.DeleteAgentPersona(agent); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: failed to delete persona: %v", err))
			} else {
				steps = append(steps, "Deleted persona from mux")
			}

			// Remove from agent_state table.
			if _, err := store.DeleteAgentState(agent); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: failed to delete agent state: %v", err))
			} else {
				steps = append(steps, "Deleted state from mux")
			}

			return fmt.Sprintf("Agent '%s' destroyed.\n%s", agent, strings.Join(steps, "\n")), nil
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
			if isReservedName(cfg, agent) {
				return "", fmt.Errorf("cannot create agent named %q — that is the mux bot's own name", agent)
			}
			source, err := stringArg(args, "source")
			if err != nil {
				return "", err
			}
			return runWrapper(ctx, wrapperPath, agent, "clone", source)
		},
	)

	// -----------------------------------------------------------------------
	// provision_agent — full setup: create + configure + start in mux mode
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "provision_agent",
			Description: "Create a new agent, auto-configure it with API keys, assign a unique personality, start it in mux mode, and say hello. If no agent name is provided, one is auto-generated. Returns the agent's first response — always relay it to the user.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name (optional — auto-generated if omitted)"},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent := optionalStringArg(args, "agent", "")

			// Auto-generate name if not provided.
			if agent == "" {
				agent = pickAgentName(ctx, cfg, wrapperPath)
			}

			// Block reserved names.
			if isReservedName(cfg, agent) {
				return "", fmt.Errorf("cannot create agent named %q — that is the mux bot's own name", agent)
			}

			// Pick a personality variant (deterministic from name hash for consistency).
			variant := personaVariants[len(agent)%len(personaVariants)]

			var steps []string
			steps = append(steps, fmt.Sprintf("Name: %s", agent))

			// 1. Setup (create agent directory + default config).
			if _, err := runWrapper(ctx, wrapperPath, agent, "setup"); err != nil {
				return "", fmt.Errorf("setup failed: %w", err)
			}
			steps = append(steps, "Created agent directory")

			// 2. Inject credentials from mux config so agents work both
			//    in mux mode AND standalone (independent Telegram use).
			if cfg.OpenAI.APIKey != "" {
				if _, err := runWrapper(ctx, wrapperPath, agent, "config", "set", "openai_key", cfg.OpenAI.APIKey); err != nil {
					steps = append(steps, fmt.Sprintf("Warning: failed to set API key: %v", err))
				} else {
					steps = append(steps, "Configured OpenAI API key")
				}
			}

			// Set Telegram allow_from so standalone mode already knows the owner.
			if len(cfg.Telegram.AllowFrom) > 0 {
				allowList := strings.Join(cfg.Telegram.AllowFrom, ",")
				if _, err := runWrapper(ctx, wrapperPath, agent, "config", "set", "telegram_allow_from", allowList); err != nil {
					steps = append(steps, fmt.Sprintf("Warning: failed to set allow_from: %v", err))
				} else {
					steps = append(steps, "Configured Telegram allow_from")
				}
			}

			// 2b. Enable web tools (search, HTTP requests, web fetch).
			if _, err := runWrapper(ctx, wrapperPath, agent, "config", "set", "http_enabled", "true"); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: failed to enable HTTP tools: %v", err))
			} else {
				steps = append(steps, "Enabled web search + HTTP tools")
			}
			// Use duckduckgo as default search (free, no API key).
			runWrapper(ctx, wrapperPath, agent, "config", "set", "search_provider", "duckduckgo")

			// 2d. Set system_prompt with personality dimensions (persisted in agent config).
			sysPrompt := buildAgentSystemPrompt(agent, variant)
			if _, err := runWrapper(ctx, wrapperPath, agent, "config", "set", "system_prompt", sysPrompt); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: failed to set system_prompt: %v", err))
			} else {
				steps = append(steps, fmt.Sprintf("Personality: %s (10 dimensions configured)", variant.Trait))
			}

			// 2e. Store persona dimensions in mux's SQLite (source of truth for updates).
			store.UpsertAgentPersona(memory.AgentPersona{
				Agent: agent, Trait: variant.Trait,
				Warmth: variant.Warmth, Humor: variant.Humor, Verbosity: variant.Verbosity,
				Proactiveness: variant.Proactiveness, Formality: variant.Formality,
				Empathy: variant.Empathy, Sarcasm: variant.Sarcasm, Autonomy: variant.Autonomy,
				Interpretation: variant.Interpretation, Creativity: variant.Creativity,
			})

			// 3. Start in mux mode (no port, docker exec).
			if _, err := runWrapper(ctx, wrapperPath, agent, "start", "--mux"); err != nil {
				steps = append(steps, fmt.Sprintf("Warning: start failed: %v", err))
				return strings.Join(steps, "\n"), nil
			}
			steps = append(steps, "Started in mux mode")

			// 4. Structured hello — introduce all parties.
			// Personality is already in the system_prompt, so the hello
			// only needs to establish identity and relationships.
			ownerName := cfg.Persona.OwnerName
			if ownerName == "" {
				ownerName = "Controller"
			}
			greeting := fmt.Sprintf(
				"Hello. You are %s, and I am %s, a proxy for the human controller. "+
					"The human controller is named %s. You will address the human directly, not me. "+
					"Say hello back briefly, in character.",
				agent, cfg.Persona.Name, ownerName)
			hello, err := runWrapperWithStdin(ctx, wrapperPath, greeting, agent, "send")
			if err != nil {
				steps = append(steps, fmt.Sprintf("Warning: hello failed: %v", err))
			} else {
				steps = append(steps, fmt.Sprintf("%s says: %s", agent, strings.TrimSpace(hello)))
			}

			return strings.Join(steps, "\n"), nil
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

	// -----------------------------------------------------------------------
	// get_agent_persona
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_agent_persona",
			Description: "Get an agent's personality dimensions (10 sliders 0.0-1.0) and trait descriptor",
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
			p, err := store.GetAgentPersona(agent)
			if err != nil {
				return "", err
			}
			if p == nil {
				return fmt.Sprintf("No persona stored for agent %q. It may have been created before persona tracking was added.", agent), nil
			}
			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// -----------------------------------------------------------------------
	// update_agent_persona
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "update_agent_persona",
			Description: "Update one or more personality dimensions for an agent. Regenerates and pushes the system_prompt to keep text and sliders in sync. Requires agent restart to take full effect.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":          map[string]any{"type": "string", "description": "Agent name"},
					"trait":          map[string]any{"type": "string", "description": "Short personality descriptor (e.g. 'warm and creative')"},
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
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}

			// Load existing persona (or start from defaults).
			p, err := store.GetAgentPersona(agent)
			if err != nil {
				return "", err
			}
			if p == nil {
				p = &memory.AgentPersona{
					Agent: agent, Trait: "balanced",
					Warmth: 0.5, Humor: 0.5, Verbosity: 0.5, Proactiveness: 0.5,
					Formality: 0.5, Empathy: 0.5, Sarcasm: 0.5, Autonomy: 0.5,
					Interpretation: 0.5, Creativity: 0.5,
				}
			}

			// Apply provided overrides.
			var changed []string
			applyDim := func(name string, target *float64) {
				if v, err := float64Arg(args, name); err == nil {
					if v < 0.0 || v > 1.0 {
						return // silently ignore out-of-range
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
				p.Trait = trait
				changed = append(changed, fmt.Sprintf("trait=%q", trait))
			}

			if len(changed) == 0 {
				return "No dimensions changed.", nil
			}

			// Persist to mux SQLite.
			if err := store.UpsertAgentPersona(*p); err != nil {
				return "", fmt.Errorf("failed to store persona: %w", err)
			}

			// Regenerate system_prompt and push to agent config.
			variant := personaVariant{
				Trait: p.Trait, Warmth: p.Warmth, Humor: p.Humor,
				Verbosity: p.Verbosity, Proactiveness: p.Proactiveness,
				Formality: p.Formality, Empathy: p.Empathy, Sarcasm: p.Sarcasm,
				Autonomy: p.Autonomy, Interpretation: p.Interpretation,
				Creativity: p.Creativity,
			}
			sysPrompt := buildAgentSystemPrompt(agent, variant)
			if _, err := runWrapper(ctx, wrapperPath, agent, "config", "set", "system_prompt", sysPrompt); err != nil {
				return fmt.Sprintf("Persona saved but failed to push system_prompt: %v. Restart agent to apply.", err), nil
			}

			return fmt.Sprintf("Updated %s persona: %s\nSystem prompt regenerated and pushed. Restart agent to apply.", agent, strings.Join(changed, ", ")), nil
		},
	)
}
