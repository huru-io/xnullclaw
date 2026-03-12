package memory

import (
	"testing"
	"time"
)

// ==================== ExtractKeywords ====================

func TestExtractKeywordsNormal(t *testing.T) {
	keywords := ExtractKeywords("What is the status of alice and the deployment?")
	// "what", "the", "is", "of", "and" are all stop words.
	// "the" appears twice — stop word.
	// Remaining: "status", "alice", "deployment"
	expected := map[string]bool{"status": true, "alice": true, "deployment": true}
	if len(keywords) != len(expected) {
		t.Fatalf("expected %d keywords, got %d: %v", len(expected), len(keywords), keywords)
	}
	for _, kw := range keywords {
		if !expected[kw] {
			t.Errorf("unexpected keyword: %q", kw)
		}
	}
}

func TestExtractKeywordsStopWordsOnly(t *testing.T) {
	keywords := ExtractKeywords("the a an is are was were be")
	if len(keywords) != 0 {
		t.Errorf("expected 0 keywords from stop words, got %d: %v", len(keywords), keywords)
	}
}

func TestExtractKeywordsEmpty(t *testing.T) {
	keywords := ExtractKeywords("")
	if len(keywords) != 0 {
		t.Errorf("expected 0 keywords from empty string, got %d: %v", len(keywords), keywords)
	}
}

func TestExtractKeywordsDuplicates(t *testing.T) {
	keywords := ExtractKeywords("deploy deploy deploy server server")
	expected := map[string]bool{"deploy": true, "server": true}
	if len(keywords) != len(expected) {
		t.Fatalf("expected %d keywords, got %d: %v", len(expected), len(keywords), keywords)
	}
	for _, kw := range keywords {
		if !expected[kw] {
			t.Errorf("unexpected keyword: %q", kw)
		}
	}
}

func TestExtractKeywordsShortWords(t *testing.T) {
	keywords := ExtractKeywords("go is ok no he we")
	// All words are < 3 chars or stop words.
	if len(keywords) != 0 {
		t.Errorf("expected 0 keywords from short words, got %d: %v", len(keywords), keywords)
	}
}

func TestExtractKeywordsPunctuation(t *testing.T) {
	keywords := ExtractKeywords("Hello, world! How's the \"server\" doing?")
	// "hello", "world", "server", "how's", "doing" — "how's" has 5 chars, not a stop word
	found := make(map[string]bool)
	for _, kw := range keywords {
		found[kw] = true
	}
	if !found["hello"] {
		t.Error("expected 'hello' in keywords")
	}
	if !found["world"] {
		t.Error("expected 'world' in keywords")
	}
	if !found["server"] {
		t.Error("expected 'server' in keywords")
	}
	if !found["doing"] {
		t.Error("expected 'doing' in keywords")
	}
}

// ==================== Assemble ====================

func TestAssembleBasic(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()

	// Add some user messages.
	_ = s.AddMessage(Message{
		Role: "user", Content: "hello there", Stream: "conversation",
		TokenCount: 5, Timestamp: now.Add(-10 * time.Minute),
	})
	_ = s.AddMessage(Message{
		Role: "assistant", Content: "hey!", Stream: "conversation",
		TokenCount: 3, Timestamp: now.Add(-9 * time.Minute),
	})
	_ = s.AddMessage(Message{
		Role: "user", Content: "check the server status", Stream: "conversation",
		TokenCount: 8, Timestamp: now.Add(-1 * time.Minute),
	})

	// Add facts.
	_ = s.AddFact(Fact{Type: "knowledge", Content: "server runs on port 8080", Score: 1.0})
	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})
	_ = s.AddFact(Fact{Type: "rule", Content: "always respond in English", Score: 1.0})

	// Add agent states.
	_ = s.UpsertAgentState(AgentState{
		Agent:           "alice",
		Status:          strPtr("running"),
		LastInteraction: timePtr(now.Add(-5 * time.Minute)),
	})

	// Add a compaction.
	_ = s.AddCompaction(Compaction{
		PeriodStart: now.Add(-2 * time.Hour),
		PeriodEnd:   now.Add(-1 * time.Hour),
		Summary:     "discussed project setup",
		Agents:      "alice",
		TokenCount:  200,
	})

	asm := NewAssembler(s)
	cd, err := asm.Assemble("check the server status")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// CurrentTime should be recent.
	if time.Since(cd.CurrentTime) > 5*time.Second {
		t.Errorf("CurrentTime too old: %v", cd.CurrentTime)
	}

	// Should have agents.
	if len(cd.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(cd.Agents))
	}
	if cd.Agents[0].Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cd.Agents[0].Agent)
	}

	// Should have facts — at minimum the "server" keyword match + rule + preference.
	if len(cd.Facts) < 2 {
		t.Errorf("expected at least 2 facts, got %d", len(cd.Facts))
	}

	// Should have rules.
	if len(cd.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cd.Rules))
	}
	if cd.Rules[0].Content != "always respond in English" {
		t.Errorf("unexpected rule content: %q", cd.Rules[0].Content)
	}

	// Should have compactions.
	if len(cd.Compactions) != 1 {
		t.Fatalf("expected 1 compaction, got %d", len(cd.Compactions))
	}
	if cd.Compactions[0].Summary != "discussed project setup" {
		t.Errorf("unexpected compaction summary: %q", cd.Compactions[0].Summary)
	}

	// Should have last user message time.
	if cd.LastUserMessage == nil {
		t.Fatal("expected non-nil LastUserMessage")
	}
	if cd.TimeSinceUser < 30*time.Second {
		t.Errorf("TimeSinceUser seems too small: %v", cd.TimeSinceUser)
	}

	// AgentLastSeen should have alice.
	if _, ok := cd.AgentLastSeen["alice"]; !ok {
		t.Error("expected alice in AgentLastSeen")
	}
}

func TestAssembleRulesAndPrefsAlwaysIncluded(t *testing.T) {
	s := newTestStore(t)

	// Add facts of various types.
	_ = s.AddFact(Fact{Type: "rule", Content: "always respond in English", Score: 1.0})
	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})
	_ = s.AddFact(Fact{Type: "knowledge", Content: "project uses Go 1.24", Score: 1.0})

	asm := NewAssembler(s)

	// Query that matches nothing relevant.
	cd, err := asm.Assemble("completely unrelated xyz query")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Rules and preferences must still be included.
	hasRule := false
	hasPref := false
	for _, f := range cd.Facts {
		if f.Type == "rule" && f.Content == "always respond in English" {
			hasRule = true
		}
		if f.Type == "preference" && f.Content == "user prefers dark mode" {
			hasPref = true
		}
	}
	if !hasRule {
		t.Error("expected rule to be included regardless of query")
	}
	if !hasPref {
		t.Error("expected preference to be included regardless of query")
	}

	// Rules field should be populated.
	if len(cd.Rules) != 1 {
		t.Errorf("expected 1 rule in Rules, got %d", len(cd.Rules))
	}
}

func TestAssembleNoUserMessages(t *testing.T) {
	s := newTestStore(t)

	// Only assistant messages, no user messages.
	_ = s.AddMessage(Message{
		Role: "assistant", Content: "hello", Stream: "conversation", TokenCount: 3,
	})

	asm := NewAssembler(s)
	cd, err := asm.Assemble("test")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if cd.LastUserMessage != nil {
		t.Errorf("expected nil LastUserMessage, got %v", cd.LastUserMessage)
	}
	if cd.TimeSinceUser != 0 {
		t.Errorf("expected zero TimeSinceUser, got %v", cd.TimeSinceUser)
	}
}

func TestAssembleAgentLastSeen(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()

	_ = s.UpsertAgentState(AgentState{
		Agent:           "alice",
		Status:          strPtr("running"),
		LastInteraction: timePtr(now.Add(-2 * time.Hour)),
	})
	_ = s.UpsertAgentState(AgentState{
		Agent:           "bob",
		Status:          strPtr("stopped"),
		LastInteraction: timePtr(now.Add(-30 * time.Minute)),
	})
	_ = s.UpsertAgentState(AgentState{
		Agent:  "carol",
		Status: strPtr("idle"),
		// No LastInteraction set.
	})

	asm := NewAssembler(s)
	cd, err := asm.Assemble("test query")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// Alice: ~2h ago.
	if d, ok := cd.AgentLastSeen["alice"]; !ok {
		t.Error("expected alice in AgentLastSeen")
	} else if d < 1*time.Hour || d > 3*time.Hour {
		t.Errorf("alice last seen %v, expected ~2h", d)
	}

	// Bob: ~30m ago.
	if d, ok := cd.AgentLastSeen["bob"]; !ok {
		t.Error("expected bob in AgentLastSeen")
	} else if d < 20*time.Minute || d > 40*time.Minute {
		t.Errorf("bob last seen %v, expected ~30m", d)
	}

	// Carol: no last interaction, should not appear.
	if _, ok := cd.AgentLastSeen["carol"]; ok {
		t.Error("carol should not be in AgentLastSeen (no LastInteraction)")
	}
}

func TestAssembleEmptyStore(t *testing.T) {
	s := newTestStore(t)

	asm := NewAssembler(s)
	cd, err := asm.Assemble("hello world")
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if cd.CurrentTime.IsZero() {
		t.Error("expected non-zero CurrentTime")
	}
	if len(cd.Agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(cd.Agents))
	}
	if len(cd.Facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(cd.Facts))
	}
	if len(cd.Compactions) != 0 {
		t.Errorf("expected 0 compactions, got %d", len(cd.Compactions))
	}
	if cd.LastUserMessage != nil {
		t.Errorf("expected nil LastUserMessage, got %v", cd.LastUserMessage)
	}
	if len(cd.AgentLastSeen) != 0 {
		t.Errorf("expected empty AgentLastSeen, got %v", cd.AgentLastSeen)
	}
}
