// Package tools — persona.go implements the persona adjustment tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jotavich/xnullclaw/mux/config"
)

// personaPresets maps preset names to dimension values.
var personaPresets = map[string]config.PersonaDimensions{
	"professional": {
		Warmth: 0.4, Humor: 0.1, Verbosity: 0.3, Proactiveness: 0.5,
		Formality: 0.9, Empathy: 0.4, Sarcasm: 0.0, Autonomy: 0.4,
		Interpretation: 0.1, Creativity: 0.3,
	},
	"casual": {
		Warmth: 0.8, Humor: 0.7, Verbosity: 0.5, Proactiveness: 0.6,
		Formality: 0.1, Empathy: 0.7, Sarcasm: 0.3, Autonomy: 0.6,
		Interpretation: 0.4, Creativity: 0.6,
	},
	"assistant": {
		Warmth: 0.6, Humor: 0.3, Verbosity: 0.4, Proactiveness: 0.8,
		Formality: 0.5, Empathy: 0.5, Sarcasm: 0.0, Autonomy: 0.7,
		Interpretation: 0.2, Creativity: 0.4,
	},
	"minimal": {
		Warmth: 0.2, Humor: 0.0, Verbosity: 0.1, Proactiveness: 0.3,
		Formality: 0.6, Empathy: 0.2, Sarcasm: 0.0, Autonomy: 0.3,
		Interpretation: 0.0, Creativity: 0.2,
	},
	"creative": {
		Warmth: 0.7, Humor: 0.6, Verbosity: 0.6, Proactiveness: 0.7,
		Formality: 0.2, Empathy: 0.6, Sarcasm: 0.2, Autonomy: 0.8,
		Interpretation: 0.5, Creativity: 0.9,
	},
}

// dimensionDescriptions maps dimension names to descriptions of their effect.
var dimensionDescriptions = map[string]string{
	"warmth":         "How warm and friendly the tone is (0=cold, 1=very warm)",
	"humor":          "How much humor to inject (0=none, 1=frequent jokes)",
	"verbosity":      "Response length tendency (0=terse, 1=very detailed)",
	"proactiveness":  "How proactively to offer suggestions (0=only when asked, 1=always suggesting)",
	"formality":      "Language formality (0=very casual, 1=very formal)",
	"empathy":        "Emotional awareness and support (0=matter-of-fact, 1=very empathetic)",
	"sarcasm":        "Sarcasm and wit level (0=none, 1=heavy sarcasm)",
	"autonomy":       "How much to act independently vs ask for confirmation (0=always ask, 1=just do it)",
	"interpretation": "How freely to interpret ambiguous requests (0=literal, 1=creative interpretation)",
	"creativity":     "Creative vs conventional responses (0=conventional, 1=highly creative)",
}

func registerPersonaTools(r *Registry, cfg *config.Config, configPath string) {
	// -----------------------------------------------------------------------
	// set_persona
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "set_persona",
			Description: "Update a persona text field: name, language, bio, or extra_instructions",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": map[string]any{"type": "string", "description": "Field to update", "enum": []string{"name", "language", "bio", "extra_instructions"}},
					"value": map[string]any{"type": "string", "description": "New value for the field"},
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
			switch field {
			case "name":
				cfg.Persona.Name = value
			case "language":
				cfg.Persona.Language = value
			case "bio":
				cfg.Persona.Bio = value
			case "extra_instructions":
				cfg.Persona.ExtraInstructions = value
			default:
				return "", fmt.Errorf("invalid persona field: %s (must be one of: name, language, bio, extra_instructions)", field)
			}
			if err := cfg.Save(configPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("Persona %s updated to: %s", field, value), nil
		},
	)

	// -----------------------------------------------------------------------
	// set_persona_dimension
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "set_persona_dimension",
			Description: "Set a personality dimension (0.0-1.0). Dimensions: warmth, humor, verbosity, proactiveness, formality, empathy, sarcasm, autonomy, interpretation, creativity",
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
				return "", fmt.Errorf("value must be between 0.0 and 1.0, got: %f", value)
			}

			desc, valid := dimensionDescriptions[dimension]
			if !valid {
				return "", fmt.Errorf("invalid dimension: %s (valid: warmth, humor, verbosity, proactiveness, formality, empathy, sarcasm, autonomy, interpretation, creativity)", dimension)
			}

			d := &cfg.Persona.Dimensions
			switch dimension {
			case "warmth":
				d.Warmth = value
			case "humor":
				d.Humor = value
			case "verbosity":
				d.Verbosity = value
			case "proactiveness":
				d.Proactiveness = value
			case "formality":
				d.Formality = value
			case "empathy":
				d.Empathy = value
			case "sarcasm":
				d.Sarcasm = value
			case "autonomy":
				d.Autonomy = value
			case "interpretation":
				d.Interpretation = value
			case "creativity":
				d.Creativity = value
			}

			if err := cfg.Save(configPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("%s set to %.2f — %s", dimension, value, desc), nil
		},
	)

	// -----------------------------------------------------------------------
	// get_persona
	// -----------------------------------------------------------------------
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
			data, err := json.MarshalIndent(cfg.Persona, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal persona: %w", err)
			}
			return string(data), nil
		},
	)

	// -----------------------------------------------------------------------
	// reset_persona
	// -----------------------------------------------------------------------
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
			cfg.Persona = defaults.Persona
			if err := cfg.Save(configPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return "Persona reset to defaults", nil
		},
	)

	// -----------------------------------------------------------------------
	// apply_persona_preset
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "apply_persona_preset",
			Description: "Apply a named persona preset: professional, casual, assistant, minimal, creative",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"preset": map[string]any{"type": "string", "description": "Preset name", "enum": []string{"professional", "casual", "assistant", "minimal", "creative"}},
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
				return "", fmt.Errorf("invalid preset: %s (valid: professional, casual, assistant, minimal, creative)", preset)
			}
			cfg.Persona.Dimensions = dims
			if err := cfg.Save(configPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return fmt.Sprintf("Applied persona preset: %s", preset), nil
		},
	)
}
