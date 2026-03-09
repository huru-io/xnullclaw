// Package memory provides persistent memory storage backed by SQLite.
package memory

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ---------- Domain structs ----------

// Message represents a single message in the rolling window.
type Message struct {
	ID         int
	Role       string // "user", "assistant", "tool"
	Content    string
	ToolCalls  *string // JSON array of tool calls (nullable)
	ToolCallID *string // for tool result messages (nullable)
	Agent      *string // related agent (nullable)
	Stream     string  // "conversation" or "background"
	TokenCount int
	Timestamp  time.Time
}

// Fact represents a piece of long-term knowledge.
type Fact struct {
	ID          int
	Type        string // "preference", "decision", "rule", "knowledge", "pattern"
	Content     string
	Source      *string // which compaction/interaction produced it
	Agent       *string // related agent (nullable)
	Score       float64
	Created     time.Time
	Accessed    time.Time
	AccessCount int
}

// AgentState represents the persistent state summary for one agent.
type AgentState struct {
	Agent           string
	Emoji           *string
	Status          *string
	Role            *string
	Model           *string
	CurrentTask     *string
	LastMessage     *string
	LastResponse    *string
	LastInteraction *time.Time
	Error           *string
	Updated         time.Time
}

// Compaction represents a summarised block of conversation history.
type Compaction struct {
	ID          int
	PeriodStart time.Time
	PeriodEnd   time.Time
	Summary     string
	Agents      string // comma-separated agent names
	TokenCount  int
	Created     time.Time
}

// Cost represents a single cost tracking entry.
type Cost struct {
	ID           int
	Timestamp    time.Time
	Category     string // "loop", "compaction", "whisper", "tts", "pruning", "agent"
	Model        *string
	Agent        *string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// ---------- Schema ----------

const schema = `
CREATE TABLE IF NOT EXISTS messages (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    tool_calls   TEXT,
    tool_call_id TEXT,
    agent        TEXT,
    stream       TEXT DEFAULT 'conversation',
    token_count  INTEGER DEFAULT 0,
    timestamp    DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS facts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    type         TEXT NOT NULL,
    content      TEXT NOT NULL,
    source       TEXT,
    agent        TEXT,
    score        REAL DEFAULT 1.0,
    created      DATETIME DEFAULT CURRENT_TIMESTAMP,
    accessed     DATETIME DEFAULT CURRENT_TIMESTAMP,
    access_count INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS agent_state (
    agent            TEXT PRIMARY KEY,
    emoji            TEXT,
    status           TEXT,
    role             TEXT,
    model            TEXT,
    current_task     TEXT,
    last_message     TEXT,
    last_response    TEXT,
    last_interaction DATETIME,
    error            TEXT,
    updated          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS compactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    period_start DATETIME,
    period_end   DATETIME,
    summary      TEXT,
    agents       TEXT,
    token_count  INTEGER,
    created      DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS costs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp     DATETIME DEFAULT CURRENT_TIMESTAMP,
    category      TEXT NOT NULL,
    model         TEXT,
    agent         TEXT,
    input_tokens  INTEGER,
    output_tokens INTEGER,
    cost_usd      REAL NOT NULL
);
`

// ---------- Store ----------

// Store manages the mux's persistent memory in SQLite.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database at dbPath and ensures the schema exists.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: ping db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ==================== Messages ====================

// AddMessage inserts a message into the rolling window.
func (s *Store) AddMessage(msg Message) error {
	_, err := s.db.Exec(
		`INSERT INTO messages (role, content, tool_calls, tool_call_id, agent, stream, token_count, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.Role, msg.Content, msg.ToolCalls, msg.ToolCallID, msg.Agent,
		defaultStream(msg.Stream), msg.TokenCount, timeOrNow(msg.Timestamp),
	)
	return err
}

// RecentMessages returns the most recent limit messages from the given stream,
// ordered oldest-first so they can be appended to context in chronological order.
func (s *Store) RecentMessages(stream string, limit int) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, tool_calls, tool_call_id, agent, stream, token_count, timestamp
		 FROM messages WHERE stream = ?
		 ORDER BY id DESC LIMIT ?`, stream, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	// Reverse so oldest comes first.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// MessageTokenCount returns the total token count for all messages in the given stream.
func (s *Store) MessageTokenCount(stream string) (int, error) {
	var total int
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(token_count), 0) FROM messages WHERE stream = ?`, stream,
	).Scan(&total)
	return total, err
}

// DeleteOldestMessages removes the oldest count messages from the given stream and
// returns them (useful for compaction — the caller can summarise them).
func (s *Store) DeleteOldestMessages(stream string, count int) ([]Message, error) {
	// Read them first.
	rows, err := s.db.Query(
		`SELECT id, role, content, tool_calls, tool_call_id, agent, stream, token_count, timestamp
		 FROM messages WHERE stream = ?
		 ORDER BY id ASC LIMIT ?`, stream, count,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	ids := make([]interface{}, len(msgs))
	placeholders := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
		placeholders[i] = "?"
	}
	_, err = s.db.Exec(
		fmt.Sprintf("DELETE FROM messages WHERE id IN (%s)", strings.Join(placeholders, ",")),
		ids...,
	)
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

// MessagesSince returns all messages in the given stream with a timestamp >= t,
// ordered oldest-first.
func (s *Store) MessagesSince(t time.Time, stream string) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, role, content, tool_calls, tool_call_id, agent, stream, token_count, timestamp
		 FROM messages WHERE stream = ? AND timestamp >= ?
		 ORDER BY id ASC`, stream, t,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ==================== Facts ====================

// AddFact inserts a new fact after checking for duplicates. If an existing fact
// with the same type has substantially overlapping content (simple substring
// check), the insert is skipped and nil is returned.
func (s *Store) AddFact(f Fact) error {
	// Simple dedup: check if any fact of the same type contains or is contained by this content.
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM facts
		 WHERE type = ? AND (content = ? OR content LIKE ? OR ? LIKE '%' || content || '%')`,
		f.Type, f.Content, "%"+f.Content+"%", f.Content,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil // duplicate detected, skip
	}

	score := f.Score
	if score == 0 {
		score = 1.0
	}
	_, err = s.db.Exec(
		`INSERT INTO facts (type, content, source, agent, score, created, accessed, access_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Type, f.Content, f.Source, f.Agent, score,
		timeOrNow(f.Created), timeOrNow(f.Accessed), f.AccessCount,
	)
	return err
}

// SearchFacts retrieves facts matching the query keywords and optional agent filter,
// ordered by score descending. Pass agent="" to search across all agents.
func (s *Store) SearchFacts(query string, agent string, limit int) ([]Fact, error) {
	var rows *sql.Rows
	var err error
	if agent != "" {
		rows, err = s.db.Query(
			`SELECT id, type, content, source, agent, score, created, accessed, access_count
			 FROM facts
			 WHERE content LIKE ? AND agent = ?
			 ORDER BY score DESC
			 LIMIT ?`,
			"%"+query+"%", agent, limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, type, content, source, agent, score, created, accessed, access_count
			 FROM facts
			 WHERE content LIKE ?
			 ORDER BY score DESC
			 LIMIT ?`,
			"%"+query+"%", limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}

// GetFactsByType returns all facts of the given type, ordered by score descending.
func (s *Store) GetFactsByType(factType string) ([]Fact, error) {
	rows, err := s.db.Query(
		`SELECT id, type, content, source, agent, score, created, accessed, access_count
		 FROM facts WHERE type = ?
		 ORDER BY score DESC`, factType,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}

// UpdateFactAccess bumps the accessed timestamp and increments access_count.
func (s *Store) UpdateFactAccess(id int) error {
	_, err := s.db.Exec(
		`UPDATE facts SET accessed = CURRENT_TIMESTAMP, access_count = access_count + 1
		 WHERE id = ?`, id,
	)
	return err
}

// DeleteFact removes a fact by its ID.
func (s *Store) DeleteFact(id int) error {
	result, err := s.db.Exec(`DELETE FROM facts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("fact %d not found", id)
	}
	return nil
}

// DecayFactScores reduces the score of facts that have not been accessed within
// maxAge by multiplying their score by decayFactor.
func (s *Store) DecayFactScores(maxAge time.Duration, decayFactor float64) error {
	cutoff := time.Now().Add(-maxAge)
	_, err := s.db.Exec(
		`UPDATE facts SET score = score * ?
		 WHERE accessed < ?`, decayFactor, cutoff,
	)
	return err
}

// PruneFacts deletes facts with a score below minScore and returns the deleted rows.
func (s *Store) PruneFacts(minScore float64) ([]Fact, error) {
	rows, err := s.db.Query(
		`SELECT id, type, content, source, agent, score, created, accessed, access_count
		 FROM facts WHERE score < ?`, minScore,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts, err := scanFacts(rows)
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return nil, nil
	}

	_, err = s.db.Exec(`DELETE FROM facts WHERE score < ?`, minScore)
	if err != nil {
		return nil, err
	}
	return facts, nil
}

// ==================== Agent State ====================

// UpsertAgentState inserts or replaces the full agent state row.
func (s *Store) UpsertAgentState(state AgentState) error {
	_, err := s.db.Exec(
		`INSERT INTO agent_state (agent, emoji, status, role, model, current_task,
		     last_message, last_response, last_interaction, error, updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(agent) DO UPDATE SET
		     emoji            = excluded.emoji,
		     status           = excluded.status,
		     role             = excluded.role,
		     model            = excluded.model,
		     current_task     = excluded.current_task,
		     last_message     = excluded.last_message,
		     last_response    = excluded.last_response,
		     last_interaction = excluded.last_interaction,
		     error            = excluded.error,
		     updated          = excluded.updated`,
		state.Agent, state.Emoji, state.Status, state.Role, state.Model,
		state.CurrentTask, state.LastMessage, state.LastResponse,
		state.LastInteraction, state.Error, timeOrNow(state.Updated),
	)
	return err
}

// GetAgentState returns the state for a single agent, or nil if not found.
func (s *Store) GetAgentState(agent string) (*AgentState, error) {
	row := s.db.QueryRow(
		`SELECT agent, emoji, status, role, model, current_task,
		        last_message, last_response, last_interaction, error, updated
		 FROM agent_state WHERE agent = ?`, agent,
	)
	st, err := scanAgentState(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

// AllAgentStates returns every agent state row.
func (s *Store) AllAgentStates() ([]AgentState, error) {
	rows, err := s.db.Query(
		`SELECT agent, emoji, status, role, model, current_task,
		        last_message, last_response, last_interaction, error, updated
		 FROM agent_state ORDER BY agent`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []AgentState
	for rows.Next() {
		st, err := scanAgentStateRow(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *st)
	}
	return states, rows.Err()
}

// ==================== Compactions ====================

// AddCompaction inserts a new compaction summary.
func (s *Store) AddCompaction(c Compaction) error {
	_, err := s.db.Exec(
		`INSERT INTO compactions (period_start, period_end, summary, agents, token_count, created)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.PeriodStart, c.PeriodEnd, c.Summary, c.Agents, c.TokenCount, timeOrNow(c.Created),
	)
	return err
}

// RecentCompactions returns the most recent limit compactions, ordered newest-first.
func (s *Store) RecentCompactions(limit int) ([]Compaction, error) {
	rows, err := s.db.Query(
		`SELECT id, period_start, period_end, summary, agents, token_count, created
		 FROM compactions ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCompactions(rows)
}

// CompactionsByDateRange returns compactions whose period overlaps the given range.
func (s *Store) CompactionsByDateRange(start, end time.Time) ([]Compaction, error) {
	rows, err := s.db.Query(
		`SELECT id, period_start, period_end, summary, agents, token_count, created
		 FROM compactions
		 WHERE period_start <= ? AND period_end >= ?
		 ORDER BY period_start ASC`, end, start,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCompactions(rows)
}

// ==================== Costs ====================

// AddCost inserts a cost tracking entry.
func (s *Store) AddCost(c Cost) error {
	_, err := s.db.Exec(
		`INSERT INTO costs (timestamp, category, model, agent, input_tokens, output_tokens, cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		timeOrNow(c.Timestamp), c.Category, c.Model, c.Agent,
		c.InputTokens, c.OutputTokens, c.CostUSD,
	)
	return err
}

// CostsByPeriod returns all cost entries within the given time range.
func (s *Store) CostsByPeriod(start, end time.Time) ([]Cost, error) {
	rows, err := s.db.Query(
		`SELECT id, timestamp, category, model, agent, input_tokens, output_tokens, cost_usd
		 FROM costs WHERE timestamp >= ? AND timestamp <= ?
		 ORDER BY timestamp ASC`, start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCosts(rows)
}

// CostSummary returns total cost per category within the given time range.
func (s *Store) CostSummary(start, end time.Time) (map[string]float64, error) {
	rows, err := s.db.Query(
		`SELECT category, COALESCE(SUM(cost_usd), 0)
		 FROM costs WHERE timestamp >= ? AND timestamp <= ?
		 GROUP BY category`, start, end,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := make(map[string]float64)
	for rows.Next() {
		var cat string
		var total float64
		if err := rows.Scan(&cat, &total); err != nil {
			return nil, err
		}
		summary[cat] = total
	}
	return summary, rows.Err()
}

// AgentCostSummary returns total cost for a specific agent within the given time range.
func (s *Store) AgentCostSummary(agent string, start, end time.Time) (float64, error) {
	var total float64
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(cost_usd), 0)
		 FROM costs WHERE agent = ? AND timestamp >= ? AND timestamp <= ?`,
		agent, start, end,
	).Scan(&total)
	return total, err
}

// ==================== helpers ====================

func defaultStream(s string) string {
	if s == "" {
		return "conversation"
	}
	return s
}

func timeOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(
			&m.ID, &m.Role, &m.Content, &m.ToolCalls, &m.ToolCallID,
			&m.Agent, &m.Stream, &m.TokenCount, &m.Timestamp,
		); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func scanFacts(rows *sql.Rows) ([]Fact, error) {
	var facts []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(
			&f.ID, &f.Type, &f.Content, &f.Source, &f.Agent,
			&f.Score, &f.Created, &f.Accessed, &f.AccessCount,
		); err != nil {
			return nil, err
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func scanAgentState(row *sql.Row) (*AgentState, error) {
	var st AgentState
	err := row.Scan(
		&st.Agent, &st.Emoji, &st.Status, &st.Role, &st.Model,
		&st.CurrentTask, &st.LastMessage, &st.LastResponse,
		&st.LastInteraction, &st.Error, &st.Updated,
	)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func scanAgentStateRow(rows *sql.Rows) (*AgentState, error) {
	var st AgentState
	err := rows.Scan(
		&st.Agent, &st.Emoji, &st.Status, &st.Role, &st.Model,
		&st.CurrentTask, &st.LastMessage, &st.LastResponse,
		&st.LastInteraction, &st.Error, &st.Updated,
	)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func scanCompactions(rows *sql.Rows) ([]Compaction, error) {
	var cs []Compaction
	for rows.Next() {
		var c Compaction
		if err := rows.Scan(
			&c.ID, &c.PeriodStart, &c.PeriodEnd, &c.Summary,
			&c.Agents, &c.TokenCount, &c.Created,
		); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

func scanCosts(rows *sql.Rows) ([]Cost, error) {
	var costs []Cost
	for rows.Next() {
		var c Cost
		if err := rows.Scan(
			&c.ID, &c.Timestamp, &c.Category, &c.Model, &c.Agent,
			&c.InputTokens, &c.OutputTokens, &c.CostUSD,
		); err != nil {
			return nil, err
		}
		costs = append(costs, c)
	}
	return costs, rows.Err()
}
