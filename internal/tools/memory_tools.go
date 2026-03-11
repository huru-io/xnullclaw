package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/jotavich/xnullclaw/internal/memory"
)

func registerMemoryTools(r *Registry, store *memory.Store) {
	// remember
	r.Register(
		Definition{
			Name:        "remember",
			Description: "Store a fact in the mux's long-term memory",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fact":       map[string]any{"type": "string", "description": "The fact to remember"},
					"importance": map[string]any{"type": "string", "description": "Importance level", "enum": []string{"preference", "decision", "rule", "knowledge", "pattern"}},
				},
				"required": []string{"fact", "importance"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			fact, err := stringArg(args, "fact")
			if err != nil {
				return "", err
			}
			importance, err := stringArg(args, "importance")
			if err != nil {
				return "", err
			}
			validTypes := map[string]bool{
				"preference": true, "decision": true, "rule": true,
				"knowledge": true, "pattern": true,
			}
			if !validTypes[importance] {
				return "", fmt.Errorf("invalid importance type: %s", importance)
			}
			src := "mux-tool"
			err = store.AddFact(memory.Fact{
				Type:    importance,
				Content: fact,
				Source:  &src,
			})
			if err != nil {
				return "", fmt.Errorf("failed to store fact: %w", err)
			}
			return "remembered", nil
		},
	)

	// recall
	r.Register(
		Definition{
			Name:        "recall",
			Description: "Search the mux's long-term memory for matching facts",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query keywords"},
				},
				"required": []string{"query"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			query, err := stringArg(args, "query")
			if err != nil {
				return "", err
			}
			facts, err := store.SearchFacts(query, "", 20)
			if err != nil {
				return "", fmt.Errorf("failed to search facts: %w", err)
			}
			if len(facts) == 0 {
				return "No matching facts found.", nil
			}
			var sb strings.Builder
			for i, f := range facts {
				agent := ""
				if f.Agent != nil {
					agent = fmt.Sprintf(" [agent: %s]", *f.Agent)
				}
				fmt.Fprintf(&sb, "%d. [%s]%s %s (score: %.2f)\n", i+1, f.Type, agent, f.Content, f.Score)
				_ = store.UpdateFactAccess(f.ID)
			}
			return sb.String(), nil
		},
	)

	// get_conversation_summary
	r.Register(
		Definition{
			Name:        "get_conversation_summary",
			Description: "Get summary of recent conversation with an agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent": map[string]any{"type": "string", "description": "Agent name (optional)"},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agentFilter := optionalStringArg(args, "agent", "")
			compactions, err := store.RecentCompactions(10)
			if err != nil {
				return "", fmt.Errorf("failed to query compactions: %w", err)
			}
			if len(compactions) == 0 {
				return "No conversation summaries available yet.", nil
			}
			var sb strings.Builder
			count := 0
			for _, c := range compactions {
				if agentFilter != "" && !strings.Contains(c.Agents, agentFilter) {
					continue
				}
				fmt.Fprintf(&sb, "Period: %s to %s\n", c.PeriodStart.Format("2006-01-02 15:04"), c.PeriodEnd.Format("2006-01-02 15:04"))
				if c.Agents != "" {
					fmt.Fprintf(&sb, "Agents: %s\n", c.Agents)
				}
				fmt.Fprintf(&sb, "Summary: %s\n\n", c.Summary)
				count++
			}
			if count == 0 {
				return fmt.Sprintf("No conversation summaries found for agent: %s", agentFilter), nil
			}
			return sb.String(), nil
		},
	)
}
