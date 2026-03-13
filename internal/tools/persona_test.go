package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSetPersona_Name(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "set_persona", map[string]any{
		"field": "name",
		"value": "Hermes",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hermes") {
		t.Errorf("expected 'Hermes' in result, got %q", result)
	}
	if d.Cfg.Persona.Name != "Hermes" {
		t.Errorf("expected cfg.Persona.Name='Hermes', got %q", d.Cfg.Persona.Name)
	}
}

func TestSetPersona_OwnerName(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "set_persona", map[string]any{
		"field": "owner_name",
		"value": "Jorge",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Jorge") {
		t.Errorf("expected 'Jorge' in result, got %q", result)
	}
	if d.Cfg.Persona.OwnerName != "Jorge" {
		t.Errorf("expected cfg.Persona.OwnerName='Jorge', got %q", d.Cfg.Persona.OwnerName)
	}
}

func TestSetPersona_InvalidField(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	_, err := r.Execute(context.Background(), "set_persona", map[string]any{
		"field": "nonexistent_field",
		"value": "something",
	})
	if err == nil {
		t.Fatal("expected error for invalid persona field")
	}
	if !strings.Contains(err.Error(), "invalid persona field") {
		t.Errorf("expected 'invalid persona field' in error, got: %v", err)
	}
}

func TestSetPersona_TTSVoice_Valid(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "set_persona", map[string]any{
		"field": "tts_voice",
		"value": "echo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "echo") {
		t.Errorf("expected 'echo' in result, got %q", result)
	}
	if d.Cfg.Voice.TTSVoice != "echo" {
		t.Errorf("expected TTSVoice='echo', got %q", d.Cfg.Voice.TTSVoice)
	}
}

func TestSetPersona_TTSVoice_Invalid(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	_, err := r.Execute(context.Background(), "set_persona", map[string]any{
		"field": "tts_voice",
		"value": "nonexistent_voice",
	})
	if err == nil {
		t.Fatal("expected error for invalid TTS voice")
	}
	if !strings.Contains(err.Error(), "invalid TTS voice") {
		t.Errorf("expected 'invalid TTS voice' in error, got: %v", err)
	}
}

func TestSetPersonaDimension_Valid(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "set_persona_dimension", map[string]any{
		"dimension": "warmth",
		"value":     0.8,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "warmth") {
		t.Errorf("expected 'warmth' in result, got %q", result)
	}
	if !strings.Contains(result, "0.80") {
		t.Errorf("expected '0.80' in result, got %q", result)
	}
	if d.Cfg.Persona.Dimensions.Warmth != 0.8 {
		t.Errorf("expected Warmth=0.8, got %f", d.Cfg.Persona.Dimensions.Warmth)
	}
}

func TestSetPersonaDimension_OutOfRange(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	_, err := r.Execute(context.Background(), "set_persona_dimension", map[string]any{
		"dimension": "warmth",
		"value":     1.5,
	})
	if err == nil {
		t.Fatal("expected error for out-of-range dimension")
	}
	if !strings.Contains(err.Error(), "between 0.0 and 1.0") {
		t.Errorf("expected range error, got: %v", err)
	}

	// Also test negative.
	_, err = r.Execute(context.Background(), "set_persona_dimension", map[string]any{
		"dimension": "warmth",
		"value":     -0.1,
	})
	if err == nil {
		t.Fatal("expected error for negative dimension")
	}
}

func TestSetPersonaDimension_InvalidDim(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	_, err := r.Execute(context.Background(), "set_persona_dimension", map[string]any{
		"dimension": "aggressiveness",
		"value":     0.5,
	})
	if err == nil {
		t.Fatal("expected error for invalid dimension")
	}
	if !strings.Contains(err.Error(), "invalid dimension") {
		t.Errorf("expected 'invalid dimension' in error, got: %v", err)
	}
}

func TestGetPersona(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "get_persona", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nresult: %s", err, result)
	}
	// Should contain persona fields.
	if _, ok := parsed["name"]; !ok {
		t.Error("expected 'name' field in persona")
	}
	if _, ok := parsed["dimensions"]; !ok {
		t.Error("expected 'dimensions' field in persona")
	}
	if _, ok := parsed["tts_voice"]; !ok {
		t.Error("expected 'tts_voice' field in persona")
	}
}

func TestResetPersona(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	// Change a persona field first.
	d.Cfg.Persona.Name = "Changed"
	d.Cfg.Persona.Dimensions.Warmth = 0.1

	result, err := r.Execute(context.Background(), "reset_persona", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "reset to defaults") {
		t.Errorf("expected 'reset to defaults' in result, got %q", result)
	}
	if d.Cfg.Persona.Name != "Mux" {
		t.Errorf("expected name reset to 'Mux', got %q", d.Cfg.Persona.Name)
	}
	if d.Cfg.Persona.Dimensions.Warmth != 0.7 {
		t.Errorf("expected warmth reset to 0.7, got %f", d.Cfg.Persona.Dimensions.Warmth)
	}
}

func TestApplyPreset_Valid(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "apply_persona_preset", map[string]any{
		"preset": "professional",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "professional") {
		t.Errorf("expected 'professional' in result, got %q", result)
	}
	// Professional preset has formality=0.9.
	if d.Cfg.Persona.Dimensions.Formality != 0.9 {
		t.Errorf("expected formality=0.9, got %f", d.Cfg.Persona.Dimensions.Formality)
	}
}

func TestApplyPreset_Invalid(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerPersonaTools(r, d)

	_, err := r.Execute(context.Background(), "apply_persona_preset", map[string]any{
		"preset": "nonexistent_preset",
	})
	if err == nil {
		t.Fatal("expected error for invalid preset")
	}
	if !strings.Contains(err.Error(), "invalid preset") {
		t.Errorf("expected 'invalid preset' in error, got: %v", err)
	}
}

func TestListVoices(t *testing.T) {
	d, _ := newTestDeps(t)
	// Default voice is "nova".
	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "list_voices", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var voices []map[string]any
	if err := json.Unmarshal([]byte(result), &voices); err != nil {
		t.Fatalf("invalid JSON: %v\nresult: %s", err, result)
	}

	if len(voices) == 0 {
		t.Fatal("expected non-empty voices list")
	}

	// Find the active voice.
	activeCount := 0
	for _, v := range voices {
		if active, ok := v["active"].(bool); ok && active {
			activeCount++
			if v["name"] != "nova" {
				t.Errorf("expected active voice to be 'nova', got %q", v["name"])
			}
		}
	}
	if activeCount != 1 {
		t.Errorf("expected exactly 1 active voice, got %d", activeCount)
	}
}

func TestListVoices_ActiveAfterChange(t *testing.T) {
	d, _ := newTestDeps(t)
	d.Cfg.Voice.TTSVoice = "echo"

	r := NewRegistry()
	registerPersonaTools(r, d)

	result, err := r.Execute(context.Background(), "list_voices", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var voices []map[string]any
	if err := json.Unmarshal([]byte(result), &voices); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	for _, v := range voices {
		if active, ok := v["active"].(bool); ok && active {
			if v["name"] != "echo" {
				t.Errorf("expected active voice 'echo', got %q", v["name"])
			}
		}
	}
}
