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
	if val != "openai/gpt-4o-mini" {
		t.Errorf("expected 'openai/gpt-4o-mini', got %v", val)
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

func TestGetSetPath(t *testing.T) {
	doc := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
	}

	got := getPath(doc, "a.b.c")
	if got != "deep" {
		t.Errorf("expected 'deep', got %v", got)
	}

	setPath(doc, "a.b.d", "new")
	got = getPath(doc, "a.b.d")
	if got != "new" {
		t.Errorf("expected 'new', got %v", got)
	}

	// Create intermediate maps.
	setPath(doc, "x.y.z", "created")
	got = getPath(doc, "x.y.z")
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
