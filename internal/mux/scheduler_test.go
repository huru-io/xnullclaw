package mux

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// newTestScheduler builds a Scheduler wired to an in-memory store, test
// logger, default config, sync.Mutex, and injectable runTurn/deliver
// function fields. Returns the scheduler and the underlying store.
func newTestScheduler(t *testing.T) (*Scheduler, *memory.Store) {
	t.Helper()

	store := testStore(t)
	logger := testLogger(t)
	cfg := config.DefaultConfig()

	var chatID int64 = 42
	var mu sync.Mutex

	s := &Scheduler{
		store:  store,
		cfg:    cfg,
		logger: logger,
		chatID: &chatID,
		turnMu: &mu,
		runTurn: func(chatID int64, userText, stream string) turnResult {
			return turnResult{chatID: chatID, text: "ok"}
		},
		deliver: func(r turnResult) {},
		// Avoid first-tick heartbeat by pretending we just heartbeated.
		lastHeartbeat: time.Now(),
	}
	return s, store
}

// --- tick tests ---

func TestTick_DueTasksFired(t *testing.T) {
	s, store := newTestScheduler(t)

	// Insert two due tasks.
	past := time.Now().Add(-time.Minute)
	store.AddScheduledTask(memory.ScheduledTask{
		Description: "Task A",
		TriggerAt:   past,
	})
	store.AddScheduledTask(memory.ScheduledTask{
		Description: "Task B",
		TriggerAt:   past,
	})

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{chatID: chatID, text: "done"}
	}
	var delivered int
	s.deliver = func(r turnResult) { delivered++ }

	s.tick()

	if calls != 2 {
		t.Errorf("expected 2 runTurn calls, got %d", calls)
	}
	if delivered != 2 {
		t.Errorf("expected 2 deliver calls, got %d", delivered)
	}

	// Tasks should be marked fired.
	tasks, err := store.ListScheduledTasks("fired")
	if err != nil {
		t.Fatalf("ListScheduledTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 fired tasks, got %d", len(tasks))
	}
}

func TestTick_NoChatID_TasksDeferred(t *testing.T) {
	s, store := newTestScheduler(t)

	// Set chatID to 0.
	var zeroChatID int64
	s.chatID = &zeroChatID

	// Also set group ID to 0 (private mode, no chat yet).
	s.cfg.Telegram.GroupID = 0

	past := time.Now().Add(-time.Minute)
	store.AddScheduledTask(memory.ScheduledTask{
		Description: "Deferred task",
		TriggerAt:   past,
	})

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{}
	}

	s.tick()

	if calls != 0 {
		t.Errorf("expected 0 runTurn calls with zero chatID, got %d", calls)
	}

	// Task should remain pending.
	tasks, err := store.ListScheduledTasks("pending")
	if err != nil {
		t.Fatalf("ListScheduledTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 pending task, got %d", len(tasks))
	}
}

func TestTick_FiredTasks_ResetsNoops(t *testing.T) {
	s, store := newTestScheduler(t)

	s.consecutiveNoops = 5

	past := time.Now().Add(-time.Minute)
	store.AddScheduledTask(memory.ScheduledTask{
		Description: "Reset noops",
		TriggerAt:   past,
	})

	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		return turnResult{chatID: chatID, text: "done"}
	}
	s.deliver = func(r turnResult) {}

	s.tick()

	if s.consecutiveNoops != 0 {
		t.Errorf("consecutiveNoops should be reset to 0, got %d", s.consecutiveNoops)
	}
}

func TestTick_NoTasks_CallsMaybeHeartbeat(t *testing.T) {
	s, _ := newTestScheduler(t)

	// Configure heartbeat and set lastHeartbeat far in the past so it triggers.
	s.cfg.Scheduler.HeartbeatMinutes = 5
	s.lastHeartbeat = time.Time{} // zero value = long ago

	// Ensure no recent user messages so heartbeat path is exercised.
	// Store is empty, so LastUserMessageTime returns nil.

	var heartbeatCalled bool
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		// The heartbeat path calls runTurn with stream "scheduler".
		if stream == "scheduler" {
			heartbeatCalled = true
		}
		return turnResult{chatID: chatID, text: "HEARTBEAT_OK"}
	}
	s.deliver = func(r turnResult) {}

	// To ensure the heartbeat fires, we need signals from preCheck.
	// Add a drain message to create a signal.
	agent := "alice"
	s.store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: "[alice]: something happened",
		Agent:   &agent,
		Stream:  "drain",
	})

	s.tick()

	if !heartbeatCalled {
		t.Error("expected maybeHeartbeat to call runTurn with stream=scheduler")
	}
}

// --- maybeHeartbeat tests ---

func TestMaybeHeartbeat_Disabled(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.cfg.Scheduler.HeartbeatMinutes = 0

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{}
	}

	s.maybeHeartbeat(time.Now(), 42)

	if calls != 0 {
		t.Errorf("expected 0 runTurn calls when heartbeat disabled, got %d", calls)
	}
}

func TestMaybeHeartbeat_NoChatID(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.cfg.Scheduler.HeartbeatMinutes = 30

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{}
	}

	s.maybeHeartbeat(time.Now(), 0)

	if calls != 0 {
		t.Errorf("expected 0 runTurn calls when chatID=0, got %d", calls)
	}
}

func TestMaybeHeartbeat_WithinInterval(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.cfg.Scheduler.HeartbeatMinutes = 30
	s.lastHeartbeat = time.Now() // just heartbeated

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{}
	}

	s.maybeHeartbeat(time.Now(), 42)

	if calls != 0 {
		t.Errorf("expected 0 runTurn calls within heartbeat interval, got %d", calls)
	}
}

// --- preCheck tests ---

func TestPreCheck_DrainMessages(t *testing.T) {
	s, store := newTestScheduler(t)
	s.lastHeartbeat = time.Now().Add(-time.Hour)

	// Add drain messages from two agents.
	alice := "alice"
	bob := "bob"
	store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: "[alice]: msg 1",
		Agent:   &alice,
		Stream:  "drain",
	})
	store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: "[alice]: msg 2",
		Agent:   &alice,
		Stream:  "drain",
	})
	store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: "[bob]: msg 1",
		Agent:   &bob,
		Stream:  "drain",
	})

	signals := s.preCheck(time.Now())

	if len(signals) == 0 {
		t.Fatal("expected signals from drain messages, got none")
	}

	// Should have signals for both agents.
	foundAlice := false
	foundBob := false
	for _, sig := range signals {
		if containsStr(sig, "alice") {
			foundAlice = true
			if !containsStr(sig, "2 message(s)") {
				t.Errorf("expected 2 messages for alice, got: %s", sig)
			}
		}
		if containsStr(sig, "bob") {
			foundBob = true
			if !containsStr(sig, "1 message(s)") {
				t.Errorf("expected 1 message for bob, got: %s", sig)
			}
		}
	}
	if !foundAlice {
		t.Error("expected signal for alice")
	}
	if !foundBob {
		t.Error("expected signal for bob")
	}
}

func TestPreCheck_StaleAgents(t *testing.T) {
	s, store := newTestScheduler(t)
	s.lastHeartbeat = time.Now() // no drain messages expected

	// Create a running agent that hasn't interacted in 3 hours (exceeds staleAgentThreshold).
	status := "running"
	staleTime := time.Now().Add(-3 * time.Hour)
	store.UpsertAgentState(memory.AgentState{
		Agent:           "idle-agent",
		Status:          &status,
		LastInteraction: &staleTime,
	})

	signals := s.preCheck(time.Now())

	if len(signals) == 0 {
		t.Fatal("expected stale agent signal, got none")
	}

	found := false
	for _, sig := range signals {
		if containsStr(sig, "idle-agent") && containsStr(sig, "silent") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected stale agent signal for idle-agent, got: %v", signals)
	}
}

func TestPreCheck_NothingFlagged(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.lastHeartbeat = time.Now() // no drain messages since last heartbeat

	// No drain messages, no stale agents.
	signals := s.preCheck(time.Now())

	if len(signals) != 0 {
		t.Errorf("expected no signals, got %v", signals)
	}
}

// --- maybePrune tests ---

func TestMaybePrune_WithinFrequency(t *testing.T) {
	s, store := newTestScheduler(t)
	s.lastPrune = time.Now() // just pruned

	// Add an old fired task that WOULD be pruned if prune ran.
	past := time.Now().Add(-48 * time.Hour)
	store.AddScheduledTask(memory.ScheduledTask{
		Description: "Old task",
		TriggerAt:   past,
		Created:     past,
	})
	// Mark it fired so it is eligible for pruning.
	tasks, _ := store.ListScheduledTasks("pending")
	if len(tasks) > 0 {
		store.MarkTaskFired(tasks[0].ID)
	}

	s.maybePrune(time.Now())

	// Task should still be there because prune was skipped.
	all, _ := store.ListScheduledTasks("")
	if len(all) == 0 {
		t.Error("task should not have been pruned (within prune frequency)")
	}
}

func TestMaybePrune_PrunesOldTasks(t *testing.T) {
	s, store := newTestScheduler(t)
	s.lastPrune = time.Time{} // zero value = long ago, triggers prune

	// Add a task and cancel it. Cancelled tasks have fired_at=NULL, so
	// PruneOldScheduledTasks uses COALESCE(fired_at, created) = created.
	// Setting created to 48h ago exceeds schedulerPruneTTL (24h).
	past := time.Now().Add(-48 * time.Hour)
	id, err := store.AddScheduledTask(memory.ScheduledTask{
		Description: "Old cancelled task",
		TriggerAt:   past,
		Created:     past,
	})
	if err != nil {
		t.Fatalf("AddScheduledTask: %v", err)
	}
	if err := store.CancelScheduledTask(int(id)); err != nil {
		t.Fatalf("CancelScheduledTask: %v", err)
	}

	s.maybePrune(time.Now())

	// The old cancelled task should be pruned.
	tasks, _ := store.ListScheduledTasks("cancelled")
	if len(tasks) != 0 {
		t.Errorf("expected 0 cancelled tasks after prune, got %d", len(tasks))
	}
}

// --- safeTick tests ---

func TestSafeTick_RecoversPanic(t *testing.T) {
	s, _ := newTestScheduler(t)

	// Make runTurn panic — this will be called when tick fires a due task.
	// But instead, let's directly test safeTick by making the store nil
	// so DueScheduledTasks panics.
	s.store = nil

	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		s.safeTick()
	}()

	if didPanic {
		t.Fatal("safeTick should recover panics, but it propagated")
	}
}

// --- effectiveHeartbeatInterval tests ---

func TestEffectiveHeartbeatInterval_BelowThreshold(t *testing.T) {
	s := &Scheduler{consecutiveNoops: 0}
	got := s.effectiveHeartbeatInterval(30)
	want := 30 * time.Minute
	if got != want {
		t.Errorf("0 noops: got %v, want %v", got, want)
	}
}

func TestEffectiveHeartbeatInterval_AtThreshold(t *testing.T) {
	s := &Scheduler{consecutiveNoops: heartbeatNoopThreshold}
	got := s.effectiveHeartbeatInterval(30)
	want := 60 * time.Minute // 30m * 2^1
	if got != want {
		t.Errorf("at threshold: got %v, want %v", got, want)
	}
}

func TestEffectiveHeartbeatInterval_MaxBackoff(t *testing.T) {
	s := &Scheduler{consecutiveNoops: heartbeatNoopThreshold + heartbeatMaxBackoff + 10}
	got := s.effectiveHeartbeatInterval(30)
	want := 30 * time.Minute * time.Duration(1<<heartbeatMaxBackoff)
	if got != want {
		t.Errorf("max backoff: got %v, want %v", got, want)
	}
}

// --- IsHeartbeatOK tests (extending existing) ---

func TestIsHeartbeatOK_ExactMatch(t *testing.T) {
	if !IsHeartbeatOK("HEARTBEAT_OK") {
		t.Error("exact match should return true")
	}
}

func TestIsHeartbeatOK_WithSuffix(t *testing.T) {
	if IsHeartbeatOK("HEARTBEAT_OK but also this") {
		t.Error("response with suffix should return false")
	}
}

// --- formatScheduledTaskMessage tests ---

func TestFormatScheduledTaskMessage_Basic(t *testing.T) {
	task := memory.ScheduledTask{
		ID:          7,
		Description: "Check agent alice",
	}
	msg := formatScheduledTaskMessage(task)

	if !containsStr(msg, "[SCHEDULED TASK #7]") {
		t.Errorf("missing task ID in message: %s", msg)
	}
	if !containsStr(msg, "<task-description>Check agent alice</task-description>") {
		t.Errorf("missing description tag: %s", msg)
	}
	if containsStr(msg, "<task-context>") {
		t.Errorf("should not contain context tag when context is nil: %s", msg)
	}
}

func TestFormatScheduledTaskMessage_ContextContent(t *testing.T) {
	ctx := "agent=bob, priority=high"
	task := memory.ScheduledTask{
		ID:          3,
		Description: "Follow up",
		Context:     &ctx,
	}
	msg := formatScheduledTaskMessage(task)

	if !containsStr(msg, "<task-context>") {
		t.Errorf("missing task-context tag: %s", msg)
	}
	if !containsStr(msg, "agent=bob") {
		t.Errorf("missing context content: %s", msg)
	}
}

// --- formatHeartbeatMessage tests ---

func TestFormatHeartbeatMessage_ContainsSignals(t *testing.T) {
	signals := []string{
		"Agent \"alice\" sent 3 message(s) since last check",
		"Agent \"bob\" running but silent for 2h30m",
	}
	msg := formatHeartbeatMessage(signals)

	if !containsStr(msg, "[HEARTBEAT]") {
		t.Errorf("missing HEARTBEAT prefix: %s", msg)
	}
	if !containsStr(msg, "HEARTBEAT_OK") {
		t.Errorf("missing HEARTBEAT_OK instruction: %s", msg)
	}
	for _, sig := range signals {
		if !containsStr(msg, sig) {
			t.Errorf("missing signal %q in: %s", sig, msg)
		}
	}
	// Each signal should be on its own line with a bullet.
	if !containsStr(msg, "- Agent") {
		t.Errorf("signals should be bulleted: %s", msg)
	}
}

// --- muxTargetChatID tests ---

func TestMuxTargetChatID_AtomicRead(t *testing.T) {
	cfg := config.DefaultConfig()
	var chatID int64

	// Initially 0.
	got := muxTargetChatID(cfg, &chatID)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}

	// Set via atomic (as mux.go does).
	atomic.StoreInt64(&chatID, 999)
	got = muxTargetChatID(cfg, &chatID)
	if got != 999 {
		t.Errorf("expected 999, got %d", got)
	}
}

// --- lockedRunTurn tests ---

func TestLockedRunTurn_ReleasesLockOnSuccess(t *testing.T) {
	var mu sync.Mutex
	s := &Scheduler{
		turnMu: &mu,
		runTurn: func(chatID int64, userText, stream string) turnResult {
			return turnResult{chatID: chatID, text: "ok"}
		},
	}

	mu.Lock()
	result := s.lockedRunTurn(42, "test", "conversation")

	if result.text != "ok" {
		t.Errorf("expected text='ok', got %q", result.text)
	}

	// Lock should be released — TryLock should succeed.
	if !mu.TryLock() {
		t.Fatal("mutex should be unlocked after lockedRunTurn")
	}
	mu.Unlock()
}

// --- maxTasksPerTick limit test ---

func TestTick_MaxTasksPerTick(t *testing.T) {
	s, store := newTestScheduler(t)

	// Insert more tasks than maxTasksPerTick (which is 3).
	past := time.Now().Add(-time.Minute)
	for i := 0; i < 5; i++ {
		store.AddScheduledTask(memory.ScheduledTask{
			Description: "Bulk task",
			TriggerAt:   past,
		})
	}

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{chatID: chatID, text: "done"}
	}
	s.deliver = func(r turnResult) {}

	s.tick()

	if calls > maxTasksPerTick {
		t.Errorf("expected at most %d runTurn calls, got %d", maxTasksPerTick, calls)
	}
}

// --- heartbeat noop backoff test ---

func TestMaybeHeartbeat_NoopIncrementsCounter(t *testing.T) {
	s, store := newTestScheduler(t)
	s.cfg.Scheduler.HeartbeatMinutes = 5
	s.lastHeartbeat = time.Time{} // long ago
	s.consecutiveNoops = 0

	// Add a drain message so preCheck returns signals.
	agent := "alice"
	store.AddMessage(memory.Message{
		Role:    "assistant",
		Content: "[alice]: update",
		Agent:   &agent,
		Stream:  "drain",
	})

	// runTurn returns HEARTBEAT_OK.
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		return turnResult{chatID: chatID, heartbeatOK: true}
	}
	s.deliver = func(r turnResult) {}

	s.maybeHeartbeat(time.Now(), 42)

	if s.consecutiveNoops != 1 {
		t.Errorf("expected consecutiveNoops=1, got %d", s.consecutiveNoops)
	}
}

func TestMaybeHeartbeat_Tier1Clear_IncrementsNoops(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.cfg.Scheduler.HeartbeatMinutes = 5
	s.lastHeartbeat = time.Time{} // long ago
	s.consecutiveNoops = 0

	// No drain messages, no stale agents -> preCheck returns nil signals.
	// This should cause Tier 1 skip and increment noops.

	var calls int
	s.runTurn = func(chatID int64, userText, stream string) turnResult {
		calls++
		return turnResult{}
	}
	s.deliver = func(r turnResult) {}

	s.maybeHeartbeat(time.Now(), 42)

	if calls != 0 {
		t.Errorf("expected 0 runTurn calls (tier-1 clear), got %d", calls)
	}
	if s.consecutiveNoops != 1 {
		t.Errorf("expected consecutiveNoops=1 after tier-1 skip, got %d", s.consecutiveNoops)
	}
}
