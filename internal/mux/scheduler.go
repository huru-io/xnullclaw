// Package mux — scheduler.go checks for due scheduled tasks and fires
// synthetic turns so the LLM can act on them (reminders, check-ins, etc.).
//
// Also implements a two-tier heartbeat (informed by OpenClaw patterns):
//
//	Tier 1: Rule-based pre-check (zero tokens) — skips LLM if nothing changed
//	Tier 2: LLM triage with compact prompt — only when Tier 1 flags activity
//
// The LLM can respond with HEARTBEAT_OK to signal "nothing to report",
// which is silently dropped (not forwarded to Telegram or stored).
// Consecutive no-ops trigger exponential backoff to save tokens.
package mux

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// Scheduler timing constants.
const (
	schedulerInterval  = 30 * time.Second
	schedulerPruneTTL  = 24 * time.Hour
	schedulerPruneFreq = 10 * time.Minute

	// staleAgentThreshold is how long a running agent can be silent before
	// the heartbeat flags it for the LLM to investigate.
	staleAgentThreshold = 2 * time.Hour

	// heartbeatMaxBackoff caps the backoff multiplier for consecutive no-ops.
	// With maxBackoff=4 and base=30m, max effective interval = 30m * 2^4 = 480m.
	heartbeatMaxBackoff = 4
	// heartbeatNoopThreshold is the number of consecutive no-op results
	// before the interval starts doubling.
	heartbeatNoopThreshold = 3

	// maxPendingTasks prevents unbounded task accumulation (DoS guard).
	maxPendingTasks = 50
	// maxTasksPerTick prevents a single tick from holding the turn lock too long.
	maxTasksPerTick = 3
	// maxTriggerHorizon is the furthest in the future a task can be scheduled.
	maxTriggerHorizon = 30 * 24 * time.Hour // 30 days
	// minHeartbeatMinutes prevents misconfiguration from burning tokens.
	minHeartbeatMinutes = 5
)

// HeartbeatOK is the sentinel value the LLM returns when nothing needs
// attention. Responses matching exactly are silently dropped.
const HeartbeatOK = "HEARTBEAT_OK"

// Scheduler periodically checks for due scheduled tasks and fires synthetic
// turns via the mux loop. It also fires periodic heartbeat turns so the mux
// can autonomously check on agents and take proactive actions.
type Scheduler struct {
	store  *memory.Store
	cfg    *config.Config
	logger *logging.Logger

	// runTurn fires a synthetic turn. Returns a turnResult for deferred
	// delivery outside the turn lock (M20).
	runTurn func(chatID int64, userText, stream string) turnResult

	// deliver sends a turnResult to Telegram, called after releasing turnMu.
	deliver func(r turnResult)

	// chatID points to the mux's lastChatID (atomic).
	chatID *int64

	// turnMu prevents scheduler turns from interleaving with user/drain turns.
	turnMu *sync.Mutex

	lastPrune     time.Time
	lastHeartbeat time.Time

	// consecutiveNoops tracks how many heartbeats found nothing to do
	// (either Tier 1 clear or Tier 2 HEARTBEAT_OK), for exponential backoff.
	// Only accessed from the scheduler goroutine — no synchronization needed.
	consecutiveNoops int
}

// lockedRunTurn runs a turn under the already-held turnMu and guarantees
// the lock is released even if runTurn panics (#1 panic safety).
// Caller must hold turnMu before calling.
func (s *Scheduler) lockedRunTurn(chatID int64, msg, stream string) turnResult {
	defer s.turnMu.Unlock()
	return s.runTurn(chatID, msg, stream)
}

// Run polls for due tasks on the given interval until done is closed.
func (s *Scheduler) Run(interval time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.safeTick()
		}
	}
}

// safeTick wraps tick with panic recovery.
func (s *Scheduler) safeTick() {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler: panic recovered", "panic", fmt.Sprint(r),
				"stack", string(debug.Stack()))
		}
	}()
	s.tick()
}

// tick checks for due tasks, fires them, and checks if a heartbeat is needed.
func (s *Scheduler) tick() {
	now := time.Now()

	// Check for due tasks.
	tasks, err := s.store.DueScheduledTasks(now)
	if err != nil {
		s.logger.Error("scheduler: query due tasks", "error", err)
		return
	}

	// Determine target chat (needed for both tasks and heartbeat).
	chatID := muxTargetChatID(s.cfg, s.chatID)

	// Fire due tasks (up to maxTasksPerTick to avoid holding lock too long).
	firedTasks := false
	if len(tasks) > 0 && chatID != 0 {
		limit := maxTasksPerTick
		if len(tasks) < limit {
			limit = len(tasks)
		}
		for i := 0; i < limit; i++ {
			t := tasks[i]
			// Acquire lock per task so user messages aren't blocked for the full batch.
			if !s.turnMu.TryLock() {
				break // try remaining tasks next tick
			}

			s.logger.Info("scheduler: firing task", "id", t.ID, "description", t.Description)

			// Mark as fired BEFORE running the turn to prevent re-firing.
			// NOTE: at-most-once delivery — if the process crashes after
			// marking but before delivery, the task is lost. This trade-off
			// prevents double-firing on restart (#12).
			if err := s.store.MarkTaskFired(t.ID); err != nil {
				s.logger.Error("scheduler: mark task fired", "id", t.ID, "error", err)
				s.turnMu.Unlock()
				continue
			}
			msg := formatScheduledTaskMessage(t)
			result := s.lockedRunTurn(chatID, msg, "conversation") // unlocks turnMu (panic-safe)
			s.deliver(result)
			firedTasks = true
		}
		// Real activity resets backoff.
		if firedTasks {
			s.consecutiveNoops = 0
		}
	} else if len(tasks) > 0 && chatID == 0 {
		s.logger.Error("scheduler: no chat ID available, tasks deferred")
	}

	// Heartbeat — only if we didn't already fire tasks this tick.
	if !firedTasks {
		s.maybeHeartbeat(now, chatID)
	}

	s.maybePrune(now)
}

// maybeHeartbeat implements the two-tier heartbeat pattern.
//
// Tier 1 (zero tokens): checks idle time, recent drain activity, and pending
// tasks via direct DB queries. If nothing changed, skips the LLM entirely.
//
// Tier 2 (LLM triage): injects a compact heartbeat prompt with only the
// signals that Tier 1 detected. Response stored in "scheduler" stream.
func (s *Scheduler) maybeHeartbeat(now time.Time, chatID int64) {
	hbMinutes := s.cfg.Scheduler.HeartbeatMinutes
	if hbMinutes <= 0 || chatID == 0 {
		return // heartbeat disabled or no chat available
	}
	// Enforce minimum to prevent misconfiguration.
	if hbMinutes < minHeartbeatMinutes {
		hbMinutes = minHeartbeatMinutes
	}
	hbInterval := s.effectiveHeartbeatInterval(hbMinutes)

	// Don't fire heartbeat more frequently than the (possibly backed-off) interval.
	if now.Sub(s.lastHeartbeat) < hbInterval {
		return
	}

	// Only fire if the mux has been idle (no user messages) for at least
	// the base interval — avoids wasting tokens when already active.
	baseInterval := time.Duration(hbMinutes) * time.Minute
	lastMsg, err := s.store.LastUserMessageTime()
	if err != nil {
		s.logger.Error("scheduler: check last message time", "error", err)
		return
	}
	if lastMsg != nil && now.Sub(*lastMsg) < baseInterval {
		// Mux was active recently — reset timer and backoff.
		s.lastHeartbeat = now
		s.consecutiveNoops = 0
		return
	}

	// ── Tier 1: Rule-based pre-check (zero tokens) ──
	signals := s.preCheck(now)
	if len(signals) == 0 {
		// Nothing flagged — count as noop and skip the LLM.
		s.lastHeartbeat = now
		s.consecutiveNoops++
		s.logger.Info("scheduler: heartbeat skipped (tier-1 clear)",
			"consecutive_noops", s.consecutiveNoops,
			"next_interval", s.effectiveHeartbeatInterval(hbMinutes))
		return
	}

	// ── Tier 2: LLM triage with compact prompt ──
	if !s.turnMu.TryLock() {
		return // signals will be re-detected next tick
	}

	s.logger.Info("scheduler: heartbeat firing", "signals", len(signals))
	s.lastHeartbeat = now

	msg := formatHeartbeatMessage(signals)
	result := s.lockedRunTurn(chatID, msg, "scheduler") // unlocks turnMu (panic-safe)

	// Track noop for backoff: HEARTBEAT_OK means the LLM found nothing to do.
	if result.heartbeatOK {
		s.consecutiveNoops++
		s.logger.Info("scheduler: heartbeat noop (tier-2 ack)",
			"consecutive_noops", s.consecutiveNoops,
			"next_interval", s.effectiveHeartbeatInterval(hbMinutes))
	} else {
		s.consecutiveNoops = 0
	}

	s.deliver(result)
}

// preCheck performs lightweight rule-based checks (Tier 1) and returns a
// list of signals that warrant LLM attention. Returns nil if all clear.
func (s *Scheduler) preCheck(now time.Time) []string {
	var signals []string

	// 1. Check for recent drain messages (agent output since last heartbeat).
	drainMsgs, err := s.store.MessagesSince(s.lastHeartbeat, "drain")
	if err == nil && len(drainMsgs) > 0 {
		agents := make(map[string]int)
		for _, m := range drainMsgs {
			if m.Agent != nil {
				agents[config.SanitizeName(*m.Agent, 30)]++
			}
		}
		for agent, count := range agents {
			signals = append(signals, fmt.Sprintf("Agent %q sent %d message(s) since last check", agent, count))
		}
	}

	// 2. Check if any agent hasn't been heard from in a long time (stale agents).
	agentStates, err := s.store.AllAgentStates()
	if err == nil {
		for _, a := range agentStates {
			if a.Status != nil && *a.Status == "running" && a.LastInteraction != nil {
				silence := now.Sub(*a.LastInteraction)
				if silence > staleAgentThreshold {
					name := config.SanitizeName(a.Agent, 30)
					signals = append(signals, fmt.Sprintf("Agent %q running but silent for %s", name, silence.Truncate(time.Minute)))
				}
			}
		}
	}

	return signals
}

// effectiveHeartbeatInterval returns the heartbeat interval adjusted for
// exponential backoff based on consecutive no-op results.
func (s *Scheduler) effectiveHeartbeatInterval(hbMinutes int) time.Duration {
	base := time.Duration(hbMinutes) * time.Minute
	if s.consecutiveNoops < heartbeatNoopThreshold {
		return base
	}
	// Exponential backoff: double for each noop past the threshold, up to max.
	doublings := s.consecutiveNoops - heartbeatNoopThreshold + 1
	if doublings > heartbeatMaxBackoff {
		doublings = heartbeatMaxBackoff
	}
	return base * time.Duration(1<<doublings)
}

// IsHeartbeatOK checks if a response from the LLM is the sentinel "nothing
// to report" token. Uses exact match (after trimming) so that responses
// like "HEARTBEAT_OK but I noticed..." are NOT suppressed.
func IsHeartbeatOK(response string) bool {
	return strings.EqualFold(strings.TrimSpace(response), HeartbeatOK)
}

// muxTargetChatID returns the chat to send scheduler/drain messages to.
// Shared by Scheduler and Drainer.
func muxTargetChatID(cfg *config.Config, lastChatID *int64) int64 {
	if cfg.Telegram.GroupID != 0 {
		return cfg.Telegram.GroupID
	}
	return atomic.LoadInt64(lastChatID)
}

// maybePrune periodically cleans up old fired/cancelled tasks.
func (s *Scheduler) maybePrune(now time.Time) {
	if now.Sub(s.lastPrune) < schedulerPruneFreq {
		return
	}
	if n, err := s.store.PruneOldScheduledTasks(schedulerPruneTTL); err != nil {
		s.logger.Error("scheduler: prune failed", "error", err)
	} else if n > 0 {
		s.logger.Info("scheduler: pruned old tasks", "count", n)
	}
	s.lastPrune = now
}

// formatScheduledTaskMessage builds the synthetic user message for a fired task.
// Description and context are sanitized to prevent prompt injection.
func formatScheduledTaskMessage(t memory.ScheduledTask) string {
	desc := config.SanitizeText(t.Description, 500)
	msg := fmt.Sprintf("[SCHEDULED TASK #%d]\n<task-description>%s</task-description>", t.ID, desc)
	if t.Context != nil && *t.Context != "" {
		ctx := config.SanitizeText(*t.Context, 500)
		msg += fmt.Sprintf("\n<task-context>%s</task-context>", ctx)
	}
	return msg
}

// formatHeartbeatMessage builds a compact synthetic message for the LLM
// containing only the signals detected by the Tier 1 pre-check.
func formatHeartbeatMessage(signals []string) string {
	var sb strings.Builder
	sb.WriteString("[HEARTBEAT] Autonomous check-in. Signals detected:\n")
	for _, sig := range signals {
		sb.WriteString("- ")
		sb.WriteString(sig)
		sb.WriteString("\n")
	}
	sb.WriteString("\nReview the above signals and take action if needed. ")
	sb.WriteString("Use your tools to check agent status, send messages, or schedule follow-ups. ")
	sb.WriteString("If nothing requires action, respond with exactly: HEARTBEAT_OK")
	return sb.String()
}
