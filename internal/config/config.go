// Package config handles loading, saving, and providing defaults for mux configuration.
package config

import (
	"encoding/json"
	"os"
)

// Config is the top-level configuration matching PRD 5.3.
type Config struct {
	Telegram TelegramConfig `json:"telegram"`
	OpenAI   OpenAIConfig   `json:"openai"`
	Agents   AgentsConfig   `json:"agents"`
	Voice    VoiceConfig    `json:"voice"`
	Memory   MemoryConfig   `json:"memory"`
	Costs    CostsConfig    `json:"costs"`
	Logging  LoggingConfig  `json:"logging"`
	Persona  PersonaConfig  `json:"persona"`
}

// PersonaConfig defines the bot's personality.
type PersonaConfig struct {
	Name              string            `json:"name"`
	OwnerName         string            `json:"owner_name"`
	Language          string            `json:"language"`
	Bio               string            `json:"bio"`
	ExtraInstructions string            `json:"extra_instructions"`
	Dimensions        PersonaDimensions `json:"dimensions"`
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
	TopicID   int      `json:"topic_id,omitempty"`  // -1 = discover, 0 = no topic, 1 = General, N = specific
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

// AgentsConfig holds agent routing settings.
type AgentsConfig struct {
	Default    string                   `json:"default"`
	AutoStart  []string                 `json:"auto_start"`
	MuxManaged []string                 `json:"mux_managed"`
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
	SummaryIntervalMessages int    `json:"summary_interval_messages"`
}

// CostsConfig holds budget and cost tracking settings.
type CostsConfig struct {
	Track              bool    `json:"track"`
	MonthlyBudgetUSD   float64 `json:"monthly_budget_usd"`
	DailyBudgetUSD     float64 `json:"daily_budget_usd"`
	WarnAtPercent      int     `json:"warn_at_percent"`
	PerAgentDailyLimit float64 `json:"per_agent_daily_limit_usd"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level      string `json:"level"`
	Dir        string `json:"dir"`
	RotateDays int    `json:"rotate_days"`
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
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
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
			Name:              "mux",
			OwnerName:         "Controller",
			Language:          "en",
			Bio:               "",
			ExtraInstructions: "",
			Dimensions:        dims,
		},
	}
}
