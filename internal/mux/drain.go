// Package mux — drain.go periodically collects agent output from .outbox/
// directories and sends it directly to Telegram, bypassing the LLM.
package mux

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
	"github.com/jotavich/xnullclaw/internal/telegram"
)

// Drain timing constants — co-located for easy tuning.
const (
	drainInterval      = 5 * time.Second
	drainPruneTTL      = time.Hour
	drainPruneInterval = time.Minute
	stalePendingCutoff = 10 * time.Minute
	maxOutboxFileSize  = 1 << 20 // 1 MB
)

// Drainer periodically reads agent .outbox/ directories and sends
// completed responses directly to Telegram with identity headers.
type Drainer struct {
	home   string
	store  *memory.Store
	bot    *telegram.Bot
	cfg    *config.Config
	logger *logging.Logger

	// chatID is the Telegram chat to send to. In group mode we use
	// cfg.Telegram.GroupID; in private mode we use the last known chatID.
	chatID *int64 // pointer to mux's lastChatID (atomic)

	// turnMu prevents drain sends from interleaving with turn sends.
	turnMu *sync.Mutex

	// lastPrune tracks when we last pruned old drain messages.
	lastPrune time.Time
}

// Run polls agent outboxes on the given interval until done is closed.
// Recovers from panics to prevent a single bad file from killing the drain pipeline.
func (d *Drainer) Run(interval time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			d.safeDrainAll()
		}
	}
}

// safeDrainAll wraps drainAll with panic recovery so a single bad file
// cannot kill the drain goroutine permanently.
func (d *Drainer) safeDrainAll() {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("drain: panic recovered", "panic", fmt.Sprint(r))
		}
	}()
	d.drainAll()
}

// drainAll iterates all known agents and drains their outboxes.
func (d *Drainer) drainAll() {
	agents, err := agent.ListAll(d.home)
	if err != nil {
		d.logger.Error("drain: list agents failed", "error", err)
		return
	}
	for _, a := range agents {
		d.drainAgent(a.Name)
	}
	// Prune drain messages periodically (not every tick — once per minute is enough).
	if time.Since(d.lastPrune) > drainPruneInterval {
		if n, err := d.store.PruneOldMessages("drain", drainPruneTTL); err != nil {
			d.logger.Error("drain: prune failed", "error", err)
		} else if n > 0 {
			d.logger.Info("drain: pruned old messages", "count", n)
		}
		d.lastPrune = time.Now()
	}
}

// drainAgent reads and sends all completed messages from a single agent's outbox.
func (d *Drainer) drainAgent(name string) {
	outboxDir := filepath.Join(agent.Dir(d.home, name), "data", ".outbox")

	// Safety: verify the outbox path is within the agents directory.
	agentsBase := filepath.Clean(agent.AgentsDir(d.home))
	if !strings.HasPrefix(filepath.Clean(outboxDir), agentsBase+string(filepath.Separator)) {
		d.logger.Error("drain: outbox path outside agents dir", "agent", name, "path", outboxDir)
		return
	}

	// List .msg files (complete responses only, ignore .pending).
	entries, err := os.ReadDir(outboxDir)
	if err != nil {
		return // no outbox dir yet — normal for agents that haven't been messaged
	}

	var msgFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".msg") {
			msgFiles = append(msgFiles, e.Name())
		}
	}
	if len(msgFiles) == 0 {
		return
	}

	// Sort by filename (timestamp-based, natural order).
	sort.Strings(msgFiles)

	// Determine target chat.
	chatID := d.targetChatID()
	if chatID == 0 {
		return // no chat available yet — files preserved for next tick
	}

	// Coordinate with turn sends — skip if a turn is active.
	if !d.turnMu.TryLock() {
		return // turn in progress, try next tick
	}
	defer d.turnMu.Unlock()

	header := agentIdentityHeader(d.cfg, name)

	for _, fname := range msgFiles {
		fpath := filepath.Join(outboxDir, fname)

		// Open with O_NOFOLLOW to atomically reject symlinks (no TOCTOU race).
		f, err := os.OpenFile(fpath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			// ELOOP means symlink — remove it. Other errors: skip.
			d.logger.Error("drain: cannot open outbox file", "agent", name, "file", fname, "error", err)
			os.Remove(fpath)
			continue
		}

		// Check file type and size on the opened fd (no TOCTOU).
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			d.logger.Error("drain: skipping non-regular file", "agent", name, "file", fname)
			os.Remove(fpath)
			continue
		}
		if info.Size() > maxOutboxFileSize {
			f.Close()
			d.logger.Error("drain: outbox file too large, removing", "agent", name, "file", fname, "size", info.Size())
			os.Remove(fpath)
			continue
		}

		content, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			d.logger.Error("drain: read outbox file", "agent", name, "file", fname, "error", err)
			continue
		}

		raw := strings.TrimSpace(string(content))
		if raw == "" {
			os.Remove(fpath)
			continue
		}

		// Parse media attachments (agent may include [IMAGE:path] etc).
		// Restrict attachment paths to this agent's data directory.
		cleanText, attachments := media.Parse(raw)
		allowedBase := filepath.Clean(filepath.Join(agent.Dir(d.home, name), "data"))
		var safeAttachments []media.Attachment
		for _, att := range attachments {
			clean := filepath.Clean(att.Path)
			if strings.HasPrefix(clean, allowedBase+string(filepath.Separator)) {
				safeAttachments = append(safeAttachments, att)
			} else {
				d.logger.Error("drain: attachment path outside agent data dir, skipping",
					"agent", name, "path", att.Path, "allowed", allowedBase)
			}
		}

		// Send text with identity header.
		allOK := true
		if cleanText != "" {
			if err := d.bot.Send(chatID, header+cleanText); err != nil {
				d.logger.Error("drain: telegram send", "agent", name, "error", err)
				allOK = false
			}
		}

		// Send safe attachments with agent identity as caption.
		// Only attempt attachments if text succeeded — prevents duplicates on retry.
		if allOK {
			caption := strings.TrimSpace(header)
			for _, att := range safeAttachments {
				sendAttachment(d.bot, d.logger, chatID, att, caption)
			}
		}

		// Only remove the file after successful send — prevents message loss.
		// Trade-off: crash after send but before delete = duplicate on restart (at-least-once).
		if allOK {
			os.Remove(fpath)
		} else {
			d.logger.Error("drain: keeping file for retry", "agent", name, "file", fname)
		}

		// Store in memory so the LLM has context awareness of agent activity.
		// Sanitize: collapse newlines to prevent prompt section injection.
		sanitized := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, truncateLog(raw, 300))
		agentName := name
		if allOK {
			d.store.AddMessage(memory.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[%s]: %s", name, sanitized),
				Agent:   &agentName,
				Stream:  "drain",
			})
		}

		d.logger.Info("drain: delivered", "agent", name, "file", fname, "len", len(raw))
	}

	// Clean up stale .pending files (older than stalePendingCutoff = likely crashed agent).
	d.cleanStalePending(outboxDir)
}

// targetChatID returns the chat ID to send drain messages to.
func (d *Drainer) targetChatID() int64 {
	// Prefer configured group ID.
	if d.cfg.Telegram.GroupID != 0 {
		return d.cfg.Telegram.GroupID
	}
	// Fall back to last known chat (private mode).
	return atomic.LoadInt64(d.chatID)
}

// cleanStalePending removes .pending files older than stalePendingCutoff.
func (d *Drainer) cleanStalePending(outboxDir string) {
	entries, err := os.ReadDir(outboxDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-stalePendingCutoff)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".pending") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fpath := filepath.Join(outboxDir, e.Name())
			d.logger.Info("drain: removing stale pending", "file", fpath)
			os.Remove(fpath)
		}
	}
}
