// Package prompt constructs the system prompt for the mux from config and persona.
package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/memory"
)

// coreRole is the fixed identity block always present in the system prompt.
const coreRole = `You are a personal AI orchestrator managing a fleet of AI agents.
You run 24/7 as the user's intelligence layer between them and their agents.

Your job:
- Route messages to the right agent(s) based on intent
- Manage agent lifecycle (start, stop, create, configure, clone, destroy)
- Synthesize multi-agent responses when useful
- Maintain situational awareness of all agents
- Remember user preferences and apply them consistently
- Be transparent: always prefix agent output with their emoji + name

When the user addresses an agent, be a transparent pipe — forward the message
with minimal intervention unless passthrough rules say otherwise.

When the user talks to YOU, respond directly. You handle:
- Agent management, system status, multi-agent coordination
- Memory/preferences, voice/TTS settings, cost reporting`

// dimensionDesc holds the low / mid / high text descriptions for a single dimension.
type dimensionDesc struct {
	name string
	low  string
	mid  string
	high string
}

// dimensionDescriptors defines the 3-tier description for each persona dimension.
var dimensionDescriptors = []dimensionDesc{
	{"warmth", "Be clinical and matter-of-fact", "Be friendly but professional", "Be warm, caring, and personal"},
	{"humor", "Never joke or use humor", "Use occasional humor when appropriate", "Be playful, use jokes and wit freely"},
	{"verbosity", "Be extremely terse — minimum words", "Balance brevity and detail", "Be thorough and detailed in explanations"},
	{"proactiveness", "Only respond when explicitly asked", "Suggest actions when clearly relevant", "Actively anticipate needs and volunteer information"},
	{"formality", "Be casual, slang is fine", "Professional but relaxed", "Be formal and proper at all times"},
	{"empathy", "Be matter-of-fact, skip emotional acknowledgment", "Acknowledge feelings when relevant", "Be emotionally attuned and supportive"},
	{"sarcasm", "Never be sarcastic", "Light irony occasionally", "Use sharp wit and sarcasm freely"},
	{"autonomy", "Always ask before taking action", "Act on clear intent, ask when ambiguous", "Take initiative freely, act first"},
	{"interpretation", "Pass user messages through completely raw", "Fix obvious typos silently", "Actively refine and clarify messages before forwarding"},
	{"creativity", "Be straightforward and predictable", "Balance conventional and novel approaches", "Prefer creative and surprising solutions"},
}

// Presets returns the named dimension presets as described in the PRD.
var Presets = map[string]config.PersonaDimensions{
	"professional": {Formality: 0.8, Humor: 0.1, Sarcasm: 0.0, Verbosity: 0.4, Warmth: 0.3,
		Proactiveness: 0.7, Empathy: 0.5, Autonomy: 0.6, Interpretation: 0.2, Creativity: 0.5},
	"casual": {Formality: 0.2, Humor: 0.6, Sarcasm: 0.3, Verbosity: 0.3, Warmth: 0.7,
		Proactiveness: 0.7, Empathy: 0.5, Autonomy: 0.6, Interpretation: 0.2, Creativity: 0.5},
	"assistant": {Proactiveness: 0.8, Autonomy: 0.7, Empathy: 0.6, Verbosity: 0.4,
		Warmth: 0.6, Humor: 0.4, Formality: 0.4, Sarcasm: 0.2, Interpretation: 0.2, Creativity: 0.5},
	"minimal": {Verbosity: 0.1, Humor: 0.0, Sarcasm: 0.0, Proactiveness: 0.3,
		Warmth: 0.6, Formality: 0.4, Empathy: 0.5, Autonomy: 0.6, Interpretation: 0.2, Creativity: 0.5},
	"creative": {Creativity: 0.9, Humor: 0.5, Interpretation: 0.6, Autonomy: 0.7,
		Warmth: 0.6, Verbosity: 0.3, Proactiveness: 0.7, Formality: 0.4, Empathy: 0.5, Sarcasm: 0.2},
}

// nowFunc is the function used to get the current time. Tests can override it.
var nowFunc = time.Now

// Builder assembles the dynamic system prompt for each model call.
type Builder struct {
	cfg *config.Config
}

// New creates a prompt Builder from the given configuration.
func New(cfg *config.Config) *Builder {
	return &Builder{cfg: cfg}
}

// Build assembles the full system prompt from all dynamic sources.
func (b *Builder) Build(agents []memory.AgentState, facts []memory.Fact, compactions []memory.Compaction, rules []memory.Fact) string {
	var sections []string

	// 1. Core role
	sections = append(sections, coreRole)

	// 2. Persona
	sections = append(sections, b.buildPersonaBlock())

	// 3. Passthrough rules
	if block := buildRulesBlock(rules); block != "" {
		sections = append(sections, block)
	}

	// 4. Agent roster
	sections = append(sections, b.buildRosterBlock(agents))

	// 5. Long-term facts
	if block := buildFactsBlock(facts); block != "" {
		sections = append(sections, block)
	}

	// 6. Recent compaction summaries
	if block := buildCompactionsBlock(compactions); block != "" {
		sections = append(sections, block)
	}

	return strings.Join(sections, "\n\n")
}

// buildPersonaBlock generates natural language behavioral instructions from persona config.
func (b *Builder) buildPersonaBlock() string {
	dims := b.cfg.Persona.Dimensions
	values := dimensionValues(dims)

	var lines []string
	lines = append(lines, "Your communication style:")
	for i, desc := range dimensionDescriptors {
		lines = append(lines, "- "+pickDescription(values[i], desc))
	}

	var extra []string
	extra = append(extra, fmt.Sprintf("Your name is %s.", b.cfg.Persona.Name))
	if b.cfg.Persona.Bio != "" {
		extra = append(extra, b.cfg.Persona.Bio)
	}
	extra = append(extra, fmt.Sprintf("Respond in %s.", b.cfg.Persona.Language))
	if b.cfg.Persona.ExtraInstructions != "" {
		extra = append(extra, b.cfg.Persona.ExtraInstructions)
	}

	return strings.Join(lines, "\n") + "\n\n" + strings.Join(extra, "\n")
}

// dimensionValues returns the dimension values in the same order as dimensionDescriptors.
func dimensionValues(d config.PersonaDimensions) []float64 {
	return []float64{
		d.Warmth,
		d.Humor,
		d.Verbosity,
		d.Proactiveness,
		d.Formality,
		d.Empathy,
		d.Sarcasm,
		d.Autonomy,
		d.Interpretation,
		d.Creativity,
	}
}

// pickDescription selects the low / mid / high description based on value thresholds.
// low: < 0.33, mid: 0.33-0.66, high: > 0.66.
func pickDescription(value float64, desc dimensionDesc) string {
	switch {
	case value < 0.33:
		return desc.low
	case value > 0.66:
		return desc.high
	default:
		return desc.mid
	}
}

// buildRulesBlock formats passthrough rules from facts of type "rule".
func buildRulesBlock(rules []memory.Fact) string {
	if len(rules) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Active passthrough rules:")
	for _, r := range rules {
		scope := "global"
		if r.Agent != nil && *r.Agent != "" {
			scope = *r.Agent
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", scope, r.Content))
	}
	return strings.Join(lines, "\n")
}

// buildRosterBlock formats the current agent roster with live state.
func (b *Builder) buildRosterBlock(agents []memory.AgentState) string {
	now := nowFunc()
	var lines []string
	lines = append(lines, fmt.Sprintf("Current time: %s", now.Format(time.RFC3339)))

	if len(agents) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Active agents:")
		for _, a := range agents {
			emoji := derefOr(a.Emoji, "?")
			role := derefOr(a.Role, "unknown role")
			status := derefOr(a.Status, "unknown")
			model := derefOr(a.Model, "unknown")

			uptime := "unknown"
			lastMsg := "never"
			if a.LastInteraction != nil {
				uptime = RelativeTime(now, a.Updated)
				lastMsg = RelativeTime(now, *a.LastInteraction)
			}

			lines = append(lines, fmt.Sprintf("  %s %s — %s (%s, %s, uptime %s, last msg %s)",
				emoji, a.Agent, role, status, model, uptime, lastMsg))
		}
	}

	if b.cfg.Agents.Default != "" {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("Default agent: %s", b.cfg.Agents.Default))
	}

	if len(b.cfg.Voice.CorrectionDict) > 0 {
		var pairs []string
		for word, corrections := range b.cfg.Voice.CorrectionDict {
			pairs = append(pairs, fmt.Sprintf("%s -> %s", word, strings.Join(corrections, ", ")))
		}
		lines = append(lines, fmt.Sprintf("Correction dictionary: %s", strings.Join(pairs, "; ")))
	}

	return strings.Join(lines, "\n")
}

// buildFactsBlock formats long-term facts for the system prompt.
func buildFactsBlock(facts []memory.Fact) string {
	if len(facts) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Known facts:")
	for _, f := range facts {
		source := "unknown"
		if f.Source != nil && *f.Source != "" {
			source = *f.Source
		}
		lines = append(lines, fmt.Sprintf("- %s (source: %s, relevance: %.2f)", f.Content, source, f.Score))
	}
	return strings.Join(lines, "\n")
}

// buildCompactionsBlock formats recent compaction summaries.
func buildCompactionsBlock(compactions []memory.Compaction) string {
	if len(compactions) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "Recent history:")
	for _, c := range compactions {
		lines = append(lines, fmt.Sprintf("[%s to %s] %s",
			c.PeriodStart.Format(time.RFC3339),
			c.PeriodEnd.Format(time.RFC3339),
			c.Summary))
	}
	return strings.Join(lines, "\n")
}

// RelativeTime returns a human-readable relative time string (e.g. "5m ago", "2h ago").
func RelativeTime(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd ago", days)
	}
}

// derefOr dereferences a *string, returning fallback if nil or empty.
func derefOr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}
