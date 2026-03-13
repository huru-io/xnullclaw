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

const (
	defaultMaxFileSize = 10 * 1024 * 1024 // 10 MB
	maxRotatedFiles    = 3
)

// logTarget identifies which log file to write to.
type logTarget int

const (
	targetMux logTarget = iota
	targetAgent
	targetCost
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

	// Rotation fields.
	logDir      string // stored for reopening after rotation
	maxFileSize int64  // bytes; 0 = no rotation
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

	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, fmt.Errorf("logging: create log dir %s: %w", logDir, err)
	}

	muxFile, err := os.OpenFile(filepath.Join(logDir, "mux.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("logging: open mux.log: %w", err)
	}

	agentFile, err := os.OpenFile(filepath.Join(logDir, "agents.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		muxFile.Close()
		return nil, fmt.Errorf("logging: open agents.log: %w", err)
	}

	costFile, err := os.OpenFile(filepath.Join(logDir, "costs.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		muxFile.Close()
		agentFile.Close()
		return nil, fmt.Errorf("logging: open costs.log: %w", err)
	}

	maxSize := int64(defaultMaxFileSize)
	if cfg.MaxFileSizeMB > 0 {
		maxSize = int64(cfg.MaxFileSizeMB) * 1024 * 1024
	}

	return &Logger{
		muxLog:      muxFile,
		agentLog:    agentFile,
		costLog:     costFile,
		level:       ParseLevel(cfg.Level),
		nowFunc:     func() time.Time { return time.Now().UTC() },
		logDir:      logDir,
		maxFileSize: maxSize,
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

// targetInfo returns the file pointer and base filename for a log target.
// Caller must hold l.mu.
func (l *Logger) targetInfo(t logTarget) (fp **os.File, baseName string) {
	switch t {
	case targetAgent:
		return &l.agentLog, "agents.log"
	case targetCost:
		return &l.costLog, "costs.log"
	default:
		return &l.muxLog, "mux.log"
	}
}

// write serializes an Entry as JSON and appends it (with newline) to the
// target log file. The file pointer is resolved under the lock so that
// rotation (which swaps the pointer) is always safe.
// The caller must NOT hold l.mu.
func (l *Logger) write(target logTarget, e Entry) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	fp, baseName := l.targetInfo(target)
	if *fp != nil {
		_, _ = (*fp).Write(data)
	}
	if l.maxFileSize > 0 {
		l.maybeRotate(fp, baseName)
	}
	if l.mirror != nil {
		_, _ = l.mirror.Write(data)
	}
}

// maybeRotate checks if the file exceeds maxFileSize and rotates if needed.
// Caller must hold l.mu.
func (l *Logger) maybeRotate(fp **os.File, baseName string) {
	f := *fp
	info, err := f.Stat()
	if err != nil || info.Size() < l.maxFileSize {
		return
	}
	f.Close()
	base := filepath.Join(l.logDir, baseName)
	// Shift rotated files: .3 → delete, .2 → .3, .1 → .2.
	for i := maxRotatedFiles; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", base, i)
		if i == maxRotatedFiles {
			os.Remove(old)
		} else {
			os.Rename(old, fmt.Sprintf("%s.%d", base, i+1))
		}
	}
	os.Rename(base, base+".1")
	newFile, err := os.OpenFile(base, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		// Best effort: reopen the rotated file.
		newFile, _ = os.OpenFile(base+".1", os.O_WRONLY|os.O_APPEND, 0600)
	}
	if newFile == nil {
		// Both opens failed (disk full, permissions). Leave fp as the closed
		// handle — next write will error but won't nil-pointer panic.
		return
	}
	*fp = newFile
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
func (l *Logger) log(lvl Level, target logTarget, msg string, kvs []any) {
	if lvl < l.level {
		return
	}
	e := Entry{
		Timestamp: l.now().Format(time.RFC3339),
		Level:     lvl.String(),
		Message:   msg,
		Fields:    fieldsToMap(kvs),
	}
	l.write(target, e)
}

// logEntry writes a pre-built Entry to the given target if the level passes.
func (l *Logger) logEntry(lvl Level, target logTarget, e Entry) {
	if lvl < l.level {
		return
	}
	l.write(target, e)
}

// ---------------------------------------------------------------------------
// Main mux log methods
// ---------------------------------------------------------------------------

// Debug logs at debug level to mux.log.
func (l *Logger) Debug(msg string, fields ...any) {
	l.log(LevelDebug, targetMux, msg, fields)
}

// Info logs at info level to mux.log.
func (l *Logger) Info(msg string, fields ...any) {
	l.log(LevelInfo, targetMux, msg, fields)
}

// Warn logs at warn level to mux.log.
func (l *Logger) Warn(msg string, fields ...any) {
	l.log(LevelWarn, targetMux, msg, fields)
}

// Error logs at error level to mux.log.
func (l *Logger) Error(msg string, fields ...any) {
	l.log(LevelError, targetMux, msg, fields)
}

// ---------------------------------------------------------------------------
// Specialized logging methods for observability
// ---------------------------------------------------------------------------

// LogIncoming logs an incoming Telegram message to mux.log.
func (l *Logger) LogIncoming(userID string, text string, messageType string) {
	l.log(LevelInfo, targetMux, "incoming message", []any{
		"user_id", userID,
		"text_len", len(text),
		"type", messageType,
	})
}

// LogRouting logs an agent routing decision to mux.log.
func (l *Logger) LogRouting(agent string, reason string, confidence float64) {
	l.log(LevelInfo, targetMux, "routing decision", []any{
		"agent", agent,
		"reason", reason,
		"confidence", confidence,
	})
}

// LogToolCall logs a tool call execution to agents.log.
// The caller is responsible for redacting sensitive values in args before calling.
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
	l.logEntry(LevelInfo, targetAgent, e)
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
	l.logEntry(LevelInfo, targetAgent, e)
}

// LogAgentSend logs a message sent to an agent (agents.log).
func (l *Logger) LogAgentSend(agent string, msgLen int) {
	l.log(LevelInfo, targetAgent, "agent send", []any{
		"agent", agent,
		"msg_len", msgLen,
	})
}

// LogAgentResponse logs a response received from an agent (agents.log).
func (l *Logger) LogAgentResponse(agent string, respLen int, latencyMs int64) {
	l.log(LevelInfo, targetAgent, "agent response", []any{
		"agent", agent,
		"resp_len", respLen,
		"latency_ms", latencyMs,
	})
}

// LogLifecycle logs agent/mux lifecycle events to agents.log.
func (l *Logger) LogLifecycle(event string, agent string, details string) {
	l.log(LevelInfo, targetAgent, "lifecycle", []any{
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
	l.logEntry(LevelInfo, targetCost, e)
}

// LogCompaction logs a compaction event to mux.log.
func (l *Logger) LogCompaction(messagesCompacted int, factsExtracted int, durationMs int64) {
	l.log(LevelInfo, targetMux, "compaction", []any{
		"messages_compacted", messagesCompacted,
		"facts_extracted", factsExtracted,
		"duration_ms", durationMs,
	})
}

// LogPersonaChange logs a persona dimension change to mux.log.
func (l *Logger) LogPersonaChange(field string, oldValue, newValue string) {
	l.log(LevelInfo, targetMux, "persona change", []any{
		"field", field,
		"old_value", oldValue,
		"new_value", newValue,
	})
}

