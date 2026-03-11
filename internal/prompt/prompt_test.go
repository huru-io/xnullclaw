package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/memory"
)

func strPtr(s string) *string { return &s }
func timePtr(t time.Time) *time.Time { return &t }

func defaultTestConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Persona.Name = "mux"
	cfg.Persona.Language = "en"
	return cfg
}

// ---------- Persona dimension tests ----------

func TestPickDescription_Low(t *testing.T) {
	desc := dimensionDesc{name: "warmth", low: "cold", mid: "friendly", high: "warm"}
	if got := pickDescription(0.0, desc); got != "cold" {
		t.Errorf("pickDescription(0.0) = %q, want %q", got, "cold")
	}
	if got := pickDescription(0.1, desc); got != "cold" {
		t.Errorf("pickDescription(0.1) = %q, want %q", got, "cold")
	}
	if got := pickDescription(0.32, desc); got != "cold" {
		t.Errorf("pickDescription(0.32) = %q, want %q", got, "cold")
	}
}

func TestPickDescription_Mid(t *testing.T) {
	desc := dimensionDesc{name: "warmth", low: "cold", mid: "friendly", high: "warm"}
	if got := pickDescription(0.33, desc); got != "friendly" {
		t.Errorf("pickDescription(0.33) = %q, want %q", got, "friendly")
	}
	if got := pickDescription(0.5, desc); got != "friendly" {
		t.Errorf("pickDescription(0.5) = %q, want %q", got, "friendly")
	}
	if got := pickDescription(0.66, desc); got != "friendly" {
		t.Errorf("pickDescription(0.66) = %q, want %q", got, "friendly")
	}
}

func TestPickDescription_High(t *testing.T) {
	desc := dimensionDesc{name: "warmth", low: "cold", mid: "friendly", high: "warm"}
	if got := pickDescription(0.67, desc); got != "warm" {
		t.Errorf("pickDescription(0.67) = %q, want %q", got, "warm")
	}
	if got := pickDescription(1.0, desc); got != "warm" {
		t.Errorf("pickDescription(1.0) = %q, want %q", got, "warm")
	}
}

func TestBuildPersonaBlock_ContainsName(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Persona.Name = "Nova"
	b := New(cfg)
	block := b.buildPersonaBlock()
	if !strings.Contains(block, "Your name is Nova.") {
		t.Errorf("persona block missing name, got:\n%s", block)
	}
}

func TestBuildPersonaBlock_ContainsLanguage(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Persona.Language = "es"
	b := New(cfg)
	block := b.buildPersonaBlock()
	if !strings.Contains(block, "Respond in es.") {
		t.Errorf("persona block missing language, got:\n%s", block)
	}
}

func TestBuildPersonaBlock_Bio(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Persona.Bio = "I am a helpful orchestrator."
	b := New(cfg)
	block := b.buildPersonaBlock()
	if !strings.Contains(block, "I am a helpful orchestrator.") {
		t.Errorf("persona block missing bio, got:\n%s", block)
	}
}

func TestBuildPersonaBlock_EmptyBio(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Persona.Bio = ""
	b := New(cfg)
	block := b.buildPersonaBlock()
	if strings.Contains(block, "\n\n\n") {
		t.Errorf("persona block has extra blank lines for empty bio")
	}
}

func TestBuildPersonaBlock_ExtraInstructions(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Persona.ExtraInstructions = "Always greet the user."
	b := New(cfg)
	block := b.buildPersonaBlock()
	if !strings.Contains(block, "Always greet the user.") {
		t.Errorf("persona block missing extra instructions, got:\n%s", block)
	}
}

func TestBuildPersonaBlock_DimensionMapping(t *testing.T) {
	cfg := defaultTestConfig()
	// Set warmth high, humor low
	cfg.Persona.Dimensions.Warmth = 0.9
	cfg.Persona.Dimensions.Humor = 0.1
	b := New(cfg)
	block := b.buildPersonaBlock()

	if !strings.Contains(block, "Be warm, caring, and personal") {
		t.Errorf("expected high warmth description, got:\n%s", block)
	}
	if !strings.Contains(block, "Never joke or use humor") {
		t.Errorf("expected low humor description, got:\n%s", block)
	}
}

// ---------- Relative time tests ----------

func TestRelativeTime_Seconds(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-30 * time.Second)
	got := RelativeTime(now, past)
	if got != "30s ago" {
		t.Errorf("RelativeTime(30s) = %q, want %q", got, "30s ago")
	}
}

func TestRelativeTime_Minutes(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-5 * time.Minute)
	got := RelativeTime(now, past)
	if got != "5m ago" {
		t.Errorf("RelativeTime(5m) = %q, want %q", got, "5m ago")
	}
}

func TestRelativeTime_Hours(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-2 * time.Hour)
	got := RelativeTime(now, past)
	if got != "2h ago" {
		t.Errorf("RelativeTime(2h) = %q, want %q", got, "2h ago")
	}
}

func TestRelativeTime_Days(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-3 * 24 * time.Hour)
	got := RelativeTime(now, past)
	if got != "3d ago" {
		t.Errorf("RelativeTime(3d) = %q, want %q", got, "3d ago")
	}
}

// ---------- Passthrough rules tests ----------

func TestBuildRulesBlock_Empty(t *testing.T) {
	got := buildRulesBlock(nil)
	if got != "" {
		t.Errorf("expected empty string for nil rules, got %q", got)
	}
}

func TestBuildRulesBlock_WithRules(t *testing.T) {
	rules := []memory.Fact{
		{Content: "always forward code to coder", Agent: strPtr("coder")},
		{Content: "never interrupt research agent"},
	}
	got := buildRulesBlock(rules)
	if !strings.Contains(got, "Active passthrough rules:") {
		t.Errorf("missing header in rules block")
	}
	if !strings.Contains(got, "- coder: always forward code to coder") {
		t.Errorf("missing coder rule, got:\n%s", got)
	}
	if !strings.Contains(got, "- global: never interrupt research agent") {
		t.Errorf("missing global rule, got:\n%s", got)
	}
}

// ---------- Facts block tests ----------

func TestBuildFactsBlock_Empty(t *testing.T) {
	got := buildFactsBlock(nil)
	if got != "" {
		t.Errorf("expected empty string for nil facts, got %q", got)
	}
}

func TestBuildFactsBlock_WithFacts(t *testing.T) {
	facts := []memory.Fact{
		{Content: "User prefers dark mode", Source: strPtr("conversation"), Score: 0.95},
		{Content: "User works at Acme Corp", Score: 0.8},
	}
	got := buildFactsBlock(facts)
	if !strings.Contains(got, "Known facts:") {
		t.Errorf("missing header")
	}
	if !strings.Contains(got, "User prefers dark mode (source: conversation, relevance: 0.95)") {
		t.Errorf("missing fact 1, got:\n%s", got)
	}
	if !strings.Contains(got, "User works at Acme Corp (source: unknown, relevance: 0.80)") {
		t.Errorf("missing fact 2 with unknown source, got:\n%s", got)
	}
}

// ---------- Compaction block tests ----------

func TestBuildCompactionsBlock_Empty(t *testing.T) {
	got := buildCompactionsBlock(nil)
	if got != "" {
		t.Errorf("expected empty string for nil compactions, got %q", got)
	}
}

func TestBuildCompactionsBlock_WithEntries(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	comps := []memory.Compaction{
		{PeriodStart: t1, PeriodEnd: t2, Summary: "Discussed project architecture"},
	}
	got := buildCompactionsBlock(comps)
	if !strings.Contains(got, "Recent history:") {
		t.Errorf("missing header")
	}
	if !strings.Contains(got, "Discussed project architecture") {
		t.Errorf("missing summary, got:\n%s", got)
	}
	if !strings.Contains(got, "2025-01-01T10:00:00Z") {
		t.Errorf("missing period start, got:\n%s", got)
	}
}

// ---------- Full Build tests ----------

func TestBuild_ContainsCoreRole(t *testing.T) {
	cfg := defaultTestConfig()
	b := New(cfg)
	got := b.Build(nil, nil, nil, nil)
	if !strings.Contains(got, "a personal AI orchestrator") {
		t.Errorf("Build output missing core role")
	}
}

func TestBuild_ContainsCurrentTime(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	origNow := nowFunc
	nowFunc = func() time.Time { return fixed }
	defer func() { nowFunc = origNow }()

	cfg := defaultTestConfig()
	b := New(cfg)
	got := b.Build(nil, nil, nil, nil)
	if !strings.Contains(got, "2025-06-15T14:30:00Z") {
		t.Errorf("Build output missing current time, got:\n%s", got)
	}
}

func TestBuild_WithAgents(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	origNow := nowFunc
	nowFunc = func() time.Time { return fixed }
	defer func() { nowFunc = origNow }()

	cfg := defaultTestConfig()
	cfg.Agents.Default = "alice"
	b := New(cfg)

	lastInteraction := fixed.Add(-5 * time.Minute)
	agents := []memory.AgentState{
		{
			Agent:           "alice",
			Emoji:           strPtr("A"),
			Status:          strPtr("running"),
			Role:            strPtr("coding assistant"),
			Model:           strPtr("gpt-4o"),
			LastInteraction: timePtr(lastInteraction),
			Updated:         fixed.Add(-1 * time.Hour),
		},
	}

	got := b.Build(agents, nil, nil, nil)
	if !strings.Contains(got, "Active agents:") {
		t.Errorf("missing Active agents section")
	}
	if !strings.Contains(got, "A alice") {
		t.Errorf("missing agent emoji+name, got:\n%s", got)
	}
	if !strings.Contains(got, "coding assistant") {
		t.Errorf("missing agent role")
	}
	if !strings.Contains(got, "Default agent: alice") {
		t.Errorf("missing default agent")
	}
	if !strings.Contains(got, "5m ago") {
		t.Errorf("missing relative time for last msg")
	}
}

func TestBuild_EmptyInputs(t *testing.T) {
	cfg := defaultTestConfig()
	b := New(cfg)
	got := b.Build(nil, nil, nil, nil)

	// Should still have core role and persona
	if !strings.Contains(got, "a personal AI orchestrator") {
		t.Errorf("missing core role with empty inputs")
	}
	if !strings.Contains(got, "Your name is mux.") {
		t.Errorf("missing persona name with empty inputs")
	}
	// Should NOT have empty section headers
	if strings.Contains(got, "Known facts:") {
		t.Errorf("should not have facts header with no facts")
	}
	if strings.Contains(got, "Active passthrough rules:") {
		t.Errorf("should not have rules header with no rules")
	}
	if strings.Contains(got, "Recent history:") {
		t.Errorf("should not have compaction header with no compactions")
	}
}

func TestBuild_AllSections(t *testing.T) {
	fixed := time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC)
	origNow := nowFunc
	nowFunc = func() time.Time { return fixed }
	defer func() { nowFunc = origNow }()

	cfg := defaultTestConfig()
	cfg.Agents.Default = "bob"
	cfg.Persona.Bio = "I am your personal assistant."
	b := New(cfg)

	lastInteraction := fixed.Add(-2 * time.Hour)
	agents := []memory.AgentState{
		{
			Agent:           "bob",
			Emoji:           strPtr("B"),
			Status:          strPtr("running"),
			Role:            strPtr("researcher"),
			Model:           strPtr("gpt-4o"),
			LastInteraction: timePtr(lastInteraction),
			Updated:         fixed.Add(-30 * time.Minute),
		},
	}
	facts := []memory.Fact{
		{Content: "User likes tea", Source: strPtr("chat"), Score: 0.9},
	}
	compactions := []memory.Compaction{
		{
			PeriodStart: fixed.Add(-6 * time.Hour),
			PeriodEnd:   fixed.Add(-3 * time.Hour),
			Summary:     "Discussed deployment strategy",
		},
	}
	rules := []memory.Fact{
		{Content: "always add context to code requests", Agent: strPtr("bob")},
	}

	got := b.Build(agents, facts, compactions, rules)

	// All 6 sections should be present
	checks := []string{
		"a personal AI orchestrator",                // core role
		"Your communication style:",                         // persona
		"Active passthrough rules:",                         // rules
		"Current time:",                                     // roster
		"Known facts:",                                      // facts
		"Recent history:",                                   // compactions
		"I am your personal assistant.",                     // bio
		"B bob",                                             // agent in roster
		"User likes tea",                                    // fact content
		"Discussed deployment strategy",                     // compaction summary
		"bob: always add context to code requests",          // rule content
	}
	for _, check := range checks {
		if !strings.Contains(got, check) {
			t.Errorf("Build output missing %q", check)
		}
	}
}

// ---------- Presets tests ----------

func TestPresets_Exist(t *testing.T) {
	expected := []string{"professional", "casual", "assistant", "minimal", "creative"}
	for _, name := range expected {
		if _, ok := Presets[name]; !ok {
			t.Errorf("missing preset %q", name)
		}
	}
}

func TestPresets_Professional(t *testing.T) {
	p := Presets["professional"]
	if p.Formality != 0.8 {
		t.Errorf("professional formality = %v, want 0.8", p.Formality)
	}
	if p.Humor != 0.1 {
		t.Errorf("professional humor = %v, want 0.1", p.Humor)
	}
	if p.Sarcasm != 0.0 {
		t.Errorf("professional sarcasm = %v, want 0.0", p.Sarcasm)
	}
}

// ---------- Correction dictionary test ----------

func TestBuild_CorrectionDictionary(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Voice.CorrectionDict = map[string][]string{
		"kubernetes": {"k8s", "kube"},
	}
	b := New(cfg)
	got := b.Build(nil, nil, nil, nil)
	if !strings.Contains(got, "Correction dictionary:") {
		t.Errorf("missing correction dictionary, got:\n%s", got)
	}
	if !strings.Contains(got, "kubernetes") {
		t.Errorf("missing correction word, got:\n%s", got)
	}
}
