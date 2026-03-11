package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CostEntry is a single line from an agent's costs.jsonl file.
type CostEntry struct {
	Timestamp string  `json:"timestamp"`
	Model     string  `json:"model"`
	Tokens    int     `json:"tokens"`
	CostUSD   float64 `json:"cost_usd"`
}

// CostSummary aggregates cost data.
type CostSummary struct {
	TotalUSD float64            `json:"total_usd"`
	ByModel  map[string]float64 `json:"by_model"`
	Count    int                `json:"count"`
}

// ReadCosts reads cost entries from an agent's data directory.
// Supports filtering by time period.
func ReadCosts(agentDir string, since time.Time) ([]CostEntry, error) {
	path := filepath.Join(agentDir, "data", ".nullclaw", "costs.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read costs: %w", err)
	}
	defer f.Close()

	var entries []CostEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e CostEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // skip malformed lines
		}
		if !since.IsZero() && e.Timestamp != "" {
			t, err := time.Parse(time.RFC3339, e.Timestamp)
			if err == nil && t.Before(since) {
				continue
			}
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

// SummarizeCosts aggregates a list of cost entries.
func SummarizeCosts(entries []CostEntry) CostSummary {
	s := CostSummary{ByModel: make(map[string]float64)}
	for _, e := range entries {
		s.TotalUSD += e.CostUSD
		s.ByModel[e.Model] += e.CostUSD
		s.Count++
	}
	return s
}
