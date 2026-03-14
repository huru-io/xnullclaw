package mux

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// newTestDrainer builds a Drainer wired to temp dirs, in-memory store, and a mock bot.
// The returned home dir has an agents/ subdirectory ready for agent.Dir() to work.
func newTestDrainer(t *testing.T, bot *mockSender) (*Drainer, string) {
	t.Helper()

	home := t.TempDir()
	store := testStore(t)
	logger := testLogger(t)
	cfg := config.DefaultConfig()

	var chatID int64 = 42
	var mu sync.Mutex

	d := &Drainer{
		home:    home,
		backend: &agent.LocalBackend{Home: home},
		store:   store,
		bot:     bot,
		cfg:     cfg,
		logger:  logger,
		chatID:  &chatID,
		turnMu:  &mu,
	}
	return d, home
}

// createAgentOutbox sets up a minimal agent directory with config.json and
// the data/.outbox/ subdirectory, returning the outbox path.
func createAgentOutbox(t *testing.T, home, name string) string {
	t.Helper()
	dir := agent.Dir(home, name)
	outbox := filepath.Join(dir, "data", ".outbox")
	if err := os.MkdirAll(outbox, 0700); err != nil {
		t.Fatalf("create outbox dir: %v", err)
	}
	// agent.ListAll requires config.json to recognise the directory as an agent.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return outbox
}

// --- tests ---

func TestDrainAgent_EmptyOutbox(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	// Create agent dir with empty outbox.
	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for empty outbox, got %d", len(bot.sent()))
	}
}

func TestDrainAgent_NoOutboxDir(t *testing.T) {
	bot := &mockSender{}
	d, _ := newTestDrainer(t, bot)

	// Don't create any agent dir — drainAgent should return without error.
	d.drainAgent("nonexistent")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for missing outbox dir, got %d", len(bot.sent()))
	}
}

func TestDrainAgent_SingleMessage(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Write a .msg file (name must sort, e.g. timestamp-based).
	msgFile := filepath.Join(outbox, "20260101-120000.msg")
	if err := os.WriteFile(msgFile, []byte("hello from alice"), 0600); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	d.drainAgent("alice")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}

	// Message should contain the identity header + content.
	header := agentIdentityHeader(d.cfg, "alice")
	want := header + "hello from alice"
	if msgs[0].text != want {
		t.Errorf("sent text = %q, want %q", msgs[0].text, want)
	}
	if msgs[0].chatID != 42 {
		t.Errorf("chatID = %d, want 42", msgs[0].chatID)
	}

	// File should be removed after successful send.
	if _, err := os.Stat(msgFile); !os.IsNotExist(err) {
		t.Errorf("msg file should be removed after send, err = %v", err)
	}
}

func TestDrainAgent_LargeFileRejected(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Create a file larger than maxOutboxFileSize (1 MB).
	bigFile := filepath.Join(outbox, "big.msg")
	data := make([]byte, maxOutboxFileSize+1)
	if err := os.WriteFile(bigFile, data, 0600); err != nil {
		t.Fatalf("write big file: %v", err)
	}

	d.drainAgent("alice")

	// Nothing should be sent.
	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for oversized file, got %d", len(bot.sent()))
	}

	// File should be removed (not left to retry forever).
	if _, err := os.Stat(bigFile); !os.IsNotExist(err) {
		t.Errorf("oversized file should be removed, err = %v", err)
	}
}

func TestDrainAgent_SymlinkRejected(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Create a regular target file and a symlink to it in the outbox.
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret data"), 0600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(outbox, "link.msg")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	d.drainAgent("alice")

	// Symlink should not be sent.
	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for symlink, got %d", len(bot.sent()))
	}

	// Symlink should be removed from the outbox.
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("symlink should be removed, err = %v", err)
	}
}

func TestDrainAgent_TurnMuLocked(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	msgFile := filepath.Join(outbox, "20260101-120000.msg")
	if err := os.WriteFile(msgFile, []byte("should be skipped"), 0600); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	// Lock turnMu to simulate a turn in progress.
	d.turnMu.Lock()

	d.drainAgent("alice")

	d.turnMu.Unlock()

	// Nothing should be sent because TryLock fails.
	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends when turnMu locked, got %d", len(bot.sent()))
	}

	// File should be preserved for the next tick.
	if _, err := os.Stat(msgFile); err != nil {
		t.Errorf("msg file should be preserved when turn is locked, err = %v", err)
	}
}

func TestSafeDrainAll_RecoversPanic(t *testing.T) {
	store := testStore(t)
	logger := testLogger(t)
	cfg := config.DefaultConfig()

	var chatID int64 = 1
	var mu sync.Mutex

	// Use a home dir that will cause agent.ListAll to panic.
	// We achieve this indirectly: create a home dir with an agents/ dir
	// containing something that will panic when processed.
	// Actually, the simplest approach: override drainAll to panic directly.
	// But drainAll is not overridable, so instead we use a real Drainer
	// pointing to a valid home and verify safeDrainAll doesn't propagate panics.
	//
	// Since safeDrainAll wraps drainAll, and drainAll does not panic under
	// normal circumstances, we test the recovery by constructing a scenario
	// that would panic (nil pointer) via a nil store.
	tmpHome := t.TempDir()
	d := &Drainer{
		home:    tmpHome,
		backend: &agent.LocalBackend{Home: tmpHome},
		store:   store,
		bot:     &mockSender{},
		cfg:     cfg,
		logger:  logger,
		chatID:  &chatID,
		turnMu:  &mu,
	}

	// Create an agent that will be found by ListAll.
	outbox := createAgentOutbox(t, d.home, "panicker")
	if err := os.WriteFile(filepath.Join(outbox, "test.msg"), []byte("content"), 0600); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	// Nil out the store to trigger a panic when drainAgent tries to use it
	// after a successful send.
	d.store = nil

	// safeDrainAll should recover from the nil-pointer panic and not propagate it.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		d.safeDrainAll()
	}()

	if didPanic {
		t.Fatal("safeDrainAll should recover panics, but the panic propagated")
	}
}

func TestDrainAgent_MultipleMessages_SortedOrder(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Write multiple .msg files; they should be sent in sorted order.
	files := []struct {
		name    string
		content string
	}{
		{"20260101-120002.msg", "third"},
		{"20260101-120000.msg", "first"},
		{"20260101-120001.msg", "second"},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(outbox, f.name), []byte(f.content), 0600); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}

	d.drainAgent("alice")

	msgs := bot.sent()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(msgs))
	}

	expected := []string{"first", "second", "third"}
	for i, want := range expected {
		if !strings.HasSuffix(msgs[i].text, want) {
			t.Errorf("message %d: got %q, want suffix %q", i, msgs[i].text, want)
		}
	}
}

func TestDrainAgent_EmptyFileRemoved(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Write an empty .msg file (whitespace-only counts as empty).
	emptyFile := filepath.Join(outbox, "empty.msg")
	if err := os.WriteFile(emptyFile, []byte("   \n  "), 0600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	d.drainAgent("alice")

	// Nothing should be sent.
	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for empty content, got %d", len(bot.sent()))
	}

	// File should be removed.
	if _, err := os.Stat(emptyFile); !os.IsNotExist(err) {
		t.Errorf("empty file should be removed, err = %v", err)
	}
}

func TestDrainAgent_StoresInMemory(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")
	if err := os.WriteFile(filepath.Join(outbox, "test.msg"), []byte("remember this"), 0600); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	d.drainAgent("alice")

	// Verify the message was stored in the drain stream.
	msgs, err := d.store.RecentMessages("drain", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("role = %q, want %q", msgs[0].Role, "assistant")
	}
	if !strings.Contains(msgs[0].Content, "[alice]") {
		t.Errorf("stored content should contain agent name, got %q", msgs[0].Content)
	}
	if msgs[0].Stream != "drain" {
		t.Errorf("stream = %q, want %q", msgs[0].Stream, "drain")
	}
	agentName := "alice"
	if msgs[0].Agent == nil || *msgs[0].Agent != agentName {
		t.Errorf("stored agent = %v, want %q", msgs[0].Agent, agentName)
	}
}

func TestDrainAgent_NoChatID_PreservesFiles(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	// Set chatID to 0 (no chat available yet) and ensure no group mode.
	var zeroChatID int64
	d.chatID = &zeroChatID

	outbox := createAgentOutbox(t, home, "alice")
	msgFile := filepath.Join(outbox, "test.msg")
	if err := os.WriteFile(msgFile, []byte("waiting for chat"), 0600); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	d.drainAgent("alice")

	// Nothing sent — no chat ID available.
	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends with zero chatID, got %d", len(bot.sent()))
	}

	// File should be preserved for next tick.
	if _, err := os.Stat(msgFile); err != nil {
		t.Errorf("msg file should be preserved when no chatID, err = %v", err)
	}
}

func TestDrainAgent_StoreMessage_UsesMemory(t *testing.T) {
	// Verify that after draining, we can query the drain message from memory.
	bot := &mockSender{}
	store, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	home := t.TempDir()
	logger := testLogger(t)
	cfg := config.DefaultConfig()

	var chatID int64 = 99
	var mu sync.Mutex

	d := &Drainer{
		home:    home,
		backend: &agent.LocalBackend{Home: home},
		store:   store,
		bot:     bot,
		cfg:     cfg,
		logger:  logger,
		chatID:  &chatID,
		turnMu:  &mu,
	}

	outbox := createAgentOutbox(t, home, "bob")
	if err := os.WriteFile(filepath.Join(outbox, "test.msg"), []byte("important update"), 0600); err != nil {
		t.Fatalf("write msg: %v", err)
	}

	d.drainAgent("bob")

	// Drain stream should have exactly one message.
	msgs, err := store.RecentMessages("drain", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 drain message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[bob]") {
		t.Errorf("expected content to contain [bob], got %q", msgs[0].Content)
	}
}

// --- cleanStalePending tests ---

func TestCleanStalePending_RemovesOld(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Create a .pending file with an old modification time.
	pendingFile := filepath.Join(outbox, "20260101-120000.pending")
	if err := os.WriteFile(pendingFile, []byte("in progress"), 0600); err != nil {
		t.Fatalf("write pending file: %v", err)
	}

	// Set mtime to well past the stalePendingCutoff (10 minutes).
	oldTime := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(pendingFile, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	d.cleanStalePending(outbox)

	if _, err := os.Stat(pendingFile); !os.IsNotExist(err) {
		t.Errorf("stale .pending file should be removed, err = %v", err)
	}
}

func TestCleanStalePending_KeepsRecent(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Create a .pending file with a recent modification time.
	pendingFile := filepath.Join(outbox, "20260313-120000.pending")
	if err := os.WriteFile(pendingFile, []byte("in progress"), 0600); err != nil {
		t.Fatalf("write pending file: %v", err)
	}

	// Set mtime to recent (well within the stalePendingCutoff of 10 minutes).
	recentTime := time.Now().Add(-1 * time.Minute)
	if err := os.Chtimes(pendingFile, recentTime, recentTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	d.cleanStalePending(outbox)

	if _, err := os.Stat(pendingFile); err != nil {
		t.Errorf("recent .pending file should be preserved, err = %v", err)
	}
}

func TestCleanStalePending_IgnoresNonPending(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)

	outbox := createAgentOutbox(t, home, "alice")

	// Create a .msg file with an old modification time.
	msgFile := filepath.Join(outbox, "20260101-120000.msg")
	if err := os.WriteFile(msgFile, []byte("a message"), 0600); err != nil {
		t.Fatalf("write msg file: %v", err)
	}

	// Set mtime to old.
	oldTime := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(msgFile, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	d.cleanStalePending(outbox)

	// .msg file should be untouched.
	if _, err := os.Stat(msgFile); err != nil {
		t.Errorf(".msg file should be untouched, err = %v", err)
	}
}

// --- drainAgentExec tests ---

func TestDrainAgentExec_SingleMessage(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	// Mock ExecSync to return outbox output.
	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:20260101-120000.msg---\nhello from alice\n", nil
		},
	}

	// Create agent so ListAll finds it (drainAll calls ListAll).
	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}

	header := agentIdentityHeader(d.cfg, "alice")
	want := header + "hello from alice"
	if msgs[0].text != want {
		t.Errorf("sent text = %q, want %q", msgs[0].text, want)
	}
}

func TestDrainAgentExec_MultipleMessages(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:20260101-120000.msg---\nfirst\n---FILE:20260101-120001.msg---\nsecond\n", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	msgs := bot.sent()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 sends, got %d", len(msgs))
	}
	if !strings.HasSuffix(msgs[0].text, "first") {
		t.Errorf("msg 0: got %q, want suffix %q", msgs[0].text, "first")
	}
	if !strings.HasSuffix(msgs[1].text, "second") {
		t.Errorf("msg 1: got %q, want suffix %q", msgs[1].text, "second")
	}
}

func TestDrainAgentExec_EmptyOutput(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for empty exec output, got %d", len(bot.sent()))
	}
}

func TestDrainAgentExec_ExecError(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "", context.DeadlineExceeded
		},
	}

	createAgentOutbox(t, home, "alice")

	// Should not panic or send anything.
	d.drainAgent("alice")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends on exec error, got %d", len(bot.sent()))
	}
}

func TestDrainAgentExec_TurnMuLocked(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:test.msg---\nhello\n", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	// Lock turnMu — should cause drain to skip delivery.
	d.turnMu.Lock()
	d.drainAgent("alice")
	d.turnMu.Unlock()

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends when turnMu is locked, got %d", len(bot.sent()))
	}
}

func TestDrainAgentExec_NoChatID(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:test.msg---\nhello\n", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	// Set chatID to 0 — should skip delivery.
	var zeroChatID int64
	d.chatID = &zeroChatID

	d.drainAgent("alice")

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends when chatID is 0, got %d", len(bot.sent()))
	}
}

func TestDrainAgentExec_DeletesOnlyDelivered(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	var execCalls []string
	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			script := strings.Join(cmd, " ")
			execCalls = append(execCalls, script)
			// First call: read script returns two files.
			if strings.Contains(script, "printf") {
				return "---FILE:a.msg---\nfirst\n---FILE:b.msg---\nsecond\n", nil
			}
			// Second call: rm command.
			return "", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	// Should have 2 exec calls: read + delete.
	if len(execCalls) != 2 {
		t.Fatalf("expected 2 exec calls, got %d", len(execCalls))
	}
	// Delete call should reference both files.
	if !strings.Contains(execCalls[1], "a.msg") || !strings.Contains(execCalls[1], "b.msg") {
		t.Errorf("delete cmd should reference both files: %q", execCalls[1])
	}
}

func TestDrainAgentExec_StoresInMemory(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:test.msg---\nremember this via exec\n", nil
		},
	}

	createAgentOutbox(t, home, "alice")

	d.drainAgent("alice")

	msgs, err := d.store.RecentMessages("drain", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[alice]") {
		t.Errorf("stored content should contain agent name, got %q", msgs[0].Content)
	}
}

// --- drainAllParallel tests ---

func TestDrainAllParallel_DeliversAllAgents(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	// Track exec calls per agent.
	var mu sync.Mutex
	execCalls := map[string]int{}

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			mu.Lock()
			execCalls[name]++
			mu.Unlock()
			script := strings.Join(cmd, " ")
			if strings.Contains(script, "printf") {
				// Each agent returns a unique message.
				agentName := strings.TrimPrefix(name, "xnc-")
				agentName = agentName[strings.Index(agentName, "-")+1:] // strip instanceID
				return "---FILE:test.msg---\nhello from " + agentName + "\n", nil
			}
			return "", nil
		},
	}

	// Create 3 agents.
	createAgentOutbox(t, home, "alice")
	createAgentOutbox(t, home, "bob")
	createAgentOutbox(t, home, "carol")

	// drainAll with >1 agent in K8s mode should use the parallel path.
	d.drainAll()

	msgs := bot.sent()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 sends (one per agent), got %d", len(msgs))
	}

	// All agents should have been drained.
	var foundAlice, foundBob, foundCarol bool
	for _, m := range msgs {
		if strings.Contains(m.text, "hello from alice") {
			foundAlice = true
		}
		if strings.Contains(m.text, "hello from bob") {
			foundBob = true
		}
		if strings.Contains(m.text, "hello from carol") {
			foundCarol = true
		}
	}
	if !foundAlice || !foundBob || !foundCarol {
		t.Errorf("not all agents drained: alice=%v bob=%v carol=%v", foundAlice, foundBob, foundCarol)
	}
}

func TestDrainAllParallel_OneAgentError_OthersDelivered(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			script := strings.Join(cmd, " ")
			if strings.Contains(script, "printf") {
				// bob's exec fails.
				if strings.Contains(name, "bob") {
					return "", context.DeadlineExceeded
				}
				agentName := strings.TrimPrefix(name, "xnc-")
				agentName = agentName[strings.Index(agentName, "-")+1:]
				return "---FILE:test.msg---\nhello from " + agentName + "\n", nil
			}
			return "", nil
		},
	}

	createAgentOutbox(t, home, "alice")
	createAgentOutbox(t, home, "bob")

	d.drainAll()

	// Only alice should be delivered (bob's exec failed).
	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send (alice only, bob failed), got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].text, "hello from alice") {
		t.Errorf("expected alice's message, got %q", msgs[0].text)
	}
}

func TestDrainAllParallel_TurnMuLocked_BatchSkipped(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:test.msg---\nhello\n", nil
		},
	}

	createAgentOutbox(t, home, "alice")
	createAgentOutbox(t, home, "bob")

	// Lock turnMu — entire batch should be skipped.
	d.turnMu.Lock()
	d.drainAll()
	d.turnMu.Unlock()

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends when turnMu is locked, got %d", len(bot.sent()))
	}
}

func TestDrainAll_SingleAgentK8s_UsesSerialPath(t *testing.T) {
	bot := &mockSender{}
	d, home := newTestDrainer(t, bot)
	d.mode = "kubernetes"

	d.docker = &docker.MockOps{
		ExecSyncFn: func(ctx context.Context, name string, cmd []string, stdin io.Reader) (string, error) {
			return "---FILE:test.msg---\nhello solo\n", nil
		},
	}

	// Only one agent — should use the serial (non-parallel) path.
	createAgentOutbox(t, home, "alice")

	d.drainAll()

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].text, "hello solo") {
		t.Errorf("expected 'hello solo', got %q", msgs[0].text)
	}
}

// --- safeOutboxFilename regex tests ---

func TestSafeOutboxFilename(t *testing.T) {
	valid := []string{
		"20260101-120000.msg",
		"a.msg",
		"test_file-1.2.msg",
		"1msg.msg",
	}
	for _, f := range valid {
		if !safeOutboxFilename.MatchString(f) {
			t.Errorf("expected %q to match", f)
		}
	}

	invalid := []string{
		"../../etc/passwd.msg",      // path traversal
		"-rf.msg",                   // leading dash
		".hidden.msg",              // leading dot
		"$(evil).msg",              // shell injection
		"`cmd`.msg",                // backtick injection
		"file name.msg",            // space
		"file;rm -rf /.msg",        // semicolon
		"file|cat /etc/passwd.msg", // pipe
		"file\nname.msg",           // newline
		"--no-preserve-root.msg",   // flag injection
		"file.txt",                 // wrong extension
		"",                         // empty
		".msg",                     // just extension
		"../evil.msg",              // relative path
		"a/b.msg",                  // slash
	}
	for _, f := range invalid {
		if safeOutboxFilename.MatchString(f) {
			t.Errorf("expected %q to NOT match", f)
		}
	}
}
