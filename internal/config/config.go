// Package config handles loading, saving, and providing defaults for mux configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Config is the top-level configuration matching PRD 5.3.
type Config struct {
	Telegram  TelegramConfig  `json:"telegram"`
	OpenAI    OpenAIConfig    `json:"openai"`
	Agents    AgentsConfig    `json:"agents"`
	Voice     VoiceConfig     `json:"voice"`
	Memory    MemoryConfig    `json:"memory"`
	Costs     CostsConfig     `json:"costs"`
	Logging   LoggingConfig   `json:"logging"`
	Persona   PersonaConfig   `json:"persona"`
	Scheduler SchedulerConfig `json:"scheduler"`
	Runtime   RuntimeConfig   `json:"runtime"`
}

// RuntimeConfig holds settings for multi-environment deployment.
type RuntimeConfig struct {
	Mode    string `json:"mode"`    // "local" (default), "docker", "kubernetes"
	Network string `json:"network"` // Docker network name (e.g. "xnc-net"), empty = default bridge
}

// SchedulerConfig holds settings for the mux's task scheduler and heartbeat.
type SchedulerConfig struct {
	HeartbeatMinutes int `json:"heartbeat_minutes"` // 0 = disabled, default 30
}

// PersonaConfig defines the bot's personality.
type PersonaConfig struct {
	Name              string            `json:"name"`
	OwnerName         string            `json:"owner_name"`
	Language          string            `json:"language"`
	Bio               string            `json:"bio"`
	ExtraInstructions string            `json:"extra_instructions"`
	Dimensions PersonaDimensions `json:"dimensions"`
}

// PersonaDimensions holds the 0.0–1.0 personality sliders.
type PersonaDimensions struct {
	Warmth         float64 `json:"warmth"`
	Humor          float64 `json:"humor"`
	Verbosity      float64 `json:"verbosity"`
	Proactiveness  float64 `json:"proactiveness"`
	Formality      float64 `json:"formality"`
	Empathy        float64 `json:"empathy"`
	Sarcasm        float64 `json:"sarcasm"`
	Autonomy       float64 `json:"autonomy"`
	Interpretation float64 `json:"interpretation"`
	Creativity     float64 `json:"creativity"`
}

// Defaults returns the default persona dimensions as specified in the PRD.
func (PersonaDimensions) Defaults() PersonaDimensions {
	return PersonaDimensions{
		Warmth:         0.7,
		Humor:          0.5,
		Verbosity:      0.3,
		Proactiveness:  0.9,
		Formality:      0.1,
		Empathy:        0.5,
		Sarcasm:        0.2,
		Autonomy:       0.9,
		Interpretation: 0.2,
		Creativity:     0.8,
	}
}

// TelegramConfig holds Telegram bot settings.
type TelegramConfig struct {
	BotToken  string   `json:"bot_token"`
	AllowFrom []string `json:"allow_from"`
	GroupID   int64    `json:"group_id,omitempty"`  // 0 = private chat mode (default)
	TopicID   int      `json:"topic_id"`  // -1 = discover, 0 = no topic, 1 = General, N = specific
}

// OpenAIConfig holds OpenAI-compatible API settings.
// BaseURL can be overridden to point to OpenRouter or other compatible APIs.
type OpenAIConfig struct {
	APIKey       string  `json:"api_key"`
	BaseURL      string  `json:"base_url,omitempty"` // default: https://api.openai.com/v1
	Model        string  `json:"model"`
	Temperature  float64 `json:"temperature"`
	WhisperModel string  `json:"whisper_model"`
	TTSVoice     string  `json:"tts_voice"`
}

// AgentsConfig holds agent routing and lifecycle settings.
//
// AutoStart vs MuxManaged:
//   - AutoStart: containers started when mux boots (e.g. always-on agents).
//   - MuxManaged: containers stopped when mux shuts down (superset of AutoStart).
//     An agent can be MuxManaged without being in AutoStart if it was started
//     manually or via a tool during the session.
type AgentsConfig struct {
	Default    string                   `json:"default"`       // agent name used when routing is ambiguous
	AutoStart  []string                 `json:"auto_start"`    // agents started automatically when mux boots
	MuxManaged []string                 `json:"mux_managed"`   // agents whose lifecycle the mux controls (stopped on mux shutdown)
	Identities map[string]AgentIdentity `json:"identities"`
}

// AgentIdentity maps an agent name to its emoji and aliases.
type AgentIdentity struct {
	Emoji   string   `json:"emoji"`
	Aliases []string `json:"aliases"`
}

// VoiceConfig holds voice/TTS/STT settings.
type VoiceConfig struct {
	Enabled           bool                `json:"enabled"`
	ShowTranscription bool                `json:"show_transcription"`
	TTSEnabled        bool                `json:"tts_enabled"`
	TTSVoice          string              `json:"tts_voice"`
	TTSForAgents      bool                `json:"tts_for_agents"`
	TTSMaxLength      int                 `json:"tts_max_length"`
	CorrectionDict    map[string][]string `json:"correction_dictionary"`
}

// MemoryConfig holds persistent memory/DB settings.
type MemoryConfig struct {
	DBPath                  string `json:"db_path"`
	SummaryIntervalMessages int    `json:"summary_interval_messages"` // number of messages between compaction summaries (0 = disabled)
}

// CostsConfig holds budget and cost tracking settings.
type CostsConfig struct {
	Track              bool    `json:"track"`
	MonthlyBudgetUSD   float64 `json:"monthly_budget_usd"`
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`
	WarnAtPercent      int     `json:"warn_at_percent"`           // percentage threshold for budget warnings in /costs display
	PerAgentDailyLimit float64 `json:"per_agent_daily_limit_usd"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level         string `json:"level"`
	Dir           string `json:"dir"`
	RotateDays    int    `json:"rotate_days"`
	MaxFileSizeMB int    `json:"max_file_size_mb"` // 0 = default 10MB per log file
}

// Load reads a JSON config file from the given path and returns a Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the config to the given path as indented JSON.
// Uses atomic write (temp file + rename) to prevent corruption on crash.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// DefaultConfig returns a Config populated with sensible defaults matching the PRD.
func DefaultConfig() *Config {
	dims := PersonaDimensions{}.Defaults()
	return &Config{
		Telegram: TelegramConfig{
			BotToken:  "",
			AllowFrom: []string{},
		},
		OpenAI: OpenAIConfig{
			APIKey:       "",
			Model:        "gpt-5-mini",
			Temperature:  0.7,
			WhisperModel: "whisper-1",
			TTSVoice:     "nova",
		},
		Agents: AgentsConfig{
			Default:    "",
			AutoStart:  []string{},
			MuxManaged: []string{},
			Identities: map[string]AgentIdentity{},
		},
		Voice: VoiceConfig{
			Enabled:           true,
			ShowTranscription: true,
			TTSEnabled:        false,
			TTSVoice:          "nova",
			TTSForAgents:      false,
			TTSMaxLength:      4096,
			CorrectionDict:    map[string][]string{},
		},
		Memory: MemoryConfig{
			DBPath:                  "memory.db",
			SummaryIntervalMessages: 50,
		},
		Costs: CostsConfig{
			Track:              true,
			MonthlyBudgetUSD:   50.0,
			DailyBudgetUSD:     5.0,
			WarnAtPercent:      80,
			PerAgentDailyLimit: 2.0,
		},
		Logging: LoggingConfig{
			Level:      "info",
			Dir:        "logs",
			RotateDays: 7,
		},
		Persona: PersonaConfig{
			Name:              "Mux",
			OwnerName:         "Controller",
			Language:          "en",
			Bio:               "",
			ExtraInstructions: "",
			Dimensions:        dims,
		},
		Scheduler: SchedulerConfig{
			HeartbeatMinutes: 30,
		},
		Runtime: RuntimeConfig{
			Mode:    "local",
			Network: "",
		},
	}
}

// validRuntimeModes is the set of accepted values for XNC_RUNTIME.
var validRuntimeModes = map[string]bool{
	"local": true, "docker": true, "kubernetes": true,
}

// validLogLevels is the set of accepted values for XNC_LOG_LEVEL.
// Includes "warning" as a synonym for "warn" (matches logging.ParseLevel).
var validLogLevels = map[string]bool{
	"debug": true, "info": true, "warn": true, "warning": true, "error": true,
}

// networkNameRe validates Docker network names: alphanumeric, hyphens, underscores.
// NOTE: duplicated in agent.NetworkName() because agent cannot import config
// (import cycle). Keep both copies in sync — pattern: ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$
var networkNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ValidBaseURL reports whether u is a valid OpenAI-compatible base URL.
func ValidBaseURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// ValidLogLevel reports whether l is a recognised log level.
func ValidLogLevel(l string) bool { return validLogLevels[l] }

// ValidRuntimeMode reports whether m is a recognised runtime mode.
func ValidRuntimeMode(m string) bool { return validRuntimeModes[m] }

// ValidNetworkName reports whether n is a valid Docker network name.
func ValidNetworkName(n string) bool { return networkNameRe.MatchString(n) }

// ApplyEnvOverrides applies environment variable overrides on top of the
// loaded config. Priority: env var > config file > default.
// Only non-empty env vars take effect. Invalid values are logged to stderr
// and ignored.
func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("XNC_TELEGRAM_BOT_TOKEN"); v != "" {
		c.Telegram.BotToken = v
	}
	if v := os.Getenv("XNC_TELEGRAM_GROUP_ID"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			c.Telegram.GroupID = id
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_TELEGRAM_GROUP_ID=%q: %v\n", v, err)
		}
	}
	if v := os.Getenv("XNC_TELEGRAM_TOPIC_ID"); v != "" {
		if id, err := strconv.Atoi(v); err == nil {
			c.Telegram.TopicID = id
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_TELEGRAM_TOPIC_ID=%q: %v\n", v, err)
		}
	}
	if v := os.Getenv("XNC_OPENAI_API_KEY"); v != "" {
		c.OpenAI.APIKey = v
	}
	if v := os.Getenv("XNC_OPENAI_MODEL"); v != "" {
		c.OpenAI.Model = v
	}
	if v := os.Getenv("XNC_OPENAI_BASE_URL"); v != "" {
		if ValidBaseURL(v) {
			c.OpenAI.BaseURL = v
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_OPENAI_BASE_URL=%q (must start with http:// or https://)\n", v)
		}
	}
	if v := os.Getenv("XNC_PERSONA_NAME"); v != "" {
		c.Persona.Name = v
	}
	if v := os.Getenv("XNC_PERSONA_OWNER"); v != "" {
		c.Persona.OwnerName = v
	}
	if v := os.Getenv("XNC_LOG_LEVEL"); v != "" {
		if ValidLogLevel(v) {
			c.Logging.Level = v
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_LOG_LEVEL=%q (valid: debug, info, warn, error)\n", v)
		}
	}
	if v := os.Getenv("XNC_TELEGRAM_ALLOW_FROM"); v != "" {
		c.Telegram.AllowFrom = splitCSV(v)
	}
	if v := os.Getenv("XNC_RUNTIME"); v != "" {
		if ValidRuntimeMode(v) {
			c.Runtime.Mode = v
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_RUNTIME=%q (valid: local, docker, kubernetes)\n", v)
		}
	}
	if v := os.Getenv("XNC_NETWORK"); v != "" {
		if ValidNetworkName(v) {
			c.Runtime.Network = v
		} else {
			fmt.Fprintf(os.Stderr, "config: ignoring invalid XNC_NETWORK=%q (must be alphanumeric/hyphens/underscores, 1-64 chars)\n", v)
		}
	}
}

// splitCSV splits a comma-separated string, trimming whitespace from each element.
// Empty elements are discarded.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
