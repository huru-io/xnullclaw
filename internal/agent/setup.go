package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SetupOpts holds optional configuration to inject during agent creation.
type SetupOpts struct {
	OpenAIKey     string
	AnthropicKey  string
	OpenRouterKey string
	SystemPrompt  string   // if empty, auto-generated from name
	Model         string   // if empty, uses default
	TelegramAllow []string // allowed Telegram user IDs
}

// DefaultSystemPrompt returns a basic system prompt for an agent.
func DefaultSystemPrompt(name string) string {
	return fmt.Sprintf(
		"You are %s, an AI assistant.\n"+
			"You are helpful, friendly, and concise.\n"+
			"Respond naturally and stay in character.",
		name,
	)
}

// Setup creates a new agent: directories, default config, and .meta.
func Setup(home, name string, opts SetupOpts) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if Exists(home, name) {
		return fmt.Errorf("agent %q already exists", name)
	}
	if conflict, found := ConflictsWith(home, name); found {
		return fmt.Errorf("agent %q conflicts with existing agent %q (names sound the same)", name, conflict)
	}

	// Ensure instance ID exists.
	InstanceID(home)

	dir := Dir(home, name)

	// Create directory structure.
	for _, sub := range []string{
		"data/.nullclaw",
		"data/workspace",
	} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return fmt.Errorf("setup: create %s: %w", sub, err)
		}
	}

	// Generate config with injected values.
	cfg := DefaultAgentConfig()

	// System prompt.
	prompt := opts.SystemPrompt
	if prompt == "" {
		prompt = DefaultSystemPrompt(name)
	}
	cfg["agents"].(map[string]any)["defaults"].(map[string]any)["system_prompt"] = prompt

	// Model override.
	if opts.Model != "" {
		cfg["agents"].(map[string]any)["defaults"].(map[string]any)["model"].(map[string]any)["primary"] = opts.Model
	}

	// API keys — only add providers that have keys.
	providers := cfg["models"].(map[string]any)["providers"].(map[string]any)
	if opts.OpenAIKey != "" {
		providers["openai"] = map[string]any{"api_key": opts.OpenAIKey}
	}
	if opts.AnthropicKey != "" {
		providers["anthropic"] = map[string]any{"api_key": opts.AnthropicKey}
	}
	if opts.OpenRouterKey != "" {
		providers["openrouter"] = map[string]any{"api_key": opts.OpenRouterKey}
	}

	// Telegram — only add when there's something to configure.
	if len(opts.TelegramAllow) > 0 {
		allow := make([]any, len(opts.TelegramAllow))
		for i, v := range opts.TelegramAllow {
			allow[i] = v
		}
		cfg["channels"] = map[string]any{
			"telegram": map[string]any{
				"accounts": map[string]any{
					"main": map[string]any{
						"bot_token":  "",
						"allow_from": allow,
					},
				},
			},
		}
	}

	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("setup: marshal config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(cfgData, '\n'), 0600); err != nil {
		return fmt.Errorf("setup: write config: %w", err)
	}

	// Assign identity.
	emoji := NextEmoji(home, name)
	now := time.Now().UTC().Format(time.RFC3339)

	if err := WriteMetaBatch(dir, map[string]string{
		"NAME":    name,
		"CREATED": now,
		"EMOJI":   emoji,
	}); err != nil {
		return fmt.Errorf("setup: write meta: %w", err)
	}

	// Copy shared skills to new agent's workspace.
	InstallSharedToAgent(home, name)

	return nil
}

// DefaultAgentConfig returns a minimal default config for a new nullclaw agent.
func DefaultAgentConfig() map[string]any {
	return map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{
				"model": map[string]any{
					"primary": "openai/gpt-5-mini",
				},
				"system_prompt": "",
			},
		},
		"default_temperature": 0.7,
		"autonomy": map[string]any{
			"level":               "supervised",
			"max_actions_per_hour": 10,
		},
		"memory": map[string]any{
			"backend": "hybrid",
		},
		"cost": map[string]any{
			"enabled":           true,
			"daily_limit_usd":   5.0,
			"monthly_limit_usd": 100.0,
		},
		"models": map[string]any{
			"providers": map[string]any{},
		},
		"http_request": map[string]any{
			"enabled":           true,
			"timeout_secs":      30,
			"max_response_size": 100000,
			"allowed_domains":   []any{},
			"search_provider":   "duckduckgo",
		},
	}
}
