// Package mux — drain.go periodically collects agent output from .outbox/
// directories and sends it directly to Telegram, bypassing the LLM.
package mux

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
	"github.com/jotavich/xnullclaw/internal/telegram"
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
}

// Run polls agent outboxes on the given interval until ctx is cancelled.
func (d *Drainer) Run(interval time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			d.drainAll()
		}
	}
}

// drainAll iterates all known agents and drains their outboxes.
func (d *Drainer) drainAll() {
	agents, err := agent.ListAll(d.home)
	if err != nil {
		return
	}
	for _, a := range agents {
		d.drainAgent(a.Name)
	}
	// Prune drain messages older than 1 hour to prevent unbounded growth.
	d.store.PruneOldMessages("drain", time.Hour)
}

// drainAgent reads and sends all completed messages from a single agent's outbox.
func (d *Drainer) drainAgent(name string) {
	outboxDir := filepath.Join(agent.Dir(d.home, name), "data", ".outbox")

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
		return // no chat available yet
	}

	// Coordinate with turn sends — skip if a turn is active.
	if !d.turnMu.TryLock() {
		return // turn in progress, try next tick
	}
	defer d.turnMu.Unlock()

	header := agentIdentityHeader(d.cfg, name)

	for _, fname := range msgFiles {
		fpath := filepath.Join(outboxDir, fname)

		// Symlink check: only read regular files within the outbox dir.
		info, err := os.Lstat(fpath)
		if err != nil || info.Mode()&fs.ModeSymlink != 0 {
			d.logger.Error("drain: skipping non-regular file", "agent", name, "file", fname)
			os.Remove(fpath)
			continue
		}

		content, err := os.ReadFile(fpath)
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
		cleanText, attachments := media.Parse(raw)

		// Send text with identity header.
		sendOK := true
		if cleanText != "" {
			if err := d.bot.Send(chatID, header+cleanText); err != nil {
				d.logger.Error("drain: telegram send", "agent", name, "error", err)
				sendOK = false
			}
		}

		// Send attachments with agent identity as caption.
		caption := strings.TrimSpace(header)
		for _, att := range attachments {
			sendAttachment(d.bot, d.logger, chatID, att, caption)
		}

		// Only remove the file after successful send — prevents message loss.
		if sendOK {
			os.Remove(fpath)
		} else {
			d.logger.Error("drain: keeping file for retry", "agent", name, "file", fname)
		}

		// Tip off mux: store in memory so the LLM sees it in context.
		agentName := name
		d.store.AddMessage(memory.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("[%s]: %s", name, truncateLog(raw, 500)),
			Agent:   &agentName,
			Stream:  "drain",
		})

		d.logger.Info("drain: delivered", "agent", name, "file", fname, "len", len(raw))
	}

	// Clean up stale .pending files (older than 10 minutes = likely crashed agent).
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

// cleanStalePending removes .pending files older than 10 minutes.
func (d *Drainer) cleanStalePending(outboxDir string) {
	entries, err := os.ReadDir(outboxDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-10 * time.Minute)
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
