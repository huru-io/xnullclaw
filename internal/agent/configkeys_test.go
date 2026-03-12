package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupConfigKey(t *testing.T) {
	ck, ok := LookupConfigKey("model")
	if !ok {
		t.Fatal("expected to find 'model' key")
	}
	if ck.Path != "agents.defaults.model.primary" {
		t.Errorf("unexpected path: %s", ck.Path)
	}
	if ck.Type != "string" {
		t.Errorf("unexpected type: %s", ck.Type)
	}

	ck, ok = LookupConfigKey("openai_key")
	if !ok {
		t.Fatal("expected to find 'openai_key' key")
	}
	if !ck.Redacted {
		t.Error("openai_key should be redacted")
	}

	// Verify brave_key.
	ck, ok = LookupConfigKey("brave_key")
	if !ok {
		t.Fatal("expected to find 'brave_key' key")
	}
	if ck.Path != "http_request.brave_api_key" {
		t.Errorf("unexpected brave_key path: %s", ck.Path)
	}
	if !ck.Redacted {
		t.Error("brave_key should be redacted")
	}

	// Verify search_fallback_providers.
	ck, ok = LookupConfigKey("search_fallback_providers")
	if !ok {
		t.Fatal("expected to find 'search_fallback_providers' key")
	}
	if ck.Type != "string_array" {
		t.Errorf("unexpected type for search_fallback_providers: %s", ck.Type)
	}

	_, ok = LookupConfigKey("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent key")
	}
}

func TestConfigGetSet(t *testing.T) {
	dir := t.TempDir()

	// Write a test config.
	cfg := DefaultAgentConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)

	// Get using friendly key.
	val, err := ConfigGet(dir, "model")
	if err != nil {
		t.Fatalf("ConfigGet: %v", err)
	}
	if val != "openai/gpt-5-mini" {
		t.Errorf("expected 'openai/gpt-5-mini', got %v", val)
	}

	// Set using friendly key.
	if err := ConfigSet(dir, "model", "gpt-4o"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}

	// Verify.
	val, err = ConfigGet(dir, "model")
	if err != nil {
		t.Fatalf("ConfigGet after set: %v", err)
	}
	if val != "gpt-4o" {
		t.Errorf("expected 'gpt-4o', got %v", val)
	}

	// Set float value.
	if err := ConfigSet(dir, "temperature", "0.5"); err != nil {
		t.Fatalf("ConfigSet temperature: %v", err)
	}
	val, _ = ConfigGet(dir, "temperature")
	if val != 0.5 {
		t.Errorf("expected 0.5, got %v", val)
	}

	// Set bool value.
	if err := ConfigSet(dir, "http_enabled", "true"); err != nil {
		t.Fatalf("ConfigSet bool: %v", err)
	}
	val, _ = ConfigGet(dir, "http_enabled")
	if val != true {
		t.Errorf("expected true, got %v", val)
	}

	// Set int value.
	if err := ConfigSet(dir, "http_timeout", "60"); err != nil {
		t.Fatalf("ConfigSet int: %v", err)
	}
	val, _ = ConfigGet(dir, "http_timeout")
	// JSON numbers are float64 after round-trip.
	if val != float64(60) {
		t.Errorf("expected 60, got %v (%T)", val, val)
	}
}

func TestConfigGetAll(t *testing.T) {
	dir := t.TempDir()

	cfg := DefaultAgentConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)

	all, err := ConfigGetAll(dir)
	if err != nil {
		t.Fatalf("ConfigGetAll: %v", err)
	}
	if all["default_temperature"] != 0.7 {
		t.Errorf("expected temperature 0.7, got %v", all["default_temperature"])
	}
}

func TestDefaultAgentConfig_SearchDefaults(t *testing.T) {
	cfg := DefaultAgentConfig()
	httpReq := cfg["http_request"].(map[string]any)

	if httpReq["search_provider"] != "brave" {
		t.Errorf("expected search_provider 'brave', got %v", httpReq["search_provider"])
	}

	fallback, ok := httpReq["search_fallback_providers"].([]any)
	if !ok || len(fallback) != 1 || fallback[0] != "duckduckgo" {
		t.Errorf("expected search_fallback_providers [duckduckgo], got %v", httpReq["search_fallback_providers"])
	}

	if _, exists := httpReq["brave_api_key"]; exists {
		t.Error("brave_api_key should not be present in default config")
	}
}

func TestConfigSetGet_SearchFallbackProviders(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0644)

	// Set fallback providers.
	if err := ConfigSet(dir, "search_fallback_providers", "duckduckgo,bing"); err != nil {
		t.Fatalf("ConfigSet: %v", err)
	}

	// Read back.
	val, err := ConfigGet(dir, "search_fallback_providers")
	if err != nil {
		t.Fatalf("ConfigGet: %v", err)
	}
	arr, ok := val.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", val)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr))
	}
	if arr[0] != "duckduckgo" || arr[1] != "bing" {
		t.Errorf("unexpected values: %v", arr)
	}
}

func TestConfigGetAllRedacted(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultAgentConfig()
	cfg["http_request"].(map[string]any)["brave_api_key"] = "BSA-secret-key-12345"
	cfg["models"].(map[string]any)["providers"] = map[string]any{
		"openai": map[string]any{"api_key": "sk-secret-openai-key-12345"},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(filepath.Join(dir, "config.json"), data, 0600)

	redacted, err := ConfigGetAllRedacted(dir)
	if err != nil {
		t.Fatalf("ConfigGetAllRedacted: %v", err)
	}

	// Brave key should be redacted.
	httpReq := redacted["http_request"].(map[string]any)
	braveVal := httpReq["brave_api_key"].(string)
	if braveVal == "BSA-secret-key-12345" {
		t.Error("brave_api_key should be redacted")
	}
	if braveVal != "BSA-************2345" {
		t.Errorf("unexpected redaction: %s", braveVal)
	}

	// OpenAI key should be redacted.
	providers := redacted["models"].(map[string]any)["providers"].(map[string]any)
	openai := providers["openai"].(map[string]any)
	openaiVal := openai["api_key"].(string)
	if openaiVal == "sk-secret-openai-key-12345" {
		t.Error("openai api_key should be redacted")
	}
}

func TestGetSetPath(t *testing.T) {
	doc := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
	}

	got := GetPath(doc, "a.b.c")
	if got != "deep" {
		t.Errorf("expected 'deep', got %v", got)
	}

	setPath(doc, "a.b.d", "new")
	got = GetPath(doc, "a.b.d")
	if got != "new" {
		t.Errorf("expected 'new', got %v", got)
	}

	// Create intermediate maps.
	setPath(doc, "x.y.z", "created")
	got = GetPath(doc, "x.y.z")
	if got != "created" {
		t.Errorf("expected 'created', got %v", got)
	}
}

func TestCoerceValue(t *testing.T) {
	tests := []struct {
		value    string
		typeName string
		want     any
		wantErr  bool
	}{
		{"hello", "string", "hello", false},
		{"1.5", "float", 1.5, false},
		{"42", "int", 42, false},
		{"true", "bool", true, false},
		{"a,b,c", "string_array", []any{"a", "b", "c"}, false},
		{"bad", "float", nil, true},
		{"bad", "int", nil, true},
		{"bad", "bool", nil, true},
	}

	for _, tt := range tests {
		got, err := coerceValue(tt.value, tt.typeName)
		if (err != nil) != tt.wantErr {
			t.Errorf("coerceValue(%q, %q) error = %v, wantErr %v", tt.value, tt.typeName, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && tt.typeName != "string_array" {
			if got != tt.want {
				t.Errorf("coerceValue(%q, %q) = %v, want %v", tt.value, tt.typeName, got, tt.want)
			}
		}
	}
}

func TestConfigGetAllRedacted_NoSecrets(t *testing.T) {
	home := t.TempDir()
	// Setup with no API keys — default config has no secrets.
	Setup(home, "alice", SetupOpts{})

	dir := Dir(home, "alice")
	redacted, err := ConfigGetAllRedacted(dir)
	if err != nil {
		t.Fatalf("ConfigGetAllRedacted: %v", err)
	}
	if redacted == nil {
		t.Fatal("expected non-nil map")
	}
	// Verify no redacted fields appear when no secrets are set.
	httpReq, _ := redacted["http_request"].(map[string]any)
	if val, ok := httpReq["brave_api_key"]; ok {
		if s, isStr := val.(string); isStr && s != "" {
			t.Errorf("expected no brave_api_key, got %q", s)
		}
	}
}

func TestConfigKey_EnvVarField(t *testing.T) {
	ck, ok := LookupConfigKey("brave_key")
	if !ok {
		t.Fatal("brave_key not found")
	}
	if ck.EnvVar != "BRAVE_API_KEY" {
		t.Errorf("expected EnvVar=BRAVE_API_KEY, got %q", ck.EnvVar)
	}

	// Non-env keys should have empty EnvVar.
	ck2, _ := LookupConfigKey("model")
	if ck2.EnvVar != "" {
		t.Errorf("expected empty EnvVar for model, got %q", ck2.EnvVar)
	}
}

func TestConfigKey_ProviderField(t *testing.T) {
	tests := []struct {
		key      string
		provider string
	}{
		{"openai_key", "openai"},
		{"anthropic_key", "anthropic"},
		{"openrouter_key", "openrouter"},
		{"brave_key", ""},    // no validation for brave
		{"model", ""},        // not a provider key
	}
	for _, tt := range tests {
		ck, ok := LookupConfigKey(tt.key)
		if !ok {
			t.Errorf("key %q not found", tt.key)
			continue
		}
		if ck.Provider != tt.provider {
			t.Errorf("key %q: Provider=%q, want %q", tt.key, ck.Provider, tt.provider)
		}
	}
}
