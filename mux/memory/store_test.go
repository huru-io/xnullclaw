package memory

import (
	"testing"
	"time"
)

// helper — opens an in-memory Store for every test.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func strPtr(s string) *string { return &s }
func timePtr(t time.Time) *time.Time { return &t }

// ==================== Messages ====================

func TestAddAndRecentMessages(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		err := s.AddMessage(Message{
			Role:       "user",
			Content:    "msg " + string(rune('A'+i)),
			Stream:     "conversation",
			TokenCount: 10,
		})
		if err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	msgs, err := s.RecentMessages("conversation", 3)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Should be oldest-first among the 3 most recent: C, D, E
	if msgs[0].Content != "msg C" {
		t.Errorf("expected first message 'msg C', got %q", msgs[0].Content)
	}
	if msgs[2].Content != "msg E" {
		t.Errorf("expected last message 'msg E', got %q", msgs[2].Content)
	}
}

func TestMessageTokenCount(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddMessage(Message{Role: "user", Content: "a", Stream: "conversation", TokenCount: 15})
	_ = s.AddMessage(Message{Role: "assistant", Content: "b", Stream: "conversation", TokenCount: 25})
	_ = s.AddMessage(Message{Role: "user", Content: "c", Stream: "background", TokenCount: 100})

	total, err := s.MessageTokenCount("conversation")
	if err != nil {
		t.Fatalf("MessageTokenCount: %v", err)
	}
	if total != 40 {
		t.Errorf("expected 40 tokens, got %d", total)
	}

	bgTotal, err := s.MessageTokenCount("background")
	if err != nil {
		t.Fatalf("MessageTokenCount background: %v", err)
	}
	if bgTotal != 100 {
		t.Errorf("expected 100 tokens, got %d", bgTotal)
	}
}

func TestDeleteOldestMessages(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		_ = s.AddMessage(Message{
			Role:       "user",
			Content:    "msg " + string(rune('A'+i)),
			Stream:     "conversation",
			TokenCount: 10,
		})
	}

	deleted, err := s.DeleteOldestMessages("conversation", 2)
	if err != nil {
		t.Fatalf("DeleteOldestMessages: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted, got %d", len(deleted))
	}
	if deleted[0].Content != "msg A" {
		t.Errorf("expected first deleted 'msg A', got %q", deleted[0].Content)
	}
	if deleted[1].Content != "msg B" {
		t.Errorf("expected second deleted 'msg B', got %q", deleted[1].Content)
	}

	// Remaining should be C, D, E.
	remaining, err := s.RecentMessages("conversation", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(remaining) != 3 {
		t.Fatalf("expected 3 remaining, got %d", len(remaining))
	}
	if remaining[0].Content != "msg C" {
		t.Errorf("expected first remaining 'msg C', got %q", remaining[0].Content)
	}
}

func TestMessagesSince(t *testing.T) {
	s := newTestStore(t)

	past := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-30 * time.Minute)

	_ = s.AddMessage(Message{Role: "user", Content: "old", Stream: "conversation", TokenCount: 5, Timestamp: past})
	_ = s.AddMessage(Message{Role: "user", Content: "new", Stream: "conversation", TokenCount: 5, Timestamp: recent})

	cutoff := time.Now().Add(-1 * time.Hour)
	msgs, err := s.MessagesSince(cutoff, "conversation")
	if err != nil {
		t.Fatalf("MessagesSince: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message since cutoff, got %d", len(msgs))
	}
	if msgs[0].Content != "new" {
		t.Errorf("expected 'new', got %q", msgs[0].Content)
	}
}

func TestMessageStreamIsolation(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddMessage(Message{Role: "user", Content: "conv", Stream: "conversation", TokenCount: 10})
	_ = s.AddMessage(Message{Role: "user", Content: "bg", Stream: "background", TokenCount: 10})

	conv, _ := s.RecentMessages("conversation", 10)
	bg, _ := s.RecentMessages("background", 10)

	if len(conv) != 1 || conv[0].Content != "conv" {
		t.Errorf("conversation stream: expected [conv], got %v", conv)
	}
	if len(bg) != 1 || bg[0].Content != "bg" {
		t.Errorf("background stream: expected [bg], got %v", bg)
	}
}

func TestMessageDefaultStream(t *testing.T) {
	s := newTestStore(t)

	// Empty stream should default to "conversation".
	_ = s.AddMessage(Message{Role: "user", Content: "x", TokenCount: 1})
	msgs, _ := s.RecentMessages("conversation", 10)
	if len(msgs) != 1 {
		t.Errorf("expected message in conversation stream, got %d", len(msgs))
	}
}

// ==================== Facts ====================

func TestAddAndSearchFacts(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})
	_ = s.AddFact(Fact{Type: "knowledge", Content: "project uses Go 1.24", Agent: strPtr("alice"), Score: 0.8})
	_ = s.AddFact(Fact{Type: "rule", Content: "always respond in English", Score: 1.0})

	// Search all agents.
	results, err := s.SearchFacts("Go", "", 10)
	if err != nil {
		t.Fatalf("SearchFacts: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "project uses Go 1.24" {
		t.Errorf("unexpected content: %q", results[0].Content)
	}

	// Search by agent.
	results, err = s.SearchFacts("Go", "alice", 10)
	if err != nil {
		t.Fatalf("SearchFacts with agent: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result with agent filter, got %d", len(results))
	}

	// Search by agent — no match.
	results, _ = s.SearchFacts("Go", "bob", 10)
	if len(results) != 0 {
		t.Errorf("expected 0 results for bob, got %d", len(results))
	}
}

func TestFactDedup(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})
	// Exact duplicate — should be skipped.
	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})
	// Substring duplicate — new content is a substring of existing.
	_ = s.AddFact(Fact{Type: "preference", Content: "dark mode", Score: 1.0})

	facts, err := s.GetFactsByType("preference")
	if err != nil {
		t.Fatalf("GetFactsByType: %v", err)
	}
	if len(facts) != 1 {
		t.Errorf("expected 1 fact after dedup, got %d", len(facts))
	}
}

func TestFactDedupSuperset(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddFact(Fact{Type: "preference", Content: "dark mode", Score: 1.0})
	// New content contains existing content as substring — should be skipped.
	_ = s.AddFact(Fact{Type: "preference", Content: "user prefers dark mode", Score: 1.0})

	facts, _ := s.GetFactsByType("preference")
	if len(facts) != 1 {
		t.Errorf("expected 1 fact (superset dedup), got %d", len(facts))
	}
}

func TestGetFactsByType(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddFact(Fact{Type: "rule", Content: "always use English"})
	_ = s.AddFact(Fact{Type: "rule", Content: "no swearing"})
	_ = s.AddFact(Fact{Type: "preference", Content: "dark mode"})

	rules, err := s.GetFactsByType("rule")
	if err != nil {
		t.Fatalf("GetFactsByType: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

func TestUpdateFactAccess(t *testing.T) {
	s := newTestStore(t)

	_ = s.AddFact(Fact{Type: "knowledge", Content: "unique knowledge item", Score: 0.5})
	facts, _ := s.GetFactsByType("knowledge")
	if len(facts) == 0 {
		t.Fatal("expected at least 1 fact")
	}

	id := facts[0].ID
	if err := s.UpdateFactAccess(id); err != nil {
		t.Fatalf("UpdateFactAccess: %v", err)
	}

	// Re-read and check access count bumped.
	facts, _ = s.GetFactsByType("knowledge")
	if facts[0].AccessCount != 1 {
		t.Errorf("expected access_count=1, got %d", facts[0].AccessCount)
	}
}

func TestDecayFactScores(t *testing.T) {
	s := newTestStore(t)

	old := time.Now().Add(-60 * 24 * time.Hour) // 60 days ago
	_ = s.AddFact(Fact{Type: "knowledge", Content: "stale fact", Score: 1.0, Accessed: old})
	_ = s.AddFact(Fact{Type: "knowledge", Content: "fresh fact", Score: 1.0}) // accessed = now

	// Decay facts not accessed in last 30 days by 0.5.
	if err := s.DecayFactScores(30*24*time.Hour, 0.5); err != nil {
		t.Fatalf("DecayFactScores: %v", err)
	}

	facts, _ := s.SearchFacts("stale", "", 10)
	if len(facts) != 1 {
		t.Fatalf("expected 1 stale fact, got %d", len(facts))
	}
	if facts[0].Score > 0.6 {
		t.Errorf("expected decayed score <=0.5, got %f", facts[0].Score)
	}

	facts, _ = s.SearchFacts("fresh", "", 10)
	if len(facts) != 1 {
		t.Fatalf("expected 1 fresh fact, got %d", len(facts))
	}
	if facts[0].Score < 0.9 {
		t.Errorf("expected fresh score ~1.0, got %f", facts[0].Score)
	}
}

func TestPruneFacts(t *testing.T) {
	s := newTestStore(t)

	old := time.Now().Add(-60 * 24 * time.Hour)
	_ = s.AddFact(Fact{Type: "knowledge", Content: "low score fact", Score: 0.05, Accessed: old})
	_ = s.AddFact(Fact{Type: "knowledge", Content: "high score fact", Score: 0.9})

	pruned, err := s.PruneFacts(0.1)
	if err != nil {
		t.Fatalf("PruneFacts: %v", err)
	}
	if len(pruned) != 1 {
		t.Fatalf("expected 1 pruned, got %d", len(pruned))
	}
	if pruned[0].Content != "low score fact" {
		t.Errorf("expected 'low score fact', got %q", pruned[0].Content)
	}

	remaining, _ := s.GetFactsByType("knowledge")
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining, got %d", len(remaining))
	}
}

// ==================== Agent State ====================

func TestUpsertAgentState(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()

	// Insert.
	err := s.UpsertAgentState(AgentState{
		Agent:           "alice",
		Emoji:           strPtr("A"),
		Status:          strPtr("running"),
		Role:            strPtr("coder"),
		Model:           strPtr("gpt-4o"),
		CurrentTask:     strPtr("writing tests"),
		LastMessage:     strPtr("hey alice"),
		LastResponse:    strPtr("on it"),
		LastInteraction: timePtr(now),
	})
	if err != nil {
		t.Fatalf("UpsertAgentState insert: %v", err)
	}

	st, err := s.GetAgentState("alice")
	if err != nil {
		t.Fatalf("GetAgentState: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil agent state")
	}
	if *st.Status != "running" {
		t.Errorf("expected status 'running', got %q", *st.Status)
	}
	if *st.CurrentTask != "writing tests" {
		t.Errorf("expected task 'writing tests', got %q", *st.CurrentTask)
	}

	// Update.
	err = s.UpsertAgentState(AgentState{
		Agent:       "alice",
		Emoji:       strPtr("A"),
		Status:      strPtr("stopped"),
		Role:        strPtr("coder"),
		Model:       strPtr("gpt-4o"),
		CurrentTask: strPtr("idle"),
	})
	if err != nil {
		t.Fatalf("UpsertAgentState update: %v", err)
	}

	st, _ = s.GetAgentState("alice")
	if *st.Status != "stopped" {
		t.Errorf("expected updated status 'stopped', got %q", *st.Status)
	}
	if *st.CurrentTask != "idle" {
		t.Errorf("expected updated task 'idle', got %q", *st.CurrentTask)
	}
}

func TestGetAgentStateNotFound(t *testing.T) {
	s := newTestStore(t)
	st, err := s.GetAgentState("nonexistent")
	if err != nil {
		t.Fatalf("GetAgentState: %v", err)
	}
	if st != nil {
		t.Errorf("expected nil for non-existent agent, got %+v", st)
	}
}

func TestAllAgentStates(t *testing.T) {
	s := newTestStore(t)

	_ = s.UpsertAgentState(AgentState{Agent: "alice", Status: strPtr("running")})
	_ = s.UpsertAgentState(AgentState{Agent: "bob", Status: strPtr("stopped")})

	states, err := s.AllAgentStates()
	if err != nil {
		t.Fatalf("AllAgentStates: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
	// Ordered by agent name.
	if states[0].Agent != "alice" {
		t.Errorf("expected first agent 'alice', got %q", states[0].Agent)
	}
	if states[1].Agent != "bob" {
		t.Errorf("expected second agent 'bob', got %q", states[1].Agent)
	}
}

// ==================== Compactions ====================

func TestAddAndRecentCompactions(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	for i := 0; i < 5; i++ {
		_ = s.AddCompaction(Compaction{
			PeriodStart: now.Add(time.Duration(-10+i) * time.Hour),
			PeriodEnd:   now.Add(time.Duration(-9+i) * time.Hour),
			Summary:     "block " + string(rune('A'+i)),
			Agents:      "alice",
			TokenCount:  100,
		})
	}

	recent, err := s.RecentCompactions(2)
	if err != nil {
		t.Fatalf("RecentCompactions: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 compactions, got %d", len(recent))
	}
	// Newest first.
	if recent[0].Summary != "block E" {
		t.Errorf("expected newest 'block E', got %q", recent[0].Summary)
	}
	if recent[1].Summary != "block D" {
		t.Errorf("expected second 'block D', got %q", recent[1].Summary)
	}
}

func TestCompactionsByDateRange(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	_ = s.AddCompaction(Compaction{
		PeriodStart: now.Add(-48 * time.Hour),
		PeriodEnd:   now.Add(-47 * time.Hour),
		Summary:     "old",
		Agents:      "alice",
		TokenCount:  50,
	})
	_ = s.AddCompaction(Compaction{
		PeriodStart: now.Add(-2 * time.Hour),
		PeriodEnd:   now.Add(-1 * time.Hour),
		Summary:     "recent",
		Agents:      "bob",
		TokenCount:  50,
	})

	// Query last 24 hours.
	results, err := s.CompactionsByDateRange(now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatalf("CompactionsByDateRange: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 compaction in range, got %d", len(results))
	}
	if results[0].Summary != "recent" {
		t.Errorf("expected 'recent', got %q", results[0].Summary)
	}
}

// ==================== Costs ====================

func TestAddAndCostsByPeriod(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	_ = s.AddCost(Cost{
		Timestamp:    now.Add(-1 * time.Hour),
		Category:     "loop",
		Model:        strPtr("gpt-4o"),
		InputTokens:  1000,
		OutputTokens: 500,
		CostUSD:      0.05,
	})
	_ = s.AddCost(Cost{
		Timestamp:    now.Add(-30 * time.Minute),
		Category:     "agent",
		Model:        strPtr("gpt-4o-mini"),
		Agent:        strPtr("alice"),
		InputTokens:  2000,
		OutputTokens: 1000,
		CostUSD:      0.02,
	})

	costs, err := s.CostsByPeriod(now.Add(-2*time.Hour), now)
	if err != nil {
		t.Fatalf("CostsByPeriod: %v", err)
	}
	if len(costs) != 2 {
		t.Fatalf("expected 2 costs, got %d", len(costs))
	}
}

func TestCostSummary(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	_ = s.AddCost(Cost{Timestamp: now, Category: "loop", CostUSD: 0.10})
	_ = s.AddCost(Cost{Timestamp: now, Category: "loop", CostUSD: 0.20})
	_ = s.AddCost(Cost{Timestamp: now, Category: "agent", CostUSD: 0.05})
	_ = s.AddCost(Cost{Timestamp: now, Category: "compaction", CostUSD: 0.01})

	summary, err := s.CostSummary(now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("CostSummary: %v", err)
	}

	if v, ok := summary["loop"]; !ok || (v < 0.29 || v > 0.31) {
		t.Errorf("expected loop ~0.30, got %v", v)
	}
	if v, ok := summary["agent"]; !ok || (v < 0.04 || v > 0.06) {
		t.Errorf("expected agent ~0.05, got %v", v)
	}
	if v, ok := summary["compaction"]; !ok || (v < 0.009 || v > 0.011) {
		t.Errorf("expected compaction ~0.01, got %v", v)
	}
}

func TestAgentCostSummary(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	_ = s.AddCost(Cost{Timestamp: now, Category: "agent", Agent: strPtr("alice"), CostUSD: 0.10})
	_ = s.AddCost(Cost{Timestamp: now, Category: "agent", Agent: strPtr("alice"), CostUSD: 0.15})
	_ = s.AddCost(Cost{Timestamp: now, Category: "agent", Agent: strPtr("bob"), CostUSD: 0.50})

	total, err := s.AgentCostSummary("alice", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatalf("AgentCostSummary: %v", err)
	}
	if total < 0.24 || total > 0.26 {
		t.Errorf("expected ~0.25 for alice, got %f", total)
	}

	total, _ = s.AgentCostSummary("bob", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if total < 0.49 || total > 0.51 {
		t.Errorf("expected ~0.50 for bob, got %f", total)
	}
}

// ==================== Schema creation idempotency ====================

func TestNewIdempotent(t *testing.T) {
	// Opening the same in-memory DB twice shouldn't fail.
	s := newTestStore(t)
	_ = s.AddMessage(Message{Role: "user", Content: "hello", Stream: "conversation"})
	// Re-running schema creation on the same DB should be fine (IF NOT EXISTS).
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("re-running schema creation failed: %v", err)
	}
}
