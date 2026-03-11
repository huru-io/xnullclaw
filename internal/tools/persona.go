package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
)

var personaPresets = map[string]config.PersonaDimensions{
	"professional": {Warmth: 0.4, Humor: 0.1, Verbosity: 0.3, Proactiveness: 0.5,
		Formality: 0.9, Empathy: 0.4, Sarcasm: 0.0, Autonomy: 0.4, Interpretation: 0.1, Creativity: 0.3},
	"casual": {Warmth: 0.8, Humor: 0.7, Verbosity: 0.5, Proactiveness: 0.6,
		Formality: 0.1, Empathy: 0.7, Sarcasm: 0.3, Autonomy: 0.6, Interpretation: 0.4, Creativity: 0.6},
	"assistant": {Warmth: 0.6, Humor: 0.3, Verbosity: 0.4, Proactiveness: 0.8,
		Formality: 0.5, Empathy: 0.5, Sarcasm: 0.0, Autonomy: 0.7, Interpretation: 0.2, Creativity: 0.4},
	"minimal": {Warmth: 0.2, Humor: 0.0, Verbosity: 0.1, Proactiveness: 0.3,
		Formality: 0.6, Empathy: 0.2, Sarcasm: 0.0, Autonomy: 0.3, Interpretation: 0.0, Creativity: 0.2},
	"creative": {Warmth: 0.7, Humor: 0.6, Verbosity: 0.6, Proactiveness: 0.7,
		Formality: 0.2, Empathy: 0.6, Sarcasm: 0.2, Autonomy: 0.8, Interpretation: 0.5, Creativity: 0.9},
	"friendly": {Warmth: 0.7, Humor: 0.4, Verbosity: 0.4, Proactiveness: 0.6,
		Formality: 0.3, Empathy: 0.6, Sarcasm: 0.1, Autonomy: 0.5, Interpretation: 0.2, Creativity: 0.4},
	"analytical": {Warmth: 0.4, Humor: 0.2, Verbosity: 0.5, Proactiveness: 0.5,
		Formality: 0.6, Empathy: 0.3, Sarcasm: 0.0, Autonomy: 0.4, Interpretation: 0.1, Creativity: 0.3},
	"witty": {Warmth: 0.5, Humor: 0.7, Verbosity: 0.2, Proactiveness: 0.6,
		Formality: 0.4, Empathy: 0.4, Sarcasm: 0.3, Autonomy: 0.6, Interpretation: 0.3, Creativity: 0.6},
	"earnest": {Warmth: 0.7, Humor: 0.3, Verbosity: 0.5, Proactiveness: 0.8,
		Formality: 0.5, Empathy: 0.7, Sarcasm: 0.0, Autonomy: 0.7, Interpretation: 0.2, Creativity: 0.4},
	"playful": {Warmth: 0.6, Humor: 0.6, Verbosity: 0.3, Proactiveness: 0.7,
		Formality: 0.2, Empathy: 0.5, Sarcasm: 0.2, Autonomy: 0.7, Interpretation: 0.4, Creativity: 0.9},
}

var validTTSVoices = map[string]bool{
	"alloy": true, "ash": true, "ballad": true, "coral": true,
	"echo": true, "fable": true, "onyx": true, "nova": true,
	"sage": true, "shimmer": true,
}

var dimensionDescriptions = map[string]string{
	"warmth":         "How warm and friendly the tone is (0=cold, 1=very warm)",
	"humor":          "How much humor to inject (0=none, 1=frequent jokes)",
	"verbosity":      "Response length tendency (0=terse, 1=very detailed)",
	"proactiveness":  "How proactively to offer suggestions (0=only when asked, 1=always suggesting)",
	"formality":      "Language formality (0=very casual, 1=very formal)",
	"empathy":        "Emotional awareness and support (0=matter-of-fact, 1=very empathetic)",
	"sarcasm":        "Sarcasm and wit level (0=none, 1=heavy sarcasm)",
	"autonomy":       "How much to act independently (0=always ask, 1=just do it)",
	"interpretation": "How freely to interpret ambiguous requests (0=literal, 1=creative)",
	"creativity":     "Creative vs conventional responses (0=conventional, 1=highly creative)",
}

func registerPersonaTools(r *Registry, d Deps) {
	// set_persona
	r.Register(
		Definition{
			Name:        "set_persona",
			Description: "Update a persona text field: name, language, bio, or extra_instructions",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "description": "Field to update", "enum": []string{"name", "owner_name", "language", "bio", "extra_instructions", "tts_voice"}},
					"value": map[string]any{"type": "string", "description": "New value"},
				},
				"required": []string{"field", "value"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			field, err := stringArg(args, "field")
			if err != nil {
				return "", err
			}
			value, err := stringArg(args, "value")
			if err != nil {
				return "", err
			}

			var oldValue string
			switch field {
			case "name":
				oldValue = d.Cfg.Persona.Name
				d.Cfg.Persona.Name = value
			case "owner_name":
				oldValue = d.Cfg.Persona.OwnerName
				d.Cfg.Persona.OwnerName = value
			case "language":
				d.Cfg.Persona.Language = value
			case "bio":
				d.Cfg.Persona.Bio = value
			case "extra_instructions":
				d.Cfg.Persona.ExtraInstructions = value
			case "tts_voice":
				if !validTTSVoices[value] {
					return "", fmt.Errorf("invalid TTS voice: %s", value)
				}
				d.Cfg.Voice.TTSVoice = value
				d.Cfg.OpenAI.TTSVoice = value
			default:
				return "", fmt.Errorf("invalid persona field: %s", field)
			}
			if err := d.Cfg.Save(d.CfgPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}

			// Broadcast identity changes to all running agents.
			if (field == "name" || field == "owner_name") && oldValue != "" && oldValue != value {
				var notice string
				if field == "name" {
					notice = fmt.Sprintf("Notice: the orchestrator's name has changed from %s to %s.", oldValue, value)
				} else {
					notice = fmt.Sprintf("Notice: the human controller's name has changed from %s to %s.", oldValue, value)
				}
				// Best-effort broadcast.
				prefix := agent.ContainerPrefix(d.Home)
				if containers, err := d.Docker.ListContainers(ctx, prefix); err == nil {
					for _, c := range containers {
						if c.State == "running" {
							n := strings.TrimPrefix(c.Name, prefix)
							sendToAgent(ctx, d, n, notice)
						}
					}
				}
			}

			return fmt.Sprintf("Persona %s updated to: %s", field, value), nil
		},
	)

	// set_persona_dimension
	r.Register(
		Definition{
			Name:        "set_persona_dimension",
			Description: "Set a personality dimension (0.0-1.0)",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dimension": map[string]any{"type": "string", "description": "Dimension name"},
					"value":     map[string]any{"type": "number", "description": "Value between 0.0 and 1.0"},
				},
				"required": []string{"dimension", "value"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			dimension, err := stringArg(args, "dimension")
			if err != nil {
				return "", err
			}
			value, err := float64Arg(args, "value")
			if err != nil {
				return "", err
			}
			if value < 0.0 || value > 1.0 {
				return "", fmt.Errorf("value must be between 0.0 and 1.0")
			}

			desc, valid := dimensionDescriptions[dimension]
			if !valid {
				return "", fmt.Errorf("invalid dimension: %s", dimension)
			}

			dims := &d.Cfg.Persona.Dimensions
			switch dimension {
			case "warmth":
				dims.Warmth = value
			case "humor":
				dims.Humor = value
			case "verbosity":
				dims.Verbosity = value
			case "proactiveness":
				dims.Proactiveness = value
			case "formality":
				dims.Formality = value
			case "empathy":
				dims.Empathy = value
			case "sarcasm":
				dims.Sarcasm = value
			case "autonomy":
				dims.Autonomy = value
			case "interpretation":
				dims.Interpretation = value
			case "creativity":
				dims.Creativity = value
			}

			if err := d.Cfg.Save(d.CfgPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("%s set to %.2f — %s", dimension, value, desc), nil
		},
	)

	// get_persona
	r.Register(
		Definition{
			Name:        "get_persona",
			Description: "Return full persona config: text fields and all dimension values",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			output := struct {
				config.PersonaConfig
				TTSVoice   string `json:"tts_voice"`
				TTSEnabled bool   `json:"tts_enabled"`
			}{
				PersonaConfig: d.Cfg.Persona,
				TTSVoice:      d.Cfg.Voice.TTSVoice,
				TTSEnabled:    d.Cfg.Voice.TTSEnabled,
			}
			data, err := json.MarshalIndent(output, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// reset_persona
	r.Register(
		Definition{
			Name:        "reset_persona",
			Description: "Reset all persona fields and dimensions to defaults",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			defaults := config.DefaultConfig()
			d.Cfg.Persona = defaults.Persona
			if err := d.Cfg.Save(d.CfgPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return "Persona reset to defaults", nil
		},
	)

	// apply_persona_preset
	r.Register(
		Definition{
			Name:        "apply_persona_preset",
			Description: "Apply a named persona preset",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"preset": map[string]any{"type": "string", "description": "Preset name", "enum": []string{
						"professional", "casual", "assistant", "minimal", "creative",
						"friendly", "analytical", "witty", "earnest", "playful",
					}},
				},
				"required": []string{"preset"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			preset, err := stringArg(args, "preset")
			if err != nil {
				return "", err
			}
			dims, ok := personaPresets[preset]
			if !ok {
				return "", fmt.Errorf("invalid preset: %s", preset)
			}
			d.Cfg.Persona.Dimensions = dims
			if err := d.Cfg.Save(d.CfgPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("Applied persona preset: %s", preset), nil
		},
	)

	// list_voices
	r.Register(
		Definition{
			Name:        "list_voices",
			Description: "List available TTS voices and show which one is currently active",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			type voiceInfo struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Active      bool   `json:"active,omitempty"`
			}
			voices := []voiceInfo{
				{"alloy", "Neutral, balanced", false},
				{"ash", "Warm, conversational", false},
				{"ballad", "Soft, gentle", false},
				{"coral", "Clear, expressive", false},
				{"echo", "Smooth, authoritative", false},
				{"fable", "Storytelling, animated", false},
				{"nova", "Friendly, upbeat (default)", false},
				{"onyx", "Deep, resonant", false},
				{"sage", "Calm, thoughtful", false},
				{"shimmer", "Bright, energetic", false},
			}
			current := d.Cfg.Voice.TTSVoice
			for i := range voices {
				if voices[i].Name == current {
					voices[i].Active = true
				}
			}
			data, err := json.MarshalIndent(voices, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)
}
