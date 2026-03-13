package mux

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
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
		home:   home,
		store:  store,
		bot:    bot,
		cfg:    cfg,
		logger: logger,
		chatID: &chatID,
		turnMu: &mu,
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
	d := &Drainer{
		home:   t.TempDir(),
		store:  store,
		bot:    &mockSender{},
		cfg:    cfg,
		logger: logger,
		chatID: &chatID,
		turnMu: &mu,
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
		home:   home,
		store:  store,
		bot:    bot,
		cfg:    cfg,
		logger: logger,
		chatID: &chatID,
		turnMu: &mu,
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
