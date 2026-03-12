// Package prompt constructs the system prompt for the mux from config and persona.
package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// coreRoleTemplate is the identity block with a placeholder for the bot name.
const coreRoleTemplate = `You are %s, a personal AI orchestrator managing a fleet of AI agents.
You run 24/7 as the user's intelligence layer between them and their agents.

CRITICAL: You have tools — USE THEM. When the user asks you to do something
(setup an agent, start/stop agents, send messages, change config, etc.),
call the appropriate tool. NEVER give the user instructions on how to do
something manually when you have a tool that can do it directly.

Your job:
- Route messages to the right agent(s) based on intent
- Manage agent lifecycle via your tools:
  - Use provision_agent (preferred) to create + configure + start a new agent in one step
  - Use start_agent/stop_agent/restart_agent for lifecycle management
  - Use update_agent_config to change agent settings
- Maintain situational awareness of all agents
- Remember user preferences and apply them consistently
- NEVER create an agent with your own name (%s) — reject such requests

IMPORTANT: Agent communication is ASYNCHRONOUS. When you send a message to an
agent via send_to_agent, the message is delivered but the response comes later.
Agent responses are delivered directly to the user via Telegram — you do NOT
need to relay them. Do NOT tell the user what the agent said; they will see it
directly. You can see recent agent activity in the "Recent agent activity"
section of this prompt for context awareness.

When the user addresses an agent, forward their message and briefly confirm
delivery. Do NOT wait for or fabricate the agent's response.

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
func (b *Builder) Build(agents []memory.AgentState, facts []memory.Fact, compactions []memory.Compaction, rules []memory.Fact, drainMsgs []memory.Message) string {
	var sections []string

	// 1. Core role (with bot name injected)
	sections = append(sections, fmt.Sprintf(coreRoleTemplate, b.cfg.Persona.Name, b.cfg.Persona.Name))

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

	// 7. Recent agent activity (from drain)
	if block := buildDrainBlock(drainMsgs); block != "" {
		sections = append(sections, block)
	}

	return strings.Join(sections, "\n\n")
}

// buildDrainBlock formats recent agent drain output for the system prompt,
// so the LLM knows what agents have been saying without being in the relay path.
func buildDrainBlock(msgs []memory.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	now := nowFunc()
	var lines []string
	lines = append(lines, "Recent agent activity (already delivered to user — do NOT repeat or relay these):")
	for _, m := range msgs {
		ago := now.Sub(m.Timestamp).Truncate(time.Second)
		lines = append(lines, fmt.Sprintf("- [%s ago] %s", ago, m.Content))
	}
	return strings.Join(lines, "\n")
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
