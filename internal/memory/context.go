// Package memory — context.go assembles context data for each model call with full time awareness.
package memory

import (
	"fmt"
	"strings"
	"time"
)

// ContextData holds all the data needed to build a system prompt for one turn.
type ContextData struct {
	CurrentTime time.Time
	Agents      []AgentState
	Facts       []Fact
	Compactions []Compaction
	Rules       []Fact    // facts where type='rule'
	DrainMsgs   []Message // recent agent drain output

	// Time-aware metadata
	LastUserMessage *time.Time               // when the user last sent a message
	TimeSinceUser   time.Duration            // duration since last user message
	AgentLastSeen   map[string]time.Duration // agent name -> time since last interaction
}

// Assembler builds context data from the store for each turn.
type Assembler struct {
	store          *Store
	maxFacts       int // max facts to retrieve (default 30)
	maxCompactions int // max compaction summaries (default 3)
}

// NewAssembler creates an Assembler with sensible defaults.
func NewAssembler(store *Store) *Assembler {
	return &Assembler{
		store:          store,
		maxFacts:       30,
		maxCompactions: 3,
	}
}

// Assemble gathers all context data for the current turn.
// query is the current user message (used for fact relevance matching).
func (a *Assembler) Assemble(query string) (*ContextData, error) {
	now := time.Now()
	cd := &ContextData{
		CurrentTime:   now,
		AgentLastSeen: make(map[string]time.Duration),
	}

	// 1. Load all agent states.
	agents, err := a.store.AllAgentStates()
	if err != nil {
		return nil, fmt.Errorf("context: load agents: %w", err)
	}
	cd.Agents = agents

	// 2. Search for relevant facts using keywords from the query.
	keywords := ExtractKeywords(query)
	seen := make(map[int]bool) // deduplicate facts by ID

	for _, kw := range keywords {
		results, err := a.store.SearchFacts(kw, "", a.maxFacts)
		if err != nil {
			return nil, fmt.Errorf("context: search facts for %q: %w", kw, err)
		}
		for _, f := range results {
			if !seen[f.ID] {
				seen[f.ID] = true
				cd.Facts = append(cd.Facts, f)
			}
		}
	}

	// Always include type='rule' and type='preference' regardless of query.
	rules, err := a.store.GetFactsByType("rule")
	if err != nil {
		return nil, fmt.Errorf("context: load rules: %w", err)
	}
	for _, f := range rules {
		if !seen[f.ID] {
			seen[f.ID] = true
			cd.Facts = append(cd.Facts, f)
		}
	}
	cd.Rules = rules

	prefs, err := a.store.GetFactsByType("preference")
	if err != nil {
		return nil, fmt.Errorf("context: load preferences: %w", err)
	}
	for _, f := range prefs {
		if !seen[f.ID] {
			seen[f.ID] = true
			cd.Facts = append(cd.Facts, f)
		}
	}

	// Cap total facts.
	if len(cd.Facts) > a.maxFacts {
		cd.Facts = cd.Facts[:a.maxFacts]
	}

	// 3. Load recent compaction summaries.
	compactions, err := a.store.RecentCompactions(a.maxCompactions)
	if err != nil {
		return nil, fmt.Errorf("context: load compactions: %w", err)
	}
	cd.Compactions = compactions

	// 4. Load recent drain messages (agent output already sent to Telegram).
	drainMsgs, err := a.store.RecentMessages("drain", 10)
	if err != nil {
		return nil, fmt.Errorf("context: load drain messages: %w", err)
	}
	cd.DrainMsgs = drainMsgs

	// 5. Get the last user message timestamp.
	lastMsg, err := a.lastUserMessageTime()
	if err != nil {
		return nil, fmt.Errorf("context: last user message: %w", err)
	}
	if lastMsg != nil {
		cd.LastUserMessage = lastMsg
		cd.TimeSinceUser = now.Sub(*lastMsg)
	}

	// 5. For each agent, calculate time since last_interaction.
	for _, ag := range agents {
		if ag.LastInteraction != nil {
			cd.AgentLastSeen[ag.Agent] = now.Sub(*ag.LastInteraction)
		}
	}

	return cd, nil
}

// lastUserMessageTime returns the timestamp of the most recent user message,
// or nil if there are no user messages.
func (a *Assembler) lastUserMessageTime() (*time.Time, error) {
	var ts time.Time
	err := a.store.db.QueryRow(
		`SELECT timestamp FROM messages WHERE role = 'user'
		 ORDER BY id DESC LIMIT 1`,
	).Scan(&ts)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &ts, nil
}

// stopWords is the set of common words filtered out during keyword extraction.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"may": true, "might": true, "can": true, "shall": true, "to": true,
	"of": true, "in": true, "for": true, "on": true, "with": true,
	"at": true, "by": true, "from": true, "as": true, "into": true,
	"about": true, "like": true, "through": true, "after": true, "before": true,
	"between": true, "out": true, "up": true, "down": true, "then": true,
	"than": true, "so": true, "no": true, "not": true, "only": true,
	"very": true, "just": true, "that": true, "this": true, "it": true,
	"its": true, "my": true, "your": true, "his": true, "her": true,
	"our": true, "their": true, "what": true, "which": true, "who": true,
	"when": true, "where": true, "how": true, "all": true, "each": true,
	"every": true, "both": true, "few": true, "more": true, "most": true,
	"other": true, "some": true, "such": true, "and": true, "but": true,
	"or": true, "if": true, "while": true, "because": true, "until": true,
}

// ExtractKeywords returns significant words from a message for fact search.
// It splits on whitespace, lowercases, filters stop words and words shorter
// than 3 characters, and returns unique results.
func ExtractKeywords(message string) []string {
	words := strings.Fields(message)
	seen := make(map[string]bool)
	var result []string

	for _, w := range words {
		w = strings.ToLower(strings.Trim(w, ".,;:!?\"'()[]{}"))
		if len(w) < 3 {
			continue
		}
		if stopWords[w] {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		result = append(result, w)
	}
	return result
}
