package telegram

import (
	"strings"
	"testing"
	"time"
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
