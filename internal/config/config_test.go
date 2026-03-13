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

func TestDefaultConfig_RuntimeDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Runtime.Mode != "local" {
		t.Errorf("Runtime.Mode = %q, want %q", cfg.Runtime.Mode, "local")
	}
	if cfg.Runtime.Network != "" {
		t.Errorf("Runtime.Network = %q, want empty", cfg.Runtime.Network)
	}
}

func TestApplyEnvOverrides_AllVars(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("XNC_TELEGRAM_BOT_TOKEN", "tok123")
	t.Setenv("XNC_TELEGRAM_GROUP_ID", "-100999")
	t.Setenv("XNC_TELEGRAM_TOPIC_ID", "42")
	t.Setenv("XNC_TELEGRAM_ALLOW_FROM", "user1, user2,user3")
	t.Setenv("XNC_OPENAI_API_KEY", "sk-test")
	t.Setenv("XNC_OPENAI_MODEL", "gpt-4o")
	t.Setenv("XNC_OPENAI_BASE_URL", "https://openrouter.ai/api/v1")
	t.Setenv("XNC_PERSONA_NAME", "TestBot")
	t.Setenv("XNC_PERSONA_OWNER", "TestOwner")
	t.Setenv("XNC_LOG_LEVEL", "debug")
	t.Setenv("XNC_RUNTIME", "docker")
	t.Setenv("XNC_NETWORK", "xnc-net")

	cfg.ApplyEnvOverrides()

	if cfg.Telegram.BotToken != "tok123" {
		t.Errorf("BotToken = %q, want %q", cfg.Telegram.BotToken, "tok123")
	}
	if cfg.Telegram.GroupID != -100999 {
		t.Errorf("GroupID = %d, want %d", cfg.Telegram.GroupID, -100999)
	}
	if cfg.Telegram.TopicID != 42 {
		t.Errorf("TopicID = %d, want %d", cfg.Telegram.TopicID, 42)
	}
	if len(cfg.Telegram.AllowFrom) != 3 || cfg.Telegram.AllowFrom[0] != "user1" || cfg.Telegram.AllowFrom[1] != "user2" || cfg.Telegram.AllowFrom[2] != "user3" {
		t.Errorf("AllowFrom = %v, want [user1 user2 user3]", cfg.Telegram.AllowFrom)
	}
	if cfg.OpenAI.APIKey != "sk-test" {
		t.Errorf("APIKey = %q, want %q", cfg.OpenAI.APIKey, "sk-test")
	}
	if cfg.OpenAI.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", cfg.OpenAI.Model, "gpt-4o")
	}
	if cfg.OpenAI.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL = %q, want OpenRouter URL", cfg.OpenAI.BaseURL)
	}
	if cfg.Persona.Name != "TestBot" {
		t.Errorf("Persona.Name = %q, want %q", cfg.Persona.Name, "TestBot")
	}
	if cfg.Persona.OwnerName != "TestOwner" {
		t.Errorf("Persona.OwnerName = %q, want %q", cfg.Persona.OwnerName, "TestOwner")
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
	}
	if cfg.Runtime.Mode != "docker" {
		t.Errorf("Runtime.Mode = %q, want %q", cfg.Runtime.Mode, "docker")
	}
	if cfg.Runtime.Network != "xnc-net" {
		t.Errorf("Runtime.Network = %q, want %q", cfg.Runtime.Network, "xnc-net")
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestApplyEnvOverrides_PartialOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OpenAI.Model = "gpt-5-mini"
	cfg.Telegram.BotToken = "original"

	// Only override the API key, leave others alone.
	t.Setenv("XNC_OPENAI_API_KEY", "sk-partial")

	cfg.ApplyEnvOverrides()

	if cfg.OpenAI.APIKey != "sk-partial" {
		t.Errorf("APIKey = %q, want %q", cfg.OpenAI.APIKey, "sk-partial")
	}
	// Unset env vars should not clobber existing values.
	if cfg.OpenAI.Model != "gpt-5-mini" {
		t.Errorf("Model = %q, want %q (should be unchanged)", cfg.OpenAI.Model, "gpt-5-mini")
	}
	if cfg.Telegram.BotToken != "original" {
		t.Errorf("BotToken = %q, want %q (should be unchanged)", cfg.Telegram.BotToken, "original")
	}
}

func TestApplyEnvOverrides_InvalidGroupID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Telegram.GroupID = 999

	t.Setenv("XNC_TELEGRAM_GROUP_ID", "not-a-number")

	cfg.ApplyEnvOverrides()

	// Invalid value should be ignored, original preserved.
	if cfg.Telegram.GroupID != 999 {
		t.Errorf("GroupID = %d, want %d (invalid value should be ignored)", cfg.Telegram.GroupID, 999)
	}
}

func TestApplyEnvOverrides_InvalidTopicID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Telegram.TopicID = 7

	t.Setenv("XNC_TELEGRAM_TOPIC_ID", "abc")

	cfg.ApplyEnvOverrides()

	if cfg.Telegram.TopicID != 7 {
		t.Errorf("TopicID = %d, want %d (invalid value should be ignored)", cfg.Telegram.TopicID, 7)
	}
}

func TestApplyEnvOverrides_InvalidRuntime(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("XNC_RUNTIME", "invalid-mode")

	cfg.ApplyEnvOverrides()

	if cfg.Runtime.Mode != "local" {
		t.Errorf("Runtime.Mode = %q, want %q (invalid mode should be ignored)", cfg.Runtime.Mode, "local")
	}
}

func TestApplyEnvOverrides_InvalidNetwork(t *testing.T) {
	cfg := DefaultConfig()

	t.Setenv("XNC_NETWORK", "bad name with spaces!")

	cfg.ApplyEnvOverrides()

	if cfg.Runtime.Network != "" {
		t.Errorf("Runtime.Network = %q, want empty (invalid name should be ignored)", cfg.Runtime.Network)
	}
}

func TestValidRuntimeMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"local", true},
		{"docker", true},
		{"kubernetes", true},
		{"", false},
		{"invalid", false},
		{"LOCAL", false},
	}
	for _, tt := range tests {
		if got := ValidRuntimeMode(tt.mode); got != tt.want {
			t.Errorf("ValidRuntimeMode(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestValidNetworkName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"xnc-net", true},
		{"my_network", true},
		{"net123", true},
		{"a", true},
		{"", false},
		{"bad name", false},
		{"-start-hyphen", false},
		{"has@symbol", false},
		{"has/slash", false},
		{string(make([]byte, 65)), false}, // too long
	}
	for _, tt := range tests {
		if got := ValidNetworkName(tt.name); got != tt.want {
			t.Errorf("ValidNetworkName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestApplyEnvOverrides_InvalidBaseURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OpenAI.BaseURL = "https://original.com/v1"

	t.Setenv("XNC_OPENAI_BASE_URL", "ftp://not-http.com")

	cfg.ApplyEnvOverrides()

	if cfg.OpenAI.BaseURL != "https://original.com/v1" {
		t.Errorf("BaseURL = %q, want original (invalid scheme should be ignored)", cfg.OpenAI.BaseURL)
	}
}

func TestApplyEnvOverrides_ValidBaseURL(t *testing.T) {
	// http:// scheme.
	cfg := DefaultConfig()
	t.Setenv("XNC_OPENAI_BASE_URL", "http://localhost:8080/v1")
	cfg.ApplyEnvOverrides()
	if cfg.OpenAI.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("BaseURL = %q, want http://localhost:8080/v1", cfg.OpenAI.BaseURL)
	}

	// https:// scheme.
	cfg2 := DefaultConfig()
	t.Setenv("XNC_OPENAI_BASE_URL", "https://openrouter.ai/api/v1")
	cfg2.ApplyEnvOverrides()
	if cfg2.OpenAI.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL = %q, want https://openrouter.ai/api/v1", cfg2.OpenAI.BaseURL)
	}
}

func TestApplyEnvOverrides_InvalidLogLevel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logging.Level = "info"

	t.Setenv("XNC_LOG_LEVEL", "verbose")

	cfg.ApplyEnvOverrides()

	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q, want %q (invalid level should be ignored)", cfg.Logging.Level, "info")
	}
}

func TestApplyEnvOverrides_ValidLogLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		cfg := DefaultConfig()
		t.Setenv("XNC_LOG_LEVEL", level)
		cfg.ApplyEnvOverrides()
		if cfg.Logging.Level != level {
			t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, level)
		}
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
