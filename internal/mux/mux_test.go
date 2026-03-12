package mux

import (
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/memory"
)

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
