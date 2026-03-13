package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}

	// OpenAI defaults
	if cfg.OpenAI.Model != "gpt-5-mini" {
		t.Errorf("Model = %q, want %q", cfg.OpenAI.Model, "gpt-5-mini")
	}
	if cfg.OpenAI.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", cfg.OpenAI.Temperature)
	}
	if cfg.OpenAI.WhisperModel != "whisper-1" {
		t.Errorf("WhisperModel = %q, want %q", cfg.OpenAI.WhisperModel, "whisper-1")
	}
	if cfg.OpenAI.TTSVoice != "nova" {
		t.Errorf("OpenAI.TTSVoice = %q, want %q", cfg.OpenAI.TTSVoice, "nova")
	}

	// Persona defaults
	if cfg.Persona.Name != "Mux" {
		t.Errorf("Persona.Name = %q, want %q", cfg.Persona.Name, "Mux")
	}
	if cfg.Persona.OwnerName != "Controller" {
		t.Errorf("Persona.OwnerName = %q, want %q", cfg.Persona.OwnerName, "Controller")
	}
	if cfg.Persona.Language != "en" {
		t.Errorf("Persona.Language = %q, want %q", cfg.Persona.Language, "en")
	}

	// Voice defaults
	if !cfg.Voice.Enabled {
		t.Error("Voice.Enabled = false, want true")
	}
	if cfg.Voice.TTSMaxLength != 4096 {
		t.Errorf("Voice.TTSMaxLength = %d, want 4096", cfg.Voice.TTSMaxLength)
	}

	// Memory defaults
	if cfg.Memory.DBPath != "memory.db" {
		t.Errorf("Memory.DBPath = %q, want %q", cfg.Memory.DBPath, "memory.db")
	}
	if cfg.Memory.SummaryIntervalMessages != 50 {
		t.Errorf("Memory.SummaryIntervalMessages = %d, want 50", cfg.Memory.SummaryIntervalMessages)
	}

	// Costs defaults
	if !cfg.Costs.Track {
		t.Error("Costs.Track = false, want true")
	}
	if cfg.Costs.MonthlyBudgetUSD != 50.0 {
		t.Errorf("Costs.MonthlyBudgetUSD = %v, want 50.0", cfg.Costs.MonthlyBudgetUSD)
	}
	if cfg.Costs.DailyBudgetUSD != 5.0 {
		t.Errorf("Costs.DailyBudgetUSD = %v, want 5.0", cfg.Costs.DailyBudgetUSD)
	}
	if cfg.Costs.WarnAtPercent != 80 {
		t.Errorf("Costs.WarnAtPercent = %d, want 80", cfg.Costs.WarnAtPercent)
	}

	// Logging defaults
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Logging.RotateDays != 7 {
		t.Errorf("Logging.RotateDays = %d, want 7", cfg.Logging.RotateDays)
	}

	// Scheduler defaults
	if cfg.Scheduler.HeartbeatMinutes != 30 {
		t.Errorf("Scheduler.HeartbeatMinutes = %d, want 30", cfg.Scheduler.HeartbeatMinutes)
	}
}

func TestPersonaDimensionsDefaults(t *testing.T) {
	dims := PersonaDimensions{}.Defaults()

	tests := []struct {
		name string
		got  float64
		want float64
	}{
		{"Warmth", dims.Warmth, 0.7},
		{"Humor", dims.Humor, 0.5},
		{"Verbosity", dims.Verbosity, 0.3},
		{"Proactiveness", dims.Proactiveness, 0.9},
		{"Formality", dims.Formality, 0.1},
		{"Empathy", dims.Empathy, 0.5},
		{"Sarcasm", dims.Sarcasm, 0.2},
		{"Autonomy", dims.Autonomy, 0.9},
		{"Interpretation", dims.Interpretation, 0.2},
		{"Creativity", dims.Creativity, 0.8},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := DefaultConfig()
	original.Persona.Name = "TestBot"
	original.OpenAI.Model = "gpt-4o"
	original.Costs.DailyBudgetUSD = 10.0
	original.Agents.Default = "alice"
	original.Scheduler.HeartbeatMinutes = 15

	if err := original.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Verify modified fields survived the round trip.
	if loaded.Persona.Name != "TestBot" {
		t.Errorf("Persona.Name = %q, want %q", loaded.Persona.Name, "TestBot")
	}
	if loaded.OpenAI.Model != "gpt-4o" {
		t.Errorf("OpenAI.Model = %q, want %q", loaded.OpenAI.Model, "gpt-4o")
	}
	if loaded.Costs.DailyBudgetUSD != 10.0 {
		t.Errorf("Costs.DailyBudgetUSD = %v, want 10.0", loaded.Costs.DailyBudgetUSD)
	}
	if loaded.Agents.Default != "alice" {
		t.Errorf("Agents.Default = %q, want %q", loaded.Agents.Default, "alice")
	}
	if loaded.Scheduler.HeartbeatMinutes != 15 {
		t.Errorf("Scheduler.HeartbeatMinutes = %d, want 15", loaded.Scheduler.HeartbeatMinutes)
	}

	// Verify defaults that were not changed are still intact.
	if loaded.OpenAI.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", loaded.OpenAI.Temperature)
	}
	if loaded.Persona.Dimensions.Warmth != 0.7 {
		t.Errorf("Dimensions.Warmth = %v, want 0.7", loaded.Persona.Dimensions.Warmth)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(path, []byte(`{not valid json`), 0600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

func TestSaveAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// The temp file should not linger after a successful save.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file still exists after Save()")
	}

	// The saved file must be valid JSON that Load can parse.
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if loaded.OpenAI.Model != cfg.OpenAI.Model {
		t.Errorf("Model = %q, want %q", loaded.OpenAI.Model, cfg.OpenAI.Model)
	}
}

func TestSavePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}
