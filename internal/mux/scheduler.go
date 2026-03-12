// Package mux — scheduler.go checks for due scheduled tasks and fires
// synthetic turns so the LLM can act on them (reminders, check-ins, etc.).
//
// Also implements a two-tier heartbeat (informed by OpenClaw patterns):
//   Tier 1: Rule-based pre-check (zero tokens) — skips LLM if nothing changed
//   Tier 2: LLM triage with compact prompt — only when Tier 1 flags activity
//
// The LLM can respond with HEARTBEAT_OK to signal "nothing to report",
// which is silently dropped (not forwarded to Telegram).
// Consecutive no-ops trigger exponential backoff to save tokens.
package mux

import (
	"fmt"
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

	// heartbeatMaxBackoff caps the backoff multiplier for consecutive no-ops.
	heartbeatMaxBackoff = 4
	// heartbeatNoopThreshold is the number of consecutive HEARTBEAT_OK
	// responses before the interval starts doubling.
	heartbeatNoopThreshold = 3
)

// HeartbeatOK is the sentinel value the LLM returns when nothing needs
// attention. Responses starting with this token are silently dropped.
const HeartbeatOK = "HEARTBEAT_OK"

// Scheduler periodically checks for due scheduled tasks and fires synthetic
// turns via the mux loop so the LLM can act on them. It also fires periodic
// heartbeat turns so the mux can autonomously check on agents and take
// proactive actions.
type Scheduler struct {
	store  *memory.Store
	cfg    *config.Config
	logger *logging.Logger

	// runTurn fires a synthetic turn. The scheduler passes a system-generated
	// message describing the task so the LLM can decide what to do.
	runTurn func(chatID int64, userText string)

	// chatID points to the mux's lastChatID (atomic).
	chatID *int64

	// turnMu prevents scheduler turns from interleaving with user/drain turns.
	turnMu *sync.Mutex

	lastPrune     time.Time
	lastHeartbeat time.Time

	// consecutiveNoops tracks how many heartbeats returned HEARTBEAT_OK
	// in a row, for exponential backoff.
	consecutiveNoops int
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
			s.logger.Error("scheduler: panic recovered", "panic", fmt.Sprint(r))
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
	chatID := s.targetChatID()

	// Fire due tasks.
	firedTasks := false
	if len(tasks) > 0 && chatID != 0 {
		if s.turnMu.TryLock() {
			for _, t := range tasks {
				msg := formatScheduledTaskMessage(t)
				s.logger.Info("scheduler: firing task", "id", t.ID, "description", t.Description)

				// Mark as fired BEFORE running the turn to prevent re-firing
				// if the turn takes longer than the scheduler interval.
				if err := s.store.MarkTaskFired(t.ID); err != nil {
					s.logger.Error("scheduler: mark task fired", "id", t.ID, "error", err)
					continue
				}
				s.runTurn(chatID, msg)
				firedTasks = true
			}
			s.turnMu.Unlock()
			// Real activity resets backoff.
			s.consecutiveNoops = 0
		}
		// If we couldn't get the lock, tasks stay pending for next tick.
	} else if len(tasks) > 0 && chatID == 0 {
		s.logger.Error("scheduler: no chat ID available, tasks deferred")
	}

	// Heartbeat — only if we didn't already fire tasks this tick (avoid
	// double wake-ups) and the mux has been idle long enough.
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
// signals that Tier 1 detected, not the full conversation history.
func (s *Scheduler) maybeHeartbeat(now time.Time, chatID int64) {
	hbMinutes := s.cfg.Scheduler.HeartbeatMinutes
	if hbMinutes <= 0 || chatID == 0 {
		return // heartbeat disabled or no chat available
	}
	hbInterval := s.effectiveHeartbeatInterval()

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
			"next_interval", s.effectiveHeartbeatInterval())
		return
	}

	// ── Tier 2: LLM triage with compact prompt ──
	if !s.turnMu.TryLock() {
		return
	}
	defer s.turnMu.Unlock()

	s.logger.Info("scheduler: heartbeat firing", "signals", len(signals))
	s.lastHeartbeat = now

	msg := formatHeartbeatMessage(signals)
	s.runTurn(chatID, msg)
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
				agents[*m.Agent]++
			}
		}
		for agent, count := range agents {
			signals = append(signals, fmt.Sprintf("Agent %q sent %d message(s) since last check", agent, count))
		}
	}

	// 2. Check for pending scheduled tasks (upcoming in next heartbeat interval).
	hbInterval := time.Duration(s.cfg.Scheduler.HeartbeatMinutes) * time.Minute
	upcoming, err := s.store.DueScheduledTasks(now.Add(hbInterval))
	if err == nil && len(upcoming) > 0 {
		signals = append(signals, fmt.Sprintf("%d scheduled task(s) due soon", len(upcoming)))
	}

	// 3. Check if any agent hasn't been heard from in a long time (stale agents).
	agents, err := s.store.AllAgentStates()
	if err == nil {
		for _, a := range agents {
			if a.Status != nil && *a.Status == "running" && a.LastInteraction != nil {
				silence := now.Sub(*a.LastInteraction)
				if silence > 2*time.Hour {
					name := a.Agent
					signals = append(signals, fmt.Sprintf("Agent %q running but silent for %s", name, silence.Truncate(time.Minute)))
				}
			}
		}
	}

	return signals
}

// effectiveHeartbeatInterval returns the heartbeat interval adjusted for
// exponential backoff based on consecutive no-op heartbeats.
func (s *Scheduler) effectiveHeartbeatInterval() time.Duration {
	base := time.Duration(s.cfg.Scheduler.HeartbeatMinutes) * time.Minute
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
// to report" token. Callers should suppress forwarding such responses to
// Telegram. The check is case-insensitive and trims whitespace.
func IsHeartbeatOK(response string) bool {
	trimmed := strings.TrimSpace(response)
	return strings.HasPrefix(strings.ToUpper(trimmed), HeartbeatOK)
}

// RecordHeartbeatResult should be called by the mux after a heartbeat turn
// completes. It updates the backoff counter based on the LLM's response.
func (s *Scheduler) RecordHeartbeatResult(wasNoop bool) {
	if wasNoop {
		s.consecutiveNoops++
		s.logger.Info("scheduler: heartbeat noop",
			"consecutive", s.consecutiveNoops,
			"next_interval", s.effectiveHeartbeatInterval())
	} else {
		s.consecutiveNoops = 0
	}
}

// targetChatID returns the chat to send scheduler messages to.
func (s *Scheduler) targetChatID() int64 {
	if s.cfg.Telegram.GroupID != 0 {
		return s.cfg.Telegram.GroupID
	}
	return atomic.LoadInt64(s.chatID)
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
func formatScheduledTaskMessage(t memory.ScheduledTask) string {
	msg := fmt.Sprintf("[SCHEDULED TASK #%d] %s", t.ID, t.Description)
	if t.Context != nil && *t.Context != "" {
		msg += fmt.Sprintf("\nContext: %s", *t.Context)
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
