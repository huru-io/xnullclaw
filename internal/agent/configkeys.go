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
//
// Structs containing API key fields (SetupOpts, initOpts) should never be
// logged directly — always select non-secret fields explicitly.
type ConfigKey struct {
	Name     string // friendly name
	Path     string // dot-separated JSON path
	Type     string // "string", "float", "int", "bool", "string_array"
	Desc     string // short description
	Redacted bool   // hide value in output (API keys)
	EnvVar   string // container env var name (empty = not injected)
	Provider string // provider name for key validation (empty = no validation)
}

// ConfigKeys is the registry of all known config key aliases.
// EnvVar and Provider fields drive container env injection and key validation
// respectively — adding a new key here is sufficient to wire both behaviors.
var ConfigKeys = []ConfigKey{
	{Name: "model", Path: "agents.defaults.model.primary", Type: "string", Desc: "Primary LLM model"},
	{Name: "temperature", Path: "default_temperature", Type: "float", Desc: "Default temperature (0.0-2.0)"},
	{Name: "autonomy", Path: "autonomy.level", Type: "string", Desc: "Autonomy level"},
	{Name: "system_prompt", Path: "agents.defaults.system_prompt", Type: "string", Desc: "System prompt"},
	{Name: "max_actions_per_hour", Path: "autonomy.max_actions_per_hour", Type: "int", Desc: "Max autonomous actions/hour"},
	{Name: "memory_backend", Path: "memory.backend", Type: "string", Desc: "Memory backend type"},
	{Name: "cost_enabled", Path: "cost.enabled", Type: "bool", Desc: "Enable cost tracking"},
	{Name: "cost_daily_limit", Path: "cost.daily_limit_usd", Type: "float", Desc: "Daily cost limit (USD)"},
	{Name: "cost_monthly_limit", Path: "cost.monthly_limit_usd", Type: "float", Desc: "Monthly cost limit (USD)"},
	{Name: "openai_key", Path: "models.providers.openai.api_key", Type: "string", Desc: "OpenAI API key", Redacted: true, Provider: "openai"},
	{Name: "anthropic_key", Path: "models.providers.anthropic.api_key", Type: "string", Desc: "Anthropic API key", Redacted: true, Provider: "anthropic"},
	{Name: "openrouter_key", Path: "models.providers.openrouter.api_key", Type: "string", Desc: "OpenRouter API key", Redacted: true, Provider: "openrouter"},
	{Name: "telegram_token", Path: "channels.telegram.accounts.main.bot_token", Type: "string", Desc: "Telegram bot token", Redacted: true},
	{Name: "telegram_allow_from", Path: "channels.telegram.accounts.main.allow_from", Type: "string_array", Desc: "Allowed Telegram user IDs"},
	{Name: "http_enabled", Path: "http_request.enabled", Type: "bool", Desc: "Enable HTTP requests"},
	{Name: "http_timeout", Path: "http_request.timeout_secs", Type: "int", Desc: "HTTP timeout (seconds)"},
	{Name: "http_max_response", Path: "http_request.max_response_size", Type: "int", Desc: "Max HTTP response size"},
	{Name: "http_allowed_domains", Path: "http_request.allowed_domains", Type: "string_array", Desc: "Allowed HTTP domains"},
	{Name: "search_provider", Path: "http_request.search_provider", Type: "string", Desc: "Web search provider"},
	{Name: "search_fallback_providers", Path: "http_request.search_fallback_providers", Type: "string_array", Desc: "Fallback search providers"},
	{Name: "brave_key", Path: "http_request.brave_api_key", Type: "string", Desc: "Brave Search API key", Redacted: true, EnvVar: "BRAVE_API_KEY"},
	{Name: "persona_trait", Path: "persona.trait", Type: "string", Desc: "Personality trait descriptor"},
	{Name: "persona_warmth", Path: "persona.dimensions.warmth", Type: "float", Desc: "Warmth (0.0-1.0)"},
	{Name: "persona_humor", Path: "persona.dimensions.humor", Type: "float", Desc: "Humor (0.0-1.0)"},
	{Name: "persona_verbosity", Path: "persona.dimensions.verbosity", Type: "float", Desc: "Verbosity (0.0-1.0)"},
	{Name: "persona_proactiveness", Path: "persona.dimensions.proactiveness", Type: "float", Desc: "Proactiveness (0.0-1.0)"},
	{Name: "persona_formality", Path: "persona.dimensions.formality", Type: "float", Desc: "Formality (0.0-1.0)"},
	{Name: "persona_empathy", Path: "persona.dimensions.empathy", Type: "float", Desc: "Empathy (0.0-1.0)"},
	{Name: "persona_sarcasm", Path: "persona.dimensions.sarcasm", Type: "float", Desc: "Sarcasm (0.0-1.0)"},
	{Name: "persona_autonomy", Path: "persona.dimensions.autonomy", Type: "float", Desc: "Autonomy (0.0-1.0)"},
	{Name: "persona_interpretation", Path: "persona.dimensions.interpretation", Type: "float", Desc: "Interpretation (0.0-1.0)"},
	{Name: "persona_creativity", Path: "persona.dimensions.creativity", Type: "float", Desc: "Creativity (0.0-1.0)"},
	{Name: "gateway_paired_tokens", Path: "gateway.paired_tokens", Type: "string_array", Desc: "Gateway auth token hashes", Redacted: true},
	{Name: "gateway_require_pairing", Path: "gateway.require_pairing", Type: "bool", Desc: "Require auth for gateway webhook"},
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

	return GetPath(doc, jsonPath), nil
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

	return atomicWriteFile(path, append(out, '\n'), 0600)
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

// ConfigSetAll writes the entire config map back to config.json.
func ConfigSetAll(agentDir string, doc map[string]any) error {
	path := filepath.Join(agentDir, "config.json")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return atomicWriteFile(path, append(out, '\n'), 0600)
}

// atomicWriteFile writes data to a temp file then renames it into place.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ConfigGetAllRedacted reads the full config with secret values masked.
func ConfigGetAllRedacted(agentDir string) (map[string]any, error) {
	doc, err := ConfigGetAll(agentDir)
	if err != nil {
		return nil, err
	}
	for _, ck := range ConfigKeys {
		if !ck.Redacted {
			continue
		}
		val := GetPath(doc, ck.Path)
		switch v := val.(type) {
		case string:
			if v != "" {
				setPath(doc, ck.Path, RedactKey(v))
			}
		case []any:
			redacted := make([]any, len(v))
			for i, elem := range v {
				if s, ok := elem.(string); ok && s != "" {
					redacted[i] = RedactKey(s)
				} else {
					redacted[i] = elem
				}
			}
			setPath(doc, ck.Path, redacted)
		}
	}
	return doc, nil
}

// RedactKey masks a secret string, showing first 4 and last 4 characters.
func RedactKey(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

// GetPath navigates a nested map using a dot-separated path.
func GetPath(doc map[string]any, path string) any {
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
