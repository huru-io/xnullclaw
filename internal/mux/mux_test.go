package mux

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/logging"
	"github.com/jotavich/xnullclaw/internal/media"
	"github.com/jotavich/xnullclaw/internal/memory"
)

// mockSender records all Send calls for test assertions.
type mockSender struct {
	mu       sync.Mutex
	messages []mockMsg
}

type mockMsg struct {
	chatID int64
	text   string
}

func (m *mockSender) Send(chatID int64, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, mockMsg{chatID, text})
	return nil
}
func (m *mockSender) SendTyping(int64) error                          { return nil }
func (m *mockSender) SendPhoto(int64, string, string) error           { return nil }
func (m *mockSender) SendDocument(int64, string, string) error        { return nil }
func (m *mockSender) SendVoice(int64, string) error                   { return nil }
func (m *mockSender) SendAudio(int64, string, string) error           { return nil }
func (m *mockSender) SendVideo(int64, string, string) error           { return nil }
func (m *mockSender) sent() []mockMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]mockMsg, len(m.messages))
	copy(cp, m.messages)
	return cp
}

func testLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, err := logging.New(&config.LoggingConfig{}, t.TempDir())
	if err != nil {
		t.Fatalf("create test logger: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func dummyTask(desc string, ctx *string) memory.ScheduledTask {
	return memory.ScheduledTask{
		ID:          1,
		Description: desc,
		TriggerAt:   time.Now(),
		Status:      "pending",
		Created:     time.Now(),
		Context:     ctx,
	}
}

func TestIsHeartbeatOK(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"HEARTBEAT_OK", true},
		{"heartbeat_ok", true},
		{"  HEARTBEAT_OK  ", true},
		{"\tHEARTBEAT_OK\n", true},
		{"HEARTBEAT_OK but I noticed something", false},
		{"", false},
		{"ok", false},
		{"HEARTBEAT_O", false},
	}
	for _, tt := range tests {
		if got := IsHeartbeatOK(tt.input); got != tt.want {
			t.Errorf("IsHeartbeatOK(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestMuxTargetChatID_GroupMode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.GroupID = -100999

	var lastChat int64 = 12345
	got := muxTargetChatID(cfg, &lastChat)
	if got != -100999 {
		t.Errorf("group mode: got %d, want -100999", got)
	}
}

func TestMuxTargetChatID_PrivateMode(t *testing.T) {
	cfg := config.DefaultConfig()
	// GroupID = 0 (default, private mode).

	var lastChat int64 = 42
	got := muxTargetChatID(cfg, &lastChat)
	if got != 42 {
		t.Errorf("private mode: got %d, want 42", got)
	}
}

func TestMuxTargetChatID_PrivateNoChat(t *testing.T) {
	cfg := config.DefaultConfig()
	var lastChat int64 = 0
	got := muxTargetChatID(cfg, &lastChat)
	if got != 0 {
		t.Errorf("no chat: got %d, want 0", got)
	}
}

func TestTruncateLog(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncateLog(tt.input, tt.n)
		if got != tt.want {
			t.Errorf("truncateLog(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
		}
	}
}

func TestAgentIdentityHeader_WithEmoji(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Identities = map[string]config.AgentIdentity{
		"alice": {Emoji: "🔮"},
	}
	got := agentIdentityHeader(cfg, "alice")
	want := "🔮 alice\n\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAgentIdentityHeader_NoEmoji(t *testing.T) {
	cfg := config.DefaultConfig()
	got := agentIdentityHeader(cfg, "bob")
	want := "bob\n\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRedactToolArgs_PassThrough(t *testing.T) {
	args := map[string]any{"key": "model", "value": "gpt-4o"}
	got := redactToolArgs("send_to_agent", args)
	if got["value"] != "gpt-4o" {
		t.Errorf("non-sensitive tool should pass through, got %v", got)
	}
}

func TestRedactToolArgs_Nil(t *testing.T) {
	got := redactToolArgs("anything", nil)
	if got != nil {
		t.Errorf("nil args should return nil, got %v", got)
	}
}

func TestRedactToolArgs_RedactsSecret(t *testing.T) {
	args := map[string]any{
		"agent": "alice",
		"key":   "openai_key",
		"value": "sk-1234567890abcdef1234567890abcdef",
	}
	got := redactToolArgs("update_agent_config", args)
	v, ok := got["value"].(string)
	if !ok {
		t.Fatalf("expected string value, got %T", got["value"])
	}
	// Value should be redacted (not the original).
	if v == "sk-1234567890abcdef1234567890abcdef" {
		t.Error("secret value should be redacted")
	}
	// Should contain asterisks.
	if !containsStr(v, "****") {
		t.Errorf("redacted value should contain asterisks: %q", v)
	}
	// Agent key should pass through unchanged.
	if got["agent"] != "alice" {
		t.Errorf("agent should be unchanged, got %v", got["agent"])
	}
}

func TestRedactToolArgs_NonRedactedKey(t *testing.T) {
	args := map[string]any{
		"key":   "model",
		"value": "gpt-4o",
	}
	got := redactToolArgs("update_agent_config", args)
	if got["value"] != "gpt-4o" {
		t.Errorf("non-redacted key should pass through, got %v", got["value"])
	}
}

func TestRedactToolArgs_UnknownKey(t *testing.T) {
	args := map[string]any{
		"key":   "nonexistent_key",
		"value": "some-value",
	}
	got := redactToolArgs("update_agent_config", args)
	if got["value"] != "some-value" {
		t.Errorf("unknown key should pass through, got %v", got["value"])
	}
}

func TestRedactToolArgs_EmptyKey(t *testing.T) {
	args := map[string]any{
		"key":   "",
		"value": "some-value",
	}
	got := redactToolArgs("update_agent_config", args)
	if got["value"] != "some-value" {
		t.Errorf("empty key should pass through, got %v", got["value"])
	}
}

func TestFormatScheduledTaskMessage(t *testing.T) {
	// Import needed types.
	msg := formatScheduledTaskMessage(dummyTask("Check alice", nil))
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !contains(msg, "SCHEDULED TASK") {
		t.Errorf("missing SCHEDULED TASK prefix: %s", msg)
	}
	if !contains(msg, "<task-description>") {
		t.Errorf("missing task-description tag: %s", msg)
	}
}

func TestFormatScheduledTaskMessage_WithContext(t *testing.T) {
	ctx := "agent=alice"
	msg := formatScheduledTaskMessage(dummyTask("Follow up", &ctx))
	if !contains(msg, "<task-context>") {
		t.Errorf("missing task-context tag: %s", msg)
	}
}

func TestFormatHeartbeatMessage(t *testing.T) {
	signals := []string{"Agent alice sent 3 messages", "Agent bob silent for 3h"}
	msg := formatHeartbeatMessage(signals)
	if !contains(msg, "[HEARTBEAT]") {
		t.Errorf("missing HEARTBEAT prefix: %s", msg)
	}
	if !contains(msg, "HEARTBEAT_OK") {
		t.Errorf("missing HEARTBEAT_OK instruction: %s", msg)
	}
	for _, sig := range signals {
		if !contains(msg, sig) {
			t.Errorf("missing signal %q in: %s", sig, msg)
		}
	}
}

func TestEffectiveHeartbeatInterval(t *testing.T) {
	s := &Scheduler{consecutiveNoops: 0}

	// Below threshold: base interval.
	got := s.effectiveHeartbeatInterval(30)
	if got != 30*60*1e9 { // 30 minutes in nanoseconds
		t.Errorf("0 noops: got %v, want 30m", got)
	}

	// At threshold: 2x.
	s.consecutiveNoops = heartbeatNoopThreshold
	got = s.effectiveHeartbeatInterval(30)
	expected := 30 * 60 * 1e9 * 2 // 60 minutes
	if got != time.Duration(expected) {
		t.Errorf("threshold noops: got %v, want 60m", got)
	}

	// Max backoff.
	s.consecutiveNoops = heartbeatNoopThreshold + heartbeatMaxBackoff + 10
	got = s.effectiveHeartbeatInterval(30)
	maxExpected := 30 * 60 * 1e9 * (1 << heartbeatMaxBackoff)
	if got != time.Duration(maxExpected) {
		t.Errorf("max backoff: got %v, want %v", got, time.Duration(maxExpected))
	}
}

// --- deliverTurnResult tests (#7) ---

func TestDeliverTurnResult_TextOnly(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	deliverTurnResult(bot, logger, turnResult{chatID: 123, text: "hello"})

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
	if msgs[0].chatID != 123 || msgs[0].text != "hello" {
		t.Errorf("got %+v", msgs[0])
	}
}

func TestDeliverTurnResult_ErrMsg(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	deliverTurnResult(bot, logger, turnResult{chatID: 42, errMsg: "something broke"})

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
	if msgs[0].text != "something broke" {
		t.Errorf("got %q", msgs[0].text)
	}
}

func TestDeliverTurnResult_ErrMsgPreventsTextSend(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	// errMsg takes priority — text should NOT be sent (#11).
	deliverTurnResult(bot, logger, turnResult{chatID: 1, errMsg: "err", text: "should not send"})

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 send, got %d", len(msgs))
	}
	if msgs[0].text != "err" {
		t.Errorf("expected errMsg, got %q", msgs[0].text)
	}
}

func TestDeliverTurnResult_HeartbeatOK_NothingSent(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	// HEARTBEAT_OK: text and errMsg are both empty, nothing should be sent.
	deliverTurnResult(bot, logger, turnResult{heartbeatOK: true, chatID: 99})

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for heartbeat-ok, got %d", len(bot.sent()))
	}
}

func TestDeliverTurnResult_ZeroValue_NothingSent(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	deliverTurnResult(bot, logger, turnResult{})

	if len(bot.sent()) != 0 {
		t.Errorf("expected no sends for zero-value, got %d", len(bot.sent()))
	}
}

func TestDeliverTurnResult_TextWithAttachments(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	deliverTurnResult(bot, logger, turnResult{
		chatID:      10,
		text:        "Here's a file",
		attachments: []media.Attachment{{Type: media.TypeImage, Path: "/nonexistent/test.jpg"}},
	})

	// Should send text + attempt attachment (which will fail gracefully
	// because the file doesn't exist, sending an error message instead).
	msgs := bot.sent()
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 send, got %d", len(msgs))
	}
	if msgs[0].text != "Here's a file" {
		t.Errorf("first message should be text, got %q", msgs[0].text)
	}
}

func TestLockedRunTurn_PanicReleasesLock(t *testing.T) {
	var mu sync.Mutex

	s := &Scheduler{
		turnMu: &mu,
		runTurn: func(chatID int64, userText, stream string) turnResult {
			panic("test panic")
		},
	}

	mu.Lock() // simulate TryLock success

	// lockedRunTurn should release the lock even though runTurn panics.
	func() {
		defer func() { recover() }()
		s.lockedRunTurn(0, "", "")
	}()

	// The lock should be free now — TryLock should succeed.
	if !mu.TryLock() {
		t.Fatal("mutex still locked after panic in lockedRunTurn")
	}
	mu.Unlock()
}

// --- sendAttachment tests ---

func TestSendAttachment_ContainerPath(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	// Container path should be rejected.
	sendAttachment(bot, logger, 123, media.Attachment{
		Type: media.TypeImage,
		Path: "/nullclaw-data/workspace/test.jpg",
	}, "caption")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(msgs))
	}
	if !containsStr(msgs[0].text, "could not be retrieved from container") {
		t.Errorf("expected container retrieval error, got %q", msgs[0].text)
	}
}

func TestSendAttachment_RelativePath(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	// Relative path should be rejected.
	sendAttachment(bot, logger, 123, media.Attachment{
		Type: media.TypeImage,
		Path: "../etc/passwd",
	}, "")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(msgs))
	}
	if !containsStr(msgs[0].text, "invalid path") {
		t.Errorf("expected invalid path error, got %q", msgs[0].text)
	}
}

func TestSendAttachment_NonexistentFile(t *testing.T) {
	bot := &mockSender{}
	logger := testLogger(t)

	sendAttachment(bot, logger, 123, media.Attachment{
		Type: media.TypeImage,
		Path: "/tmp/nonexistent_file_12345.jpg",
	}, "")

	msgs := bot.sent()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(msgs))
	}
	if !containsStr(msgs[0].text, "not found") {
		t.Errorf("expected not found error, got %q", msgs[0].text)
	}
}

// --- checkBudget tests (H24) ---

func TestCheckBudget_WithinLimits(t *testing.T) {
	store := testStore(t)
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 1.00})

	costs := &config.CostsConfig{
		Track:            true,
		DailyBudgetUSD:   5.0,
		MonthlyBudgetUSD: 50.0,
	}
	if err := checkBudget(store, costs); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckBudget_DailyExceeded(t *testing.T) {
	store := testStore(t)
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 6.00})

	costs := &config.CostsConfig{
		Track:          true,
		DailyBudgetUSD: 5.0,
	}
	err := checkBudget(store, costs)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if !containsStr(err.Error(), "daily budget exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckBudget_MonthlyExceeded(t *testing.T) {
	store := testStore(t)
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 55.00})

	costs := &config.CostsConfig{
		Track:            true,
		MonthlyBudgetUSD: 50.0,
	}
	err := checkBudget(store, costs)
	if err == nil {
		t.Fatal("expected budget exceeded error")
	}
	if !containsStr(err.Error(), "monthly budget exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheckBudget_TrackingDisabled(t *testing.T) {
	store := testStore(t)
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 999.00})

	costs := &config.CostsConfig{
		Track:          false,
		DailyBudgetUSD: 1.0,
	}
	if err := checkBudget(store, costs); err != nil {
		t.Errorf("expected nil when tracking disabled, got %v", err)
	}
}

func TestCheckBudget_ZeroBudget(t *testing.T) {
	store := testStore(t)
	store.AddCost(memory.Cost{Category: "loop", CostUSD: 100.00})

	// Zero budget = no enforcement.
	costs := &config.CostsConfig{Track: true}
	if err := checkBudget(store, costs); err != nil {
		t.Errorf("expected nil for zero budget, got %v", err)
	}
}

func testStore(t *testing.T) *memory.Store {
	t.Helper()
	s, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestEnsureAgentImage_AlreadyExists(t *testing.T) {
	mock := &docker.MockOps{
		ImageExistsFn: func(_ context.Context, img string) (bool, error) {
			return true, nil
		},
	}
	err := ensureAgentImage(context.Background(), mock, "nullclaw:latest", testLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureAgentImage_PullAndTag(t *testing.T) {
	var pulledRef, taggedSource, taggedTarget string
	mock := &docker.MockOps{
		ImageExistsFn: func(_ context.Context, img string) (bool, error) {
			return false, nil // image missing
		},
		ImagePullFn: func(_ context.Context, ref string) error {
			pulledRef = ref
			return nil
		},
		ImageTagFn: func(_ context.Context, source, target string) error {
			taggedSource = source
			taggedTarget = target
			return nil
		},
	}
	err := ensureAgentImage(context.Background(), mock, "nullclaw:latest", testLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulledRef != "ghcr.io/nullclaw/nullclaw:latest" {
		t.Errorf("pulled = %q, want registry ref", pulledRef)
	}
	// nullclaw:latest != ghcr.io/nullclaw/nullclaw:latest, so it should tag.
	if taggedSource != "ghcr.io/nullclaw/nullclaw:latest" {
		t.Errorf("tag source = %q, want registry ref", taggedSource)
	}
	if taggedTarget != "nullclaw:latest" {
		t.Errorf("tag target = %q, want nullclaw:latest", taggedTarget)
	}
}

func TestEnsureAgentImage_CustomImage(t *testing.T) {
	var taggedTarget string
	mock := &docker.MockOps{
		ImageExistsFn: func(_ context.Context, img string) (bool, error) {
			return false, nil
		},
		ImagePullFn: func(_ context.Context, ref string) error {
			return nil
		},
		ImageTagFn: func(_ context.Context, source, target string) error {
			taggedTarget = target
			return nil
		},
	}
	// Custom image via XNC_IMAGE — should pull registry and tag as custom name.
	err := ensureAgentImage(context.Background(), mock, "my-nullclaw:v2", testLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taggedTarget != "my-nullclaw:v2" {
		t.Errorf("tag target = %q, want my-nullclaw:v2", taggedTarget)
	}
}

func TestEnsureAgentImage_PullFails(t *testing.T) {
	mock := &docker.MockOps{
		ImageExistsFn: func(_ context.Context, img string) (bool, error) {
			return false, nil
		},
		ImagePullFn: func(_ context.Context, ref string) error {
			return fmt.Errorf("network error")
		},
	}
	err := ensureAgentImage(context.Background(), mock, "nullclaw:latest", testLogger(t))
	if err == nil {
		t.Fatal("expected error when pull fails")
	}
}

func TestEnsureAgentImage_RegistryRefSkipsTag(t *testing.T) {
	var tagged bool
	mock := &docker.MockOps{
		ImageExistsFn: func(_ context.Context, img string) (bool, error) {
			return false, nil
		},
		ImagePullFn: func(_ context.Context, ref string) error {
			return nil
		},
		ImageTagFn: func(_ context.Context, source, target string) error {
			tagged = true
			return nil
		},
	}
	// When image IS the registry ref, no tag needed.
	err := ensureAgentImage(context.Background(), mock, "ghcr.io/nullclaw/nullclaw:latest", testLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tagged {
		t.Error("should not tag when image name matches registry ref")
	}
}
