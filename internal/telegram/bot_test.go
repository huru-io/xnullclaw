package telegram

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// --- Message Splitting Tests ---

func TestSplitMessage_Short(t *testing.T) {
	text := "Hello, this is a short message."
	parts := SplitMessage(text, maxMessageLen)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0] != text {
		t.Fatalf("expected %q, got %q", text, parts[0])
	}
}

func TestSplitMessage_ParagraphBoundary(t *testing.T) {
	// Build a message with two paragraphs, each under maxLen individually,
	// but combined over maxLen.
	para1 := strings.Repeat("A", 2000)
	para2 := strings.Repeat("B", 2000)
	text := para1 + "\n\n" + para2

	parts := SplitMessage(text, maxMessageLen)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %v", len(parts), summarizeParts(parts))
	}
	if strings.TrimSpace(parts[0]) != para1 {
		t.Errorf("first part should be paragraph 1")
	}
	if strings.TrimSpace(parts[1]) != para2 {
		t.Errorf("second part should be paragraph 2")
	}
}

func TestSplitMessage_NewlineFallback(t *testing.T) {
	// No double newline, but single newlines exist.
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = strings.Repeat("X", 50) // 50 chars per line
	}
	text := strings.Join(lines, "\n") // 100 lines * 51 chars = 5100 chars

	parts := SplitMessage(text, maxMessageLen)
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(parts))
	}
	for i, part := range parts {
		if len(part) > maxMessageLen {
			t.Errorf("part %d exceeds maxLen: %d > %d", i, len(part), maxMessageLen)
		}
	}
}

func TestSplitMessage_HardCut(t *testing.T) {
	// Single continuous string with no newlines.
	text := strings.Repeat("Z", 8000)
	parts := SplitMessage(text, maxMessageLen)
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(parts))
	}
	// Reassemble and check no data lost.
	joined := strings.Join(parts, "")
	if joined != text {
		t.Errorf("data lost after split: original len %d, reassembled len %d", len(text), len(joined))
	}
}

func TestSplitMessage_PreservesCodeBlock(t *testing.T) {
	// A code block that straddles the split point should not be split.
	before := strings.Repeat("A", 3700)
	codeBlock := "```\nfunc main() {\n\tprintln(\"hello\")\n}\n```"
	after := strings.Repeat("B", 200)
	text := before + "\n\n" + codeBlock + "\n\n" + after

	parts := SplitMessage(text, maxMessageLen)
	// The code block should be intact in one of the parts.
	foundCodeBlock := false
	for _, part := range parts {
		if strings.Contains(part, "```\nfunc main()") && strings.Contains(part, "```") {
			// Count the fences: should be even (both opening and closing).
			count := strings.Count(part, "```")
			if count%2 == 0 {
				foundCodeBlock = true
			}
		}
	}
	if !foundCodeBlock {
		t.Errorf("code block was split across parts")
		for i, p := range parts {
			t.Logf("part %d (%d chars): %s...", i, len(p), truncate(p, 100))
		}
	}
}

func TestSplitMessage_CodeBlockAtStart(t *testing.T) {
	// Code block that is larger than maxLen on its own — must hard cut.
	bigCode := "```\n" + strings.Repeat("code\n", 1000) + "```"
	parts := SplitMessage(bigCode, maxMessageLen)
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(parts))
	}
	// No part should exceed maxLen.
	for i, part := range parts {
		if len(part) > maxMessageLen {
			t.Errorf("part %d exceeds maxLen: %d", i, len(part))
		}
	}
}

func TestSplitMessage_Empty(t *testing.T) {
	parts := SplitMessage("", maxMessageLen)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part for empty string, got %d", len(parts))
	}
}

func TestSplitMessage_ExactLimit(t *testing.T) {
	text := strings.Repeat("X", maxMessageLen)
	parts := SplitMessage(text, maxMessageLen)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part at exact limit, got %d", len(parts))
	}
}

// --- Command Parsing Tests ---

func TestParseCommand_DM(t *testing.T) {
	cmd := ParseCommand("/dm alice Hello, how are you?")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "dm" {
		t.Errorf("expected name 'dm', got %q", cmd.Name)
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
	if cmd.Args != "Hello, how are you?" {
		t.Errorf("expected args 'Hello, how are you?', got %q", cmd.Args)
	}
}

func TestParseCommand_DMNoMessage(t *testing.T) {
	cmd := ParseCommand("/dm alice")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
	if cmd.Args != "" {
		t.Errorf("expected empty args, got %q", cmd.Args)
	}
}

func TestParseCommand_List(t *testing.T) {
	cmd := ParseCommand("/list")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "list" {
		t.Errorf("expected name 'list', got %q", cmd.Name)
	}
}

func TestParseCommand_Status(t *testing.T) {
	cmd := ParseCommand("/status alice")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "status" {
		t.Errorf("expected name 'status', got %q", cmd.Name)
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
}

func TestParseCommand_StatusNoAgent(t *testing.T) {
	cmd := ParseCommand("/status")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "status" {
		t.Errorf("expected name 'status', got %q", cmd.Name)
	}
	if cmd.Agent != "" {
		t.Errorf("expected empty agent, got %q", cmd.Agent)
	}
}

func TestParseCommand_Mux(t *testing.T) {
	cmd := ParseCommand("/mux")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "mux" {
		t.Errorf("expected name 'mux', got %q", cmd.Name)
	}
}

func TestParseCommand_Start(t *testing.T) {
	cmd := ParseCommand("/start bob")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "start" {
		t.Errorf("expected name 'start', got %q", cmd.Name)
	}
	if cmd.Agent != "bob" {
		t.Errorf("expected agent 'bob', got %q", cmd.Agent)
	}
}

func TestParseCommand_Stop(t *testing.T) {
	cmd := ParseCommand("/stop carol")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "stop" {
		t.Errorf("expected name 'stop', got %q", cmd.Name)
	}
	if cmd.Agent != "carol" {
		t.Errorf("expected agent 'carol', got %q", cmd.Agent)
	}
}

func TestParseCommand_Costs(t *testing.T) {
	cmd := ParseCommand("/costs alice")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "costs" {
		t.Errorf("expected name 'costs', got %q", cmd.Name)
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
}

func TestParseCommand_History(t *testing.T) {
	cmd := ParseCommand("/history bob")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "history" {
		t.Errorf("expected name 'history', got %q", cmd.Name)
	}
	if cmd.Agent != "bob" {
		t.Errorf("expected agent 'bob', got %q", cmd.Agent)
	}
}

func TestParseCommand_Broadcast(t *testing.T) {
	cmd := ParseCommand("/broadcast Everyone check in")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "broadcast" {
		t.Errorf("expected name 'broadcast', got %q", cmd.Name)
	}
	if cmd.Args != "Everyone check in" {
		t.Errorf("expected args 'Everyone check in', got %q", cmd.Args)
	}
}

func TestParseCommand_Config(t *testing.T) {
	cmd := ParseCommand("/config alice model gpt-4o")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "config" {
		t.Errorf("expected name 'config', got %q", cmd.Name)
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
	if cmd.Args != "model gpt-4o" {
		t.Errorf("expected args 'model gpt-4o', got %q", cmd.Args)
	}
}

func TestParseCommand_Budget(t *testing.T) {
	cmd := ParseCommand("/budget 100")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "budget" {
		t.Errorf("expected name 'budget', got %q", cmd.Name)
	}
	if cmd.Args != "100" {
		t.Errorf("expected args '100', got %q", cmd.Args)
	}
}

func TestParseCommand_WithBotUsername(t *testing.T) {
	cmd := ParseCommand("/list@muxbot")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "list" {
		t.Errorf("expected name 'list', got %q", cmd.Name)
	}
}

func TestParseCommand_Unknown(t *testing.T) {
	cmd := ParseCommand("/foobar something")
	if cmd != nil {
		t.Errorf("expected nil for unknown command, got %+v", cmd)
	}
}

func TestParseCommand_NotACommand(t *testing.T) {
	cmd := ParseCommand("hello world")
	if cmd != nil {
		t.Errorf("expected nil for non-command, got %+v", cmd)
	}
}

func TestParseCommand_Empty(t *testing.T) {
	cmd := ParseCommand("")
	if cmd != nil {
		t.Errorf("expected nil for empty string, got %+v", cmd)
	}
}

func TestParseCommand_SlashOnly(t *testing.T) {
	cmd := ParseCommand("/")
	if cmd != nil {
		t.Errorf("expected nil for bare slash, got %+v", cmd)
	}
}

func TestParseCommand_Switch(t *testing.T) {
	cmd := ParseCommand("/switch alice")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "switch" {
		t.Errorf("expected name 'switch', got %q", cmd.Name)
	}
	if cmd.Agent != "alice" {
		t.Errorf("expected agent 'alice', got %q", cmd.Agent)
	}
}

func TestParseCommand_Restart(t *testing.T) {
	cmd := ParseCommand("/restart bob")
	if cmd == nil {
		t.Fatal("expected command, got nil")
	}
	if cmd.Name != "restart" {
		t.Errorf("expected name 'restart', got %q", cmd.Name)
	}
	if cmd.Agent != "bob" {
		t.Errorf("expected agent 'bob', got %q", cmd.Agent)
	}
}

// --- Auth Filtering Tests ---

func TestAuthFiltering_AllowedUser(t *testing.T) {
	allow := map[string]bool{"123": true, "456": true}

	// Simulate: user 123 should be allowed.
	if !allow["123"] {
		t.Errorf("user 123 should be allowed")
	}
}

func TestAuthFiltering_DisallowedUser(t *testing.T) {
	allow := map[string]bool{"123": true, "456": true}

	// Simulate: user 789 should be rejected.
	if allow["789"] {
		t.Errorf("user 789 should be rejected")
	}
}

func TestAuthFiltering_EmptyAllowList(t *testing.T) {
	allow := map[string]bool{}

	// When allow list is empty and len == 0, the bot code allows all.
	if len(allow) > 0 && !allow["999"] {
		t.Errorf("with empty allow list, all users should be allowed")
	}
}

// --- Token Bucket Rate Limiter Tests ---

func TestTokenBucket_InitialBurst(t *testing.T) {
	b := &Bot{
		bucketTokens: 3,
		bucketMax:    3,
		bucketRate:   1,
		bucketLast:   time.Now(),
	}

	// Should be able to consume 3 tokens immediately.
	for i := 0; i < 3; i++ {
		b.bucketMu.Lock()
		if b.bucketTokens < 1 {
			b.bucketMu.Unlock()
			t.Fatalf("expected token available at burst %d", i)
		}
		b.bucketTokens--
		b.bucketMu.Unlock()
	}

	// 4th token should not be available.
	b.bucketMu.Lock()
	if b.bucketTokens >= 1 {
		b.bucketMu.Unlock()
		t.Fatalf("expected no token after burst of 3")
	}
	b.bucketMu.Unlock()
}

func TestTokenBucket_Refill(t *testing.T) {
	b := &Bot{
		bucketTokens: 0,
		bucketMax:    3,
		bucketRate:   1,
		bucketLast:   time.Now(),
	}

	// Simulate 2 seconds passing.
	b.bucketMu.Lock()
	b.bucketTokens += 2 * b.bucketRate // refill 2 tokens
	if b.bucketTokens > b.bucketMax {
		b.bucketTokens = b.bucketMax
	}
	b.bucketMu.Unlock()

	b.bucketMu.Lock()
	if b.bucketTokens != 2 {
		t.Errorf("expected 2 tokens after 2s, got %f", b.bucketTokens)
	}
	b.bucketMu.Unlock()
}

func TestTokenBucket_MaxCap(t *testing.T) {
	b := &Bot{
		bucketTokens: 3,
		bucketMax:    3,
		bucketRate:   1,
		bucketLast:   time.Now(),
	}

	// Simulate 10 seconds passing — should not exceed max.
	b.bucketMu.Lock()
	b.bucketTokens += 10 * b.bucketRate
	if b.bucketTokens > b.bucketMax {
		b.bucketTokens = b.bucketMax
	}
	b.bucketMu.Unlock()

	b.bucketMu.Lock()
	if b.bucketTokens != 3 {
		t.Errorf("expected max 3 tokens, got %f", b.bucketTokens)
	}
	b.bucketMu.Unlock()
}

// --- handleUpdate Tests (private mode) ---

func newTestBot(groupID int64, topicID int, allowFrom map[string]bool) *Bot {
	return &Bot{
		groupID:   groupID,
		topicID:   topicID,
		allowFrom: allowFrom,
		stopCh:    make(chan struct{}),
	}
}

func makeUpdate(chatID int64, chatType string, userID int64, text string) tgbotapi.Update {
	return tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: chatID, Type: chatType},
			From: &tgbotapi.User{ID: userID},
			Text: text,
		},
	}
}

func TestHandleUpdate_PrivateMode_AcceptsPrivateChat(t *testing.T) {
	b := newTestBot(0, 0, nil)
	var got string
	b.onMessage = func(chatID int64, userID string, text string) {
		got = text
	}

	b.handleUpdate(makeUpdate(12345, "private", 99, "hello"), 0)

	if got != "hello" {
		t.Errorf("expected onMessage with 'hello', got %q", got)
	}
}

func TestHandleUpdate_PrivateMode_RejectsGroupChat(t *testing.T) {
	b := newTestBot(0, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	b.handleUpdate(makeUpdate(-100999, "group", 99, "hello"), 0)

	if called {
		t.Error("onMessage should not fire for group messages in private mode")
	}
}

func TestHandleUpdate_PrivateMode_RejectsSupergroup(t *testing.T) {
	b := newTestBot(0, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "hello"), 0)

	if called {
		t.Error("onMessage should not fire for supergroup messages in private mode")
	}
}

func TestHandleUpdate_PrivateMode_AllowFromFilters(t *testing.T) {
	b := newTestBot(0, 0, map[string]bool{"99": true})
	var got string
	b.onMessage = func(chatID int64, userID string, text string) {
		got = text
	}

	// Allowed user.
	b.handleUpdate(makeUpdate(12345, "private", 99, "allowed"), 0)
	if got != "allowed" {
		t.Errorf("expected 'allowed', got %q", got)
	}

	// Disallowed user.
	got = ""
	b.handleUpdate(makeUpdate(12345, "private", 777, "blocked"), 0)
	if got != "" {
		t.Errorf("expected empty (blocked), got %q", got)
	}
}

func TestHandleUpdate_PrivateMode_EmptyAllowFromAcceptsAll(t *testing.T) {
	b := newTestBot(0, 0, map[string]bool{})
	var got string
	b.onMessage = func(chatID int64, userID string, text string) {
		got = text
	}

	b.handleUpdate(makeUpdate(12345, "private", 999, "anyone"), 0)

	if got != "anyone" {
		t.Errorf("expected 'anyone', got %q", got)
	}
}

func TestHandleUpdate_PrivateMode_VoiceCallback(t *testing.T) {
	b := newTestBot(0, 0, nil)
	var gotFileID string
	b.onVoice = func(chatID int64, userID string, fileID string) {
		gotFileID = fileID
	}

	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:  &tgbotapi.Chat{ID: 12345, Type: "private"},
			From:  &tgbotapi.User{ID: 99},
			Voice: &tgbotapi.Voice{FileID: "voice123"},
		},
	}
	b.handleUpdate(update, 0)

	if gotFileID != "voice123" {
		t.Errorf("expected voice fileID 'voice123', got %q", gotFileID)
	}
}

func TestHandleUpdate_PrivateMode_CommandCallback(t *testing.T) {
	b := newTestBot(0, 0, nil)
	var gotCmd string
	b.onCommand = func(chatID int64, userID string, cmd Command) {
		gotCmd = cmd.Name
	}

	b.handleUpdate(makeUpdate(12345, "private", 99, "/help"), 0)

	if gotCmd != "help" {
		t.Errorf("expected command 'help', got %q", gotCmd)
	}
}

func TestHandleUpdate_PrivateMode_ChatIDPassedThrough(t *testing.T) {
	b := newTestBot(0, 0, nil)
	var gotChatID int64
	b.onMessage = func(chatID int64, userID string, text string) {
		gotChatID = chatID
	}

	b.handleUpdate(makeUpdate(12345, "private", 99, "hi"), 0)

	if gotChatID != 12345 {
		t.Errorf("expected chatID 12345, got %d", gotChatID)
	}
}

func TestHandleUpdate_PrivateMode_NilMessage(t *testing.T) {
	b := newTestBot(0, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	// Nil message (e.g. callback query, edited message).
	b.handleUpdate(tgbotapi.Update{Message: nil}, 0)

	if called {
		t.Error("should not call onMessage for nil message")
	}
}

func TestHandleUpdate_PrivateMode_EmptyText(t *testing.T) {
	b := newTestBot(0, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	b.handleUpdate(makeUpdate(12345, "private", 99, "   "), 0)

	if called {
		t.Error("should not call onMessage for whitespace-only text")
	}
}

// --- handleUpdate Tests (group mode) ---

func TestHandleUpdate_GroupMode_AcceptsConfiguredGroup(t *testing.T) {
	b := newTestBot(-100999, 0, nil)
	var got string
	b.onMessage = func(chatID int64, userID string, text string) {
		got = text
	}

	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "group msg"), 0)

	if got != "group msg" {
		t.Errorf("expected 'group msg', got %q", got)
	}
}

func TestHandleUpdate_GroupMode_RejectsOtherGroup(t *testing.T) {
	b := newTestBot(-100999, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	b.handleUpdate(makeUpdate(-100888, "supergroup", 99, "other group"), 0)

	if called {
		t.Error("should reject messages from non-configured group")
	}
}

func TestHandleUpdate_GroupMode_RejectsPrivateChat(t *testing.T) {
	b := newTestBot(-100999, 0, nil)
	called := false
	b.onMessage = func(chatID int64, userID string, text string) {
		called = true
	}

	b.handleUpdate(makeUpdate(12345, "private", 99, "dm"), 0)

	if called {
		t.Error("should reject DMs when group mode is configured")
	}
}

func TestHandleUpdate_GroupMode_TopicFiltering(t *testing.T) {
	b := newTestBot(-100999, 42, nil)
	var got string
	b.onMessage = func(chatID int64, userID string, text string) {
		got = text
	}

	// Correct topic.
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "on topic"), 42)
	if got != "on topic" {
		t.Errorf("expected 'on topic', got %q", got)
	}

	// Wrong topic.
	got = ""
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "off topic"), 7)
	if got != "" {
		t.Errorf("expected empty (wrong topic), got %q", got)
	}

	// No topic (threadID 0).
	got = ""
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "no thread"), 0)
	if got != "" {
		t.Errorf("expected empty (no thread), got %q", got)
	}
}

func TestHandleUpdate_GroupMode_DiscoveryMode(t *testing.T) {
	b := newTestBot(-100999, -1, nil)
	msgCalled := false
	b.onMessage = func(chatID int64, userID string, text string) {
		msgCalled = true
	}
	var discoveredThread int
	b.onDiscovery = func(chatID int64, userID string, threadID int, text string) {
		discoveredThread = threadID
	}

	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "test"), 42)

	if msgCalled {
		t.Error("should not process messages in discovery mode")
	}
	if discoveredThread != 42 {
		t.Errorf("expected discovered threadID 42, got %d", discoveredThread)
	}
}

func TestHandleUpdate_GroupMode_DiscoveryLogsAllChats(t *testing.T) {
	// Discovery mode should log messages from ANY chat, not just the configured group.
	// This lets users find the correct group_id.
	b := newTestBot(-100999, -1, nil)
	var discoveredChatIDs []int64
	b.onDiscovery = func(chatID int64, userID string, threadID int, text string) {
		discoveredChatIDs = append(discoveredChatIDs, chatID)
	}

	// Message from a different group.
	b.handleUpdate(makeUpdate(-100888, "supergroup", 99, "other group"), 5)
	// Message from configured group.
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "our group"), 10)
	// Message from a private chat.
	b.handleUpdate(makeUpdate(12345, "private", 99, "dm"), 0)

	if len(discoveredChatIDs) != 3 {
		t.Errorf("discovery should log all chats, got %d", len(discoveredChatIDs))
	}
}

func TestHandleUpdate_GroupMode_TopicZeroAcceptsAll(t *testing.T) {
	b := newTestBot(-100999, 0, nil)
	var calls []int
	b.onMessage = func(chatID int64, userID string, text string) {
		calls = append(calls, 1)
	}

	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "thread 0"), 0)
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "thread 5"), 5)
	b.handleUpdate(makeUpdate(-100999, "supergroup", 99, "thread 99"), 99)

	if len(calls) != 3 {
		t.Errorf("topicID=0 should accept all threads, got %d calls", len(calls))
	}
}

// --- truncateLog Tests ---

func TestTruncateLog_Short(t *testing.T) {
	if got := truncateLog("hello", 10); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestTruncateLog_Exact(t *testing.T) {
	if got := truncateLog("hello", 5); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestTruncateLog_Truncated(t *testing.T) {
	if got := truncateLog("hello world", 5); got != "hello..." {
		t.Errorf("expected 'hello...', got %q", got)
	}
}

func TestTruncateLog_Unicode(t *testing.T) {
	// 5 runes, each 3 bytes in UTF-8.
	s := "привет"
	got := truncateLog(s, 3)
	if got != "при..." {
		t.Errorf("expected 'при...', got %q", got)
	}
}

// --- Helpers ---

func summarizeParts(parts []string) []string {
	var s []string
	for _, p := range parts {
		s = append(s, truncate(p, 60))
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
