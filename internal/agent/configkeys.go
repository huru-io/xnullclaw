package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ConfigKey maps a friendly key name to a JSON path in the agent config.
type ConfigKey struct {
	Name     string // friendly name
	Path     string // dot-separated JSON path
	Type     string // "string", "float", "int", "bool", "string_array"
	Desc     string // short description
	Redacted bool   // hide value in output (API keys)
}

// ConfigKeys is the registry of all known config key aliases.
var ConfigKeys = []ConfigKey{
	{"model", "agents.defaults.model.primary", "string", "Primary LLM model", false},
	{"temperature", "default_temperature", "float", "Default temperature (0.0-2.0)", false},
	{"autonomy", "autonomy.level", "string", "Autonomy level", false},
	{"system_prompt", "agents.defaults.system_prompt", "string", "System prompt", false},
	{"max_actions_per_hour", "autonomy.max_actions_per_hour", "int", "Max autonomous actions/hour", false},
	{"memory_backend", "memory.backend", "string", "Memory backend type", false},
	{"cost_enabled", "cost.enabled", "bool", "Enable cost tracking", false},
	{"cost_daily_limit", "cost.daily_limit_usd", "float", "Daily cost limit (USD)", false},
	{"cost_monthly_limit", "cost.monthly_limit_usd", "float", "Monthly cost limit (USD)", false},
	{"openai_key", "models.providers.openai.api_key", "string", "OpenAI API key", true},
	{"anthropic_key", "models.providers.anthropic.api_key", "string", "Anthropic API key", true},
	{"openrouter_key", "models.providers.openrouter.api_key", "string", "OpenRouter API key", true},
	{"telegram_token", "channels.telegram.accounts.main.bot_token", "string", "Telegram bot token", true},
	{"telegram_allow_from", "channels.telegram.accounts.main.allow_from", "string_array", "Allowed Telegram user IDs", false},
	{"http_enabled", "http_request.enabled", "bool", "Enable HTTP requests", false},
	{"http_timeout", "http_request.timeout_secs", "int", "HTTP timeout (seconds)", false},
	{"http_max_response", "http_request.max_response_size", "int", "Max HTTP response size", false},
	{"http_allowed_domains", "http_request.allowed_domains", "string_array", "Allowed HTTP domains", false},
	{"search_provider", "http_request.search_provider", "string", "Web search provider", false},
}

// LookupConfigKey finds a ConfigKey by its friendly name.
func LookupConfigKey(name string) (ConfigKey, bool) {
	lower := strings.ToLower(name)
	for _, k := range ConfigKeys {
		if k.Name == lower {
			return k, true
		}
	}
	return ConfigKey{}, false
}

// ConfigGet reads a value from the agent's config.json using a friendly key
// or a raw JSON path.
func ConfigGet(agentDir, key string) (any, error) {
	path := filepath.Join(agentDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Resolve friendly key to JSON path.
	jsonPath := key
	if ck, ok := LookupConfigKey(key); ok {
		jsonPath = ck.Path
	}

	return getPath(doc, jsonPath), nil
}

// ConfigSet writes a value to the agent's config.json using a friendly key
// or a raw JSON path.
func ConfigSet(agentDir, key, value string) error {
	path := filepath.Join(agentDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Resolve friendly key to JSON path and type.
	jsonPath := key
	valType := "string"
	if ck, ok := LookupConfigKey(key); ok {
		jsonPath = ck.Path
		valType = ck.Type
	}

	// Convert value to the appropriate type.
	typedVal, err := coerceValue(value, valType)
	if err != nil {
		return fmt.Errorf("invalid value for %s: %w", key, err)
	}

	setPath(doc, jsonPath, typedVal)

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return os.WriteFile(path, append(out, '\n'), 0644)
}

// ConfigGetAll reads the entire agent config.json as a map.
func ConfigGetAll(agentDir string) (map[string]any, error) {
	path := filepath.Join(agentDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return doc, nil
}

// getPath navigates a nested map using a dot-separated path.
func getPath(doc map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var current any = doc
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

// setPath sets a value in a nested map using a dot-separated path,
// creating intermediate maps as needed.
func setPath(doc map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := doc
	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[part] = next
		}
		current = next
	}
}

// coerceValue converts a string value to the appropriate Go type
// based on the config key type.
func coerceValue(value, typeName string) (any, error) {
	switch typeName {
	case "string":
		return value, nil
	case "float":
		return strconv.ParseFloat(value, 64)
	case "int":
		return strconv.Atoi(value)
	case "bool":
		return strconv.ParseBool(value)
	case "string_array":
		// Accept comma-separated values.
		parts := strings.Split(value, ",")
		result := make([]any, len(parts))
		for i, p := range parts {
			result[i] = strings.TrimSpace(p)
		}
		return result, nil
	default:
		return value, nil
	}
}
