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

// Dimension descriptors and picker are in config/dimensions.go (single source of truth).

// Content limits for dynamic sections injected into the system prompt.
// These prevent prompt bloat and limit the surface area for injection.
const (
	maxFactLen      = 500  // max runes per fact/rule entry
	maxDrainMsgLen  = 400  // max runes per drain message
	maxCompactionLen = 800 // max runes per compaction summary
)

// sanitizeEntry strips control characters (including newlines) and truncates
// to maxRunes. Newlines are collapsed to spaces to prevent injected closing
// tags (e.g. "</facts>") from breaking out of XML-delimited prompt sections.
func sanitizeEntry(s string, maxRunes int) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(' ')
		} else if r < 32 {
			continue // strip other control characters
		} else {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()

	runes := []rune(cleaned)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return cleaned
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
// Content is wrapped in XML delimiters and sanitized to limit injection surface.
func buildDrainBlock(msgs []memory.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	now := nowFunc()
	var lines []string
	lines = append(lines, "<agent-activity>")
	lines = append(lines, "Recent agent activity (already delivered to user — do NOT repeat or relay these):")
	for _, m := range msgs {
		ago := now.Sub(m.Timestamp).Truncate(time.Second)
		lines = append(lines, fmt.Sprintf("- [%s ago] %s", ago, sanitizeEntry(m.Content, maxDrainMsgLen)))
	}
	lines = append(lines, "</agent-activity>")
	return strings.Join(lines, "\n")
}

// buildPersonaBlock generates natural language behavioral instructions from persona config.
func (b *Builder) buildPersonaBlock() string {
	values := config.DimensionValues(b.cfg.Persona.Dimensions)

	var lines []string
	lines = append(lines, "Your communication style:")
	for i, desc := range config.DimensionDescriptors {
		lines = append(lines, "- "+config.PickDescription(values[i], desc))
	}

	var extra []string
	extra = append(extra, fmt.Sprintf("Your name is %s.", sanitizeEntry(b.cfg.Persona.Name, 50)))
	if b.cfg.Persona.Bio != "" {
		extra = append(extra, sanitizeEntry(b.cfg.Persona.Bio, 500))
	}
	extra = append(extra, fmt.Sprintf("Respond in %s.", sanitizeEntry(b.cfg.Persona.Language, 30)))
	if b.cfg.Persona.ExtraInstructions != "" {
		extra = append(extra, sanitizeEntry(b.cfg.Persona.ExtraInstructions, 1000))
	}

	return strings.Join(lines, "\n") + "\n\n" + strings.Join(extra, "\n")
}

// buildRulesBlock formats passthrough rules from facts of type "rule".
// Content is wrapped in XML delimiters and sanitized to limit injection surface.
func buildRulesBlock(rules []memory.Fact) string {
	if len(rules) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "<rules>")
	lines = append(lines, "Active passthrough rules:")
	for _, r := range rules {
		scope := "global"
		if r.Agent != nil && *r.Agent != "" {
			scope = *r.Agent
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", scope, sanitizeEntry(r.Content, maxFactLen)))
	}
	lines = append(lines, "</rules>")
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
			emoji := sanitizeEntry(derefOr(a.Emoji, "?"), 4)
			role := sanitizeEntry(derefOr(a.Role, "unknown role"), 100)
			status := sanitizeEntry(derefOr(a.Status, "unknown"), 20)
			model := sanitizeEntry(derefOr(a.Model, "unknown"), 50)

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
			sanitizedWord := sanitizeEntry(word, 50)
			var sanitizedCorrections []string
			for _, c := range corrections {
				sanitizedCorrections = append(sanitizedCorrections, sanitizeEntry(c, 50))
			}
			pairs = append(pairs, fmt.Sprintf("%s -> %s", sanitizedWord, strings.Join(sanitizedCorrections, ", ")))
		}
		lines = append(lines, fmt.Sprintf("Correction dictionary: %s", strings.Join(pairs, "; ")))
	}

	return strings.Join(lines, "\n")
}

// buildFactsBlock formats long-term facts for the system prompt.
// Content is wrapped in XML delimiters and sanitized to limit injection surface.
func buildFactsBlock(facts []memory.Fact) string {
	if len(facts) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "<facts>")
	lines = append(lines, "Known facts:")
	for _, f := range facts {
		source := "unknown"
		if f.Source != nil && *f.Source != "" {
			source = *f.Source
		}
		lines = append(lines, fmt.Sprintf("- %s (source: %s, relevance: %.2f)", sanitizeEntry(f.Content, maxFactLen), source, f.Score))
	}
	lines = append(lines, "</facts>")
	return strings.Join(lines, "\n")
}

// buildCompactionsBlock formats recent compaction summaries.
// Content is wrapped in XML delimiters and sanitized to limit injection surface.
func buildCompactionsBlock(compactions []memory.Compaction) string {
	if len(compactions) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "<history>")
	lines = append(lines, "Recent history:")
	for _, c := range compactions {
		lines = append(lines, fmt.Sprintf("[%s to %s] %s",
			c.PeriodStart.Format(time.RFC3339),
			c.PeriodEnd.Format(time.RFC3339),
			sanitizeEntry(c.Summary, maxCompactionLen)))
	}
	lines = append(lines, "</history>")
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
