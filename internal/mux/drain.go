// Package mux — drain.go periodically collects agent output from .outbox/
// directories and sends it directly to Telegram, bypassing the LLM.
package mux

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
	"github.com/jotavich/xnullclaw/internal/telegram"
)

// safeOutboxFilename matches only safe .msg filenames: must start with alphanumeric,
// body may contain alphanumeric/dots/hyphens/underscores, must end with .msg.
var safeOutboxFilename = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*\.msg$`)

// Drain timing constants — co-located for easy tuning.
const (
	drainInterval      = 5 * time.Second
	drainPruneTTL      = time.Hour
	drainPruneInterval = time.Minute
	stalePendingCutoff = 10 * time.Minute
	maxOutboxFileSize  = 1 << 20 // 1 MB
)

// outboxReadScript is the shell script used to read agent outbox files via exec.
// Shared between single-agent and parallel drain paths.
const outboxReadScript = `cd /nullclaw-data/.outbox 2>/dev/null || exit 0
for f in *.msg; do
    [ -f "$f" ] || continue
    printf '---FILE:%s---\n' "$f"
    cat "$f"
done`

// Drainer periodically reads agent .outbox/ directories and sends
// completed responses directly to Telegram with identity headers.
type Drainer struct {
	home        string
	mediaTmpDir string // host path for retrieved container files
	backend     agent.Backend
	store       *memory.Store
	bot         telegram.Sender
	cfg         *config.Config
	logger      *logging.Logger

	// mode is the runtime mode ("local", "docker", "kubernetes").
	mode string

	// docker is the container ops interface, used for exec-based drain in K8s mode.
	docker docker.Ops

	// chatID is the Telegram chat to send to. In group mode we use
	// cfg.Telegram.GroupID; in private mode we use the last known chatID.
	chatID *int64 // pointer to mux's lastChatID (atomic)

	// turnMu prevents drain sends from interleaving with turn sends.
	turnMu *sync.Mutex

	// bridge, when non-nil, is checked to skip agents that are connected
	// via WebSocket (their output comes through the bridge, not .outbox/).
	bridge *Bridge

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
// In K8s mode, agents are drained in parallel to avoid sequential exec latency
// (each exec round-trip takes 1-4s; sequential drain of N agents would take N*2-8s).
func (d *Drainer) drainAll() {
	agents, err := d.backend.ListAll()
	if err != nil {
		d.logger.Error("drain: list agents failed", "error", err)
		return
	}

	if d.mode == "kubernetes" && len(agents) > 1 {
		d.drainAllParallel(agents)
	} else {
		for _, a := range agents {
			d.drainAgent(a.Name)
		}
	}
	// Prune drain and bridge messages periodically (not every tick — once per minute is enough).
	if time.Since(d.lastPrune) > drainPruneInterval {
		for _, stream := range []string{"drain", "bridge"} {
			if n, err := d.store.PruneOldMessages(stream, drainPruneTTL); err != nil {
				d.logger.Error("prune failed", "stream", stream, "error", err)
			} else if n > 0 {
				d.logger.Info("pruned old messages", "stream", stream, "count", n)
			}
		}
		d.lastPrune = time.Now()
	}
}

// execResult holds the output of a parallel outbox read from a K8s agent pod.
type execResult struct {
	name   string
	output string
}

// drainAllParallel reads all agent outboxes concurrently via K8s exec,
// then delivers messages sequentially (Telegram + memory store are not concurrent-safe
// and turnMu must be held during sends).
//
// Without parallelism, N agents would take N*2-8s of sequential exec round-trips,
// easily exceeding the 5s drain interval.
// maxConcurrentExec caps parallel K8s exec connections to avoid saturating
// the API server and kubelet when there are many agents.
const maxConcurrentExec = 8

func (d *Drainer) drainAllParallel(agents []agent.Info) {
	// Phase 1: parallel reads with bounded concurrency.
	results := make([]execResult, len(agents))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentExec)

	for i, a := range agents {
		if d.bridge != nil && d.bridge.IsConnected(a.Name) {
			continue
		}
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			// Bounded semaphore wait — don't block indefinitely if all slots are occupied.
			select {
			case sem <- struct{}{}:
			case <-time.After(30 * time.Second):
				return // timed out waiting for slot
			}
			defer func() { <-sem }()
			cn := agent.ContainerName(d.home, name)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			output, err := d.docker.ExecSync(ctx, cn, []string{"sh", "-c", outboxReadScript}, nil)
			if err != nil || output == "" {
				return
			}
			results[idx] = execResult{name: name, output: output}
		}(i, a.Name)
	}
	wg.Wait()

	// Phase 2+3: sequential delivery + deletion (requires turnMu).
	// Lock once for the entire batch — data is in memory so discarding
	// the batch on contention wastes the exec work already done.
	if !d.turnMu.TryLock() {
		return // files preserved in pods for next tick
	}
	defer d.turnMu.Unlock()

	for _, r := range results {
		if r.output == "" {
			continue
		}
		d.deliverAndDeleteExecLocked(r.name, r.output)
	}
}

// drainAgent reads and sends all completed messages from a single agent's outbox.
// Skips agents connected via WebSocket bridge (their output arrives in real-time).
func (d *Drainer) drainAgent(name string) {
	if d.bridge != nil && d.bridge.IsConnected(name) {
		return
	}
	if d.mode == "kubernetes" {
		d.drainAgentExec(name)
		return
	}
	d.drainAgentFS(name)
}

// drainAgentExec drains a single agent's outbox via K8s exec API.
// Used when draining a single agent (not in the parallel path).
func (d *Drainer) drainAgentExec(name string) {
	cn := agent.ContainerName(d.home, name)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	output, err := d.docker.ExecSync(ctx, cn, []string{"sh", "-c", outboxReadScript}, nil)
	if err != nil || output == "" {
		return
	}

	d.deliverAndDeleteExec(name, output)
}

// deliverAndDeleteExec handles Phase 2 (deliver to Telegram) and Phase 3 (delete files)
// for exec-based outbox drain. Acquires turnMu itself — used by the single-agent path.
func (d *Drainer) deliverAndDeleteExec(name, output string) {
	// Determine target chat.
	chatID := muxTargetChatID(d.cfg, d.chatID)
	if chatID == 0 {
		return
	}

	// Coordinate with turn sends.
	if !d.turnMu.TryLock() {
		return
	}
	defer d.turnMu.Unlock()

	d.deliverAndDeleteExecCore(name, output, chatID)
}

// deliverAndDeleteExecLocked is the same as deliverAndDeleteExec but assumes
// turnMu is already held. Used by drainAllParallel which locks once for the batch.
func (d *Drainer) deliverAndDeleteExecLocked(name, output string) {
	chatID := muxTargetChatID(d.cfg, d.chatID)
	if chatID == 0 {
		return
	}
	d.deliverAndDeleteExecCore(name, output, chatID)
}

// deliverAndDeleteExecCore is the shared implementation for exec-based delivery.
// Caller must hold turnMu.
func (d *Drainer) deliverAndDeleteExecCore(name, output string, chatID int64) {
	header := agentIdentityHeader(d.cfg, name)

	// Phase 2: Parse and deliver each file.
	var deliveredFiles []string
	sections := strings.Split(output, "---FILE:")
	for _, sec := range sections {
		if sec == "" {
			continue
		}
		idx := strings.Index(sec, "---\n")
		if idx < 0 {
			continue
		}
		fileName := sec[:idx]
		// Validate filename at parse time — only safe names enter deliveredFiles.
		if !safeOutboxFilename.MatchString(fileName) {
			d.logger.Error("drain: unsafe outbox filename, skipping", "agent", name, "file", fileName)
			continue
		}
		content := strings.TrimSpace(sec[idx+4:])
		if content == "" {
			deliveredFiles = append(deliveredFiles, fileName)
			continue
		}

		// Parse media attachments.
		cleanText, attachments := media.Parse(content)

		// Resolve container paths → host paths by retrieving files.
		if len(attachments) > 0 && d.mediaTmpDir != "" {
			retrieveCtx, retrieveCancel := context.WithTimeout(context.Background(), 60*time.Second)
			attachments = resolveContainerAttachments(retrieveCtx, d.docker, d.home, name, d.mediaTmpDir, attachments, d.logger.Error)
			retrieveCancel()
		}

		allOK := true
		if cleanText != "" {
			if err := d.bot.Send(chatID, header+cleanText); err != nil {
				d.logger.Error("drain: telegram send (exec)", "agent", name, "error", err)
				allOK = false
			}
		}

		if allOK {
			caption := strings.TrimSpace(header)
			for _, att := range attachments {
				sendAttachment(d.bot, d.logger, chatID, att, caption)
			}
		}

		if allOK {
			deliveredFiles = append(deliveredFiles, fileName)

			// Store in memory.
			sanitized := config.SanitizeText(content, 300)
			agentName := name
			if err := d.store.AddMessage(memory.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[%s]: %s", name, sanitized),
				Agent:   &agentName,
				Stream:  "drain",
			}); err != nil {
				d.logger.Error("drain: store message failed (exec)", "agent", name, "error", err)
			}

			d.logger.Info("drain: delivered (exec)", "agent", name, "len", len(content))
		} else {
			d.logger.Error("drain: keeping file for retry (exec)", "agent", name, "file", fileName)
		}
	}

	// Phase 3: Delete only successfully delivered files.
	// Uses array form (no shell) to prevent injection from filenames.
	if len(deliveredFiles) > 0 {
		cn := agent.ContainerName(d.home, name)
		rmCmd := []string{"rm", "-f", "--"}
		for _, f := range deliveredFiles {
			rmCmd = append(rmCmd, "/nullclaw-data/.outbox/"+f)
		}
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rmCancel()
		if _, err := d.docker.ExecSync(rmCtx, cn, rmCmd, nil); err != nil {
			d.logger.Error("drain: failed to delete delivered files (exec)", "agent", name, "error", err)
		}
	}
}

// drainAgentFS reads and sends all completed messages from a single agent's
// outbox directory on the local filesystem.
func (d *Drainer) drainAgentFS(name string) {
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

	// Determine target chat (shared helper with scheduler).
	chatID := muxTargetChatID(d.cfg, d.chatID)
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
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove failed", "file", fname, "error", rmErr)
			}
			continue
		}

		// Check file type and size on the opened fd (no TOCTOU).
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			d.logger.Error("drain: skipping non-regular file", "agent", name, "file", fname)
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove failed", "file", fname, "error", rmErr)
			}
			continue
		}
		if info.Size() > maxOutboxFileSize {
			f.Close()
			d.logger.Error("drain: outbox file too large, removing", "agent", name, "file", fname, "size", info.Size())
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove failed", "file", fname, "error", rmErr)
			}
			continue
		}

		content, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			d.logger.Error("drain: read outbox file, keeping for retry", "agent", name, "file", fname, "error", err)
			continue
		}

		raw := strings.TrimSpace(string(content))
		if raw == "" {
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove empty file failed", "file", fname, "error", rmErr)
			}
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
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove delivered file failed", "file", fname, "error", rmErr)
			}
		} else {
			d.logger.Error("drain: keeping file for retry", "agent", name, "file", fname)
		}

		// Store in memory so the LLM has context awareness of agent activity.
		// Sanitize: collapse newlines + control chars to prevent prompt injection.
		sanitized := config.SanitizeText(raw, 300)
		agentName := name
		if allOK {
			if err := d.store.AddMessage(memory.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("[%s]: %s", name, sanitized),
				Agent:   &agentName,
				Stream:  "drain",
			}); err != nil {
				d.logger.Error("drain: store message failed", "agent", name, "error", err)
			}
		}

		d.logger.Info("drain: delivered", "agent", name, "file", fname, "len", len(raw))
	}

	// Clean up stale .pending files (older than stalePendingCutoff = likely crashed agent).
	d.cleanStalePending(outboxDir)
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
			if rmErr := os.Remove(fpath); rmErr != nil {
				d.logger.Debug("drain: remove stale pending failed", "file", fpath, "error", rmErr)
			}
		}
	}
}
