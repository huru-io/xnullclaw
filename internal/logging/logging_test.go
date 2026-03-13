package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
)

// helper creates a Logger pointing at a temp directory and returns it along
// with the temp dir path. The caller should defer l.Close() and os.RemoveAll.
func newTestLogger(t *testing.T, level string) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.LoggingConfig{
		Level: level,
		Dir:   "logs",
	}
	l, err := New(cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pin time for deterministic output.
	l.nowFunc = func() time.Time {
		return time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC)
	}
	return l, dir
}

// readLog reads the full contents of a log file.
func readLog(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "logs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// parseEntries parses JSONL content into Entry slices.
func parseEntries(t *testing.T, data string) []Entry {
	t.Helper()
	var entries []Entry
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal entry %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestNewCreatesFiles(t *testing.T) {
	l, dir := newTestLogger(t, "info")
	defer l.Close()

	for _, name := range []string{"mux.log", "agents.log", "costs.log"} {
		path := filepath.Join(dir, "logs", name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}

func TestBasicLogWritesToMuxLog(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.Info("hello world", "key", "value")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Level != "info" {
		t.Errorf("level = %q, want %q", e.Level, "info")
	}
	if e.Message != "hello world" {
		t.Errorf("msg = %q, want %q", e.Message, "hello world")
	}
	if e.Timestamp != "2026-03-09T14:00:00Z" {
		t.Errorf("ts = %q, want %q", e.Timestamp, "2026-03-09T14:00:00Z")
	}
	if e.Fields["key"] != "value" {
		t.Errorf("fields[key] = %v, want %q", e.Fields["key"], "value")
	}
}

func TestLevelFiltering(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.Debug("should be filtered")
	l.Info("should appear")
	l.Warn("should also appear")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %s", len(entries), data)
	}
	if entries[0].Level != "info" {
		t.Errorf("first entry level = %q, want info", entries[0].Level)
	}
	if entries[1].Level != "warn" {
		t.Errorf("second entry level = %q, want warn", entries[1].Level)
	}
}

func TestDebugLevelPassesAll(t *testing.T) {
	l, dir := newTestLogger(t, "debug")

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
}

func TestErrorLevelFiltersLower(t *testing.T) {
	l, dir := newTestLogger(t, "error")

	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Level != "error" {
		t.Errorf("level = %q, want error", entries[0].Level)
	}
}

func TestOutputIsValidJSON(t *testing.T) {
	l, dir := newTestLogger(t, "debug")

	l.Info("msg1", "a", 1, "b", true)
	l.Warn("msg2", "nested", map[string]any{"x": 42})
	l.Debug("msg3") // no fields
	l.Close()

	data := readLog(t, dir, "mux.log")
	for i, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not valid JSON: %s", i, line)
		}
	}
}

func TestLogIncoming(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogIncoming("user123", "hello bot", "text")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "incoming message" {
		t.Errorf("msg = %q", e.Message)
	}
	if e.Fields["user_id"] != "user123" {
		t.Errorf("user_id = %v", e.Fields["user_id"])
	}
	if e.Fields["type"] != "text" {
		t.Errorf("type = %v", e.Fields["type"])
	}
	// text_len should be the length of "hello bot" = 9
	if v, ok := e.Fields["text_len"].(float64); !ok || int(v) != 9 {
		t.Errorf("text_len = %v", e.Fields["text_len"])
	}
}

func TestLogRouting(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogRouting("alice", "keyword match", 0.85)
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Fields["agent"] != "alice" {
		t.Errorf("agent = %v", e.Fields["agent"])
	}
	if e.Fields["reason"] != "keyword match" {
		t.Errorf("reason = %v", e.Fields["reason"])
	}
	if v, ok := e.Fields["confidence"].(float64); !ok || v != 0.85 {
		t.Errorf("confidence = %v", e.Fields["confidence"])
	}
}

func TestLogToolCall(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogToolCall("web_search", map[string]any{"q": "weather"}, "sunny", 150*time.Millisecond, nil)
	l.Close()

	data := readLog(t, dir, "agents.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "tool call" {
		t.Errorf("msg = %q", e.Message)
	}
	if e.Fields["tool"] != "web_search" {
		t.Errorf("tool = %v", e.Fields["tool"])
	}
	if v, ok := e.Fields["duration_ms"].(float64); !ok || int64(v) != 150 {
		t.Errorf("duration_ms = %v", e.Fields["duration_ms"])
	}
	// no error field expected
	if _, ok := e.Fields["error"]; ok {
		t.Errorf("unexpected error field")
	}
}

func TestLogToolCallWithError(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogToolCall("fetch", nil, "", 50*time.Millisecond, os.ErrPermission)
	l.Close()

	data := readLog(t, dir, "agents.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if _, ok := entries[0].Fields["error"]; !ok {
		t.Errorf("expected error field to be present")
	}
}

func TestLogModelCall(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogModelCall("gpt-4o", 500, 200, 0.0045, 3200)
	l.Close()

	data := readLog(t, dir, "agents.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Fields["model"] != "gpt-4o" {
		t.Errorf("model = %v", e.Fields["model"])
	}
	if v, ok := e.Fields["input_tokens"].(float64); !ok || int(v) != 500 {
		t.Errorf("input_tokens = %v", e.Fields["input_tokens"])
	}
}

func TestLogAgentSendAndResponse(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogAgentSend("alice", 42)
	l.LogAgentResponse("alice", 128, 1500)
	l.Close()

	data := readLog(t, dir, "agents.log")
	entries := parseEntries(t, data)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Message != "agent send" {
		t.Errorf("msg[0] = %q", entries[0].Message)
	}
	if entries[1].Message != "agent response" {
		t.Errorf("msg[1] = %q", entries[1].Message)
	}
	if v, ok := entries[1].Fields["latency_ms"].(float64); !ok || int64(v) != 1500 {
		t.Errorf("latency_ms = %v", entries[1].Fields["latency_ms"])
	}
}

func TestLogLifecycle(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogLifecycle("started", "alice", "pid 12345")
	l.Close()

	data := readLog(t, dir, "agents.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Fields["event"] != "started" {
		t.Errorf("event = %v", e.Fields["event"])
	}
	if e.Fields["agent"] != "alice" {
		t.Errorf("agent = %v", e.Fields["agent"])
	}
}

func TestLogCost(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogCost("completion", "gpt-4o", "alice", 1000, 500, 0.012)
	l.Close()

	// Cost entries go to costs.log, not mux.log
	data := readLog(t, dir, "costs.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "cost" {
		t.Errorf("msg = %q", e.Message)
	}
	if e.Fields["category"] != "completion" {
		t.Errorf("category = %v", e.Fields["category"])
	}

	// mux.log should be empty
	muxData := readLog(t, dir, "mux.log")
	if strings.TrimSpace(muxData) != "" {
		t.Errorf("expected mux.log to be empty for cost event, got: %s", muxData)
	}
}

func TestLogCompaction(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogCompaction(50, 12, 340)
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Message != "compaction" {
		t.Errorf("msg = %q", e.Message)
	}
	if v, ok := e.Fields["messages_compacted"].(float64); !ok || int(v) != 50 {
		t.Errorf("messages_compacted = %v", e.Fields["messages_compacted"])
	}
}

func TestLogPersonaChange(t *testing.T) {
	l, dir := newTestLogger(t, "info")

	l.LogPersonaChange("warmth", "0.6", "0.8")
	l.Close()

	data := readLog(t, dir, "mux.log")
	entries := parseEntries(t, data)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Fields["field"] != "warmth" {
		t.Errorf("field = %v", e.Fields["field"])
	}
	if e.Fields["old_value"] != "0.6" {
		t.Errorf("old_value = %v", e.Fields["old_value"])
	}
	if e.Fields["new_value"] != "0.8" {
		t.Errorf("new_value = %v", e.Fields["new_value"])
	}
}

func TestParseLevelDefaults(t *testing.T) {
	tests := []struct {
		input string
		want  Level
	}{
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"warn", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"bogus", LevelInfo},
		{"", LevelInfo},
	}
	for _, tt := range tests {
		got := ParseLevel(tt.input)
		if got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFieldsToMapOddLength(t *testing.T) {
	m := fieldsToMap([]any{"key1", "val1", "orphan"})
	if m["key1"] != "val1" {
		t.Errorf("key1 = %v", m["key1"])
	}
	if m["orphan"] != nil {
		t.Errorf("orphan = %v, want nil", m["orphan"])
	}
}

func TestFieldsToMapEmpty(t *testing.T) {
	m := fieldsToMap(nil)
	if m != nil {
		t.Errorf("expected nil, got %v", m)
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.LoggingConfig{
		Level:         "info",
		Dir:           "logs",
		MaxFileSizeMB: 0, // will override maxFileSize below
	}
	l, err := New(cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Set a tiny maxFileSize to trigger rotation quickly.
	l.maxFileSize = 200 // bytes
	l.nowFunc = func() time.Time {
		return time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC)
	}

	// Write enough entries to trigger rotation.
	for i := 0; i < 20; i++ {
		l.Info("rotation test entry", "i", i)
	}
	l.Close()

	logDir := filepath.Join(dir, "logs")

	// The current mux.log should exist and be small (post-rotation).
	data, err := os.ReadFile(filepath.Join(logDir, "mux.log"))
	if err != nil {
		t.Fatalf("read mux.log: %v", err)
	}
	if len(data) == 0 {
		t.Error("mux.log should not be empty after writes")
	}

	// At least one rotated file should exist.
	_, err = os.Stat(filepath.Join(logDir, "mux.log.1"))
	if err != nil {
		t.Error("expected mux.log.1 to exist after rotation")
	}
}

func TestLogRotation_MaxFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.LoggingConfig{Level: "info", Dir: "logs"}
	l, err := New(cfg, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.maxFileSize = 100 // tiny to force many rotations
	l.nowFunc = func() time.Time {
		return time.Date(2026, 3, 9, 14, 0, 0, 0, time.UTC)
	}

	// Write many entries to force multiple rotations.
	for i := 0; i < 100; i++ {
		l.Info("filling up logs", "i", i)
	}
	l.Close()

	logDir := filepath.Join(dir, "logs")

	// .1, .2, .3 should exist, but .4 should not (max 3 rotated files).
	for i := 1; i <= maxRotatedFiles; i++ {
		path := filepath.Join(logDir, fmt.Sprintf("mux.log.%d", i))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected mux.log.%d to exist", i)
		}
	}
	path4 := filepath.Join(logDir, fmt.Sprintf("mux.log.%d", maxRotatedFiles+1))
	if _, err := os.Stat(path4); err == nil {
		t.Errorf("mux.log.%d should not exist (max %d rotated files)", maxRotatedFiles+1, maxRotatedFiles)
	}
}

// NOTE: redactToolArgs tests moved to mux package along with the function.
