// Package logging provides structured JSON logging to files for the mux daemon.
//
// Three log files are maintained:
//   - mux.log:    main operational log (routing, lifecycle, general messages)
//   - agents.log: agent interaction log (sends, responses, tool calls, model calls)
//   - costs.log:  cost event log (token usage, USD amounts per call)
//
// All entries are JSONL (one JSON object per line) with ISO 8601 UTC timestamps.
// Writes are protected by a mutex for goroutine safety.
package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jotavich/xnullclaw/internal/config"
)

// Level represents a log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the lowercase name of the level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLevel converts a string level name to a Level.
// Defaults to LevelInfo for unrecognized values.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Entry is a single log entry serialized as one JSON line.
type Entry struct {
	Timestamp string         `json:"ts"`
	Level     string         `json:"level"`
	Message   string         `json:"msg"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger provides structured JSON logging to files.
type Logger struct {
	muxLog   *os.File // main mux log
	agentLog *os.File // agent interaction log
	costLog  *os.File // cost events
	level    Level
	mu       sync.Mutex

	// nowFunc is used for timestamps; overridden in tests.
	nowFunc func() time.Time

	// mirror receives a copy of all log entries when non-nil (e.g. stderr
	// for foreground mode).
	mirror *os.File
}

// New creates a new Logger, creating the log directory and opening log files.
// baseDir is the mux data directory (e.g. ~/.xnc/.mux/).
// The LoggingConfig.Dir field is joined with baseDir to form the log directory,
// unless it is an absolute path.
func New(cfg *config.LoggingConfig, baseDir string) (*Logger, error) {
	logDir := cfg.Dir
	if logDir == "" {
		logDir = "logs"
	}
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(baseDir, logDir)
	}

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("logging: create log dir %s: %w", logDir, err)
	}

	muxFile, err := os.OpenFile(filepath.Join(logDir, "mux.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("logging: open mux.log: %w", err)
	}

	agentFile, err := os.OpenFile(filepath.Join(logDir, "agents.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		muxFile.Close()
		return nil, fmt.Errorf("logging: open agents.log: %w", err)
	}

	costFile, err := os.OpenFile(filepath.Join(logDir, "costs.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		muxFile.Close()
		agentFile.Close()
		return nil, fmt.Errorf("logging: open costs.log: %w", err)
	}

	return &Logger{
		muxLog:   muxFile,
		agentLog: agentFile,
		costLog:  costFile,
		level:    ParseLevel(cfg.Level),
		nowFunc:  func() time.Time { return time.Now().UTC() },
	}, nil
}

// SetMirror sets an additional output for all log entries (e.g. os.Stderr
// for foreground mode). Pass nil to disable.
func (l *Logger) SetMirror(w *os.File) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mirror = w
}

// Close flushes and closes all log files.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var errs []string
	if l.muxLog != nil {
		if err := l.muxLog.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("mux.log: %v", err))
		}
	}
	if l.agentLog != nil {
		if err := l.agentLog.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("agents.log: %v", err))
		}
	}
	if l.costLog != nil {
		if err := l.costLog.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("costs.log: %v", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("logging: close errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// now returns the current UTC time, using the overridable nowFunc.
func (l *Logger) now() time.Time {
	return l.nowFunc()
}

// write serializes an Entry as JSON and appends it (with newline) to the given file.
// If a mirror is set, the entry is also written there.
// The caller must NOT hold l.mu.
func (l *Logger) write(f *os.File, e Entry) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = f.Write(data)
	if l.mirror != nil {
		_, _ = l.mirror.Write(data)
	}
}

// fieldsToMap converts variadic key/value pairs into a map.
// Odd-length slices have their last value set to nil.
// Error values are converted to their string representation so they
// serialize properly in JSON (Go error types have no exported fields).
func fieldsToMap(kvs []any) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]any, (len(kvs)+1)/2)
	for i := 0; i < len(kvs); i += 2 {
		key := fmt.Sprint(kvs[i])
		var val any
		if i+1 < len(kvs) {
			val = kvs[i+1]
			if e, ok := val.(error); ok {
				val = e.Error()
			}
		}
		m[key] = val
	}
	return m
}

// log is the internal method that all level methods delegate to.
func (l *Logger) log(lvl Level, f *os.File, msg string, kvs []any) {
	if lvl < l.level {
		return
	}
	e := Entry{
		Timestamp: l.now().Format(time.RFC3339),
		Level:     lvl.String(),
		Message:   msg,
		Fields:    fieldsToMap(kvs),
	}
	l.write(f, e)
}

// logEntry writes a pre-built Entry to the given file if the level passes.
func (l *Logger) logEntry(lvl Level, f *os.File, e Entry) {
	if lvl < l.level {
		return
	}
	l.write(f, e)
}

// ---------------------------------------------------------------------------
// Main mux log methods
// ---------------------------------------------------------------------------

// Debug logs at debug level to mux.log.
func (l *Logger) Debug(msg string, fields ...any) {
	l.log(LevelDebug, l.muxLog, msg, fields)
}

// Info logs at info level to mux.log.
func (l *Logger) Info(msg string, fields ...any) {
	l.log(LevelInfo, l.muxLog, msg, fields)
}

// Warn logs at warn level to mux.log.
func (l *Logger) Warn(msg string, fields ...any) {
	l.log(LevelWarn, l.muxLog, msg, fields)
}

// Error logs at error level to mux.log.
func (l *Logger) Error(msg string, fields ...any) {
	l.log(LevelError, l.muxLog, msg, fields)
}

// ---------------------------------------------------------------------------
// Specialized logging methods for observability
// ---------------------------------------------------------------------------

// LogIncoming logs an incoming Telegram message to mux.log.
func (l *Logger) LogIncoming(userID string, text string, messageType string) {
	l.log(LevelInfo, l.muxLog, "incoming message", []any{
		"user_id", userID,
		"text_len", len(text),
		"type", messageType,
	})
}

// LogRouting logs an agent routing decision to mux.log.
func (l *Logger) LogRouting(agent string, reason string, confidence float64) {
	l.log(LevelInfo, l.muxLog, "routing decision", []any{
		"agent", agent,
		"reason", reason,
		"confidence", confidence,
	})
}

// LogToolCall logs a tool call execution to agents.log.
func (l *Logger) LogToolCall(name string, args map[string]any, result string, duration time.Duration, err error) {
	fields := map[string]any{
		"tool":        name,
		"args":        args,
		"result_len":  len(result),
		"duration_ms": duration.Milliseconds(),
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	e := Entry{
		Timestamp: l.now().Format(time.RFC3339),
		Level:     LevelInfo.String(),
		Message:   "tool call",
		Fields:    fields,
	}
	l.logEntry(LevelInfo, l.agentLog, e)
}

// LogModelCall logs an OpenAI API call to agents.log.
func (l *Logger) LogModelCall(model string, inputTokens, outputTokens int, costUSD float64, latencyMs int64) {
	e := Entry{
		Timestamp: l.now().Format(time.RFC3339),
		Level:     LevelInfo.String(),
		Message:   "model call",
		Fields: map[string]any{
			"model":         model,
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"cost_usd":      costUSD,
			"latency_ms":    latencyMs,
		},
	}
	l.logEntry(LevelInfo, l.agentLog, e)
}

// LogAgentSend logs a message sent to an agent (agents.log).
func (l *Logger) LogAgentSend(agent string, msgLen int) {
	l.log(LevelInfo, l.agentLog, "agent send", []any{
		"agent", agent,
		"msg_len", msgLen,
	})
}

// LogAgentResponse logs a response received from an agent (agents.log).
func (l *Logger) LogAgentResponse(agent string, respLen int, latencyMs int64) {
	l.log(LevelInfo, l.agentLog, "agent response", []any{
		"agent", agent,
		"resp_len", respLen,
		"latency_ms", latencyMs,
	})
}

// LogLifecycle logs agent/mux lifecycle events to agents.log.
func (l *Logger) LogLifecycle(event string, agent string, details string) {
	l.log(LevelInfo, l.agentLog, "lifecycle", []any{
		"event", event,
		"agent", agent,
		"details", details,
	})
}

// LogCost logs a cost event to costs.log.
func (l *Logger) LogCost(category, model, agent string, inputTokens, outputTokens int, costUSD float64) {
	e := Entry{
		Timestamp: l.now().Format(time.RFC3339),
		Level:     LevelInfo.String(),
		Message:   "cost",
		Fields: map[string]any{
			"category":      category,
			"model":         model,
			"agent":         agent,
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"cost_usd":      costUSD,
		},
	}
	l.logEntry(LevelInfo, l.costLog, e)
}

// LogCompaction logs a compaction event to mux.log.
func (l *Logger) LogCompaction(messagesCompacted int, factsExtracted int, durationMs int64) {
	l.log(LevelInfo, l.muxLog, "compaction", []any{
		"messages_compacted", messagesCompacted,
		"facts_extracted", factsExtracted,
		"duration_ms", durationMs,
	})
}

// LogPersonaChange logs a persona dimension change to mux.log.
func (l *Logger) LogPersonaChange(field string, oldValue, newValue string) {
	l.log(LevelInfo, l.muxLog, "persona change", []any{
		"field", field,
		"old_value", oldValue,
		"new_value", newValue,
	})
}
