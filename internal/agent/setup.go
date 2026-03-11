package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Setup creates a new agent: directories, default config, and .meta.
func Setup(home, name string) error {
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

	// Generate default config.
	cfg := DefaultAgentConfig()
	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("setup: marshal config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(cfgData, '\n'), 0644); err != nil {
		return fmt.Errorf("setup: write config: %w", err)
	}

	// Assign identity.
	emoji := NextEmoji(home)
	now := time.Now().UTC().Format(time.RFC3339)

	if err := WriteMetaBatch(dir, map[string]string{
		"CREATED": now,
		"EMOJI":   emoji,
	}); err != nil {
		return fmt.Errorf("setup: write meta: %w", err)
	}

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
			"level":               "semi",
			"max_actions_per_hour": 10,
		},
		"memory": map[string]any{
			"backend": "local",
		},
		"cost": map[string]any{
			"enabled":           true,
			"daily_limit_usd":   5.0,
			"monthly_limit_usd": 100.0,
		},
		"models": map[string]any{
			"providers": map[string]any{
				"openai":     map[string]any{"api_key": ""},
				"anthropic":  map[string]any{"api_key": ""},
				"openrouter": map[string]any{"api_key": ""},
			},
		},
		"channels": map[string]any{
			"telegram": map[string]any{
				"accounts": map[string]any{
					"main": map[string]any{
						"bot_token":  "",
						"allow_from": []any{},
					},
				},
			},
		},
		"http_request": map[string]any{
			"enabled":           false,
			"timeout_secs":      30,
			"max_response_size": 100000,
			"allowed_domains":   []any{},
			"search_provider":   "",
		},
	}
}
