// Package tools — passthrough.go implements passthrough rule management tools.
package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jotavich/xnullclaw/mux/memory"
)

func registerPassthroughTools(r *Registry, store *memory.Store) {
	// -----------------------------------------------------------------------
	// set_passthrough_rule
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "set_passthrough_rule",
			Description: "Add a passthrough rule. Scope: agent name or 'global'. Rules modify messages before forwarding.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope": map[string]any{"type": "string", "description": "Agent name for agent-specific rule, or 'global' for all agents"},
					"rule":  map[string]any{"type": "string", "description": "The passthrough rule text"},
				},
				"required": []string{"scope", "rule"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			scope, err := stringArg(args, "scope")
			if err != nil {
				return "", err
			}
			rule, err := stringArg(args, "rule")
			if err != nil {
				return "", err
			}

			var agent *string
			if scope != "global" {
				agent = &scope
			}

			src := "passthrough-tool"
			err = store.AddFact(memory.Fact{
				Type:    "rule",
				Content: rule,
				Source:  &src,
				Agent:   agent,
			})
			if err != nil {
				return "", fmt.Errorf("failed to store rule: %w", err)
			}
			return fmt.Sprintf("Passthrough rule added for scope: %s", scope), nil
		},
	)

	// -----------------------------------------------------------------------
	// remove_passthrough_rule
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "remove_passthrough_rule",
			Description: "Remove a passthrough rule by its ID",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rule_id": map[string]any{"type": "string", "description": "Rule ID to remove"},
				},
				"required": []string{"rule_id"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			ruleIDStr, err := stringArg(args, "rule_id")
			if err != nil {
				return "", err
			}
			ruleID, err := strconv.Atoi(ruleIDStr)
			if err != nil {
				return "", fmt.Errorf("invalid rule_id: %s (must be a number)", ruleIDStr)
			}
			if err := store.DeleteFact(ruleID); err != nil {
				return "", fmt.Errorf("failed to remove rule: %w", err)
			}
			return fmt.Sprintf("Passthrough rule %d removed", ruleID), nil
		},
	)

	// -----------------------------------------------------------------------
	// list_passthrough_rules
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "list_passthrough_rules",
			Description: "List all active passthrough rules",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			facts, err := store.GetFactsByType("rule")
			if err != nil {
				return "", fmt.Errorf("failed to query rules: %w", err)
			}
			if len(facts) == 0 {
				return "No passthrough rules configured.", nil
			}
			var sb strings.Builder
			for _, f := range facts {
				scope := "global"
				if f.Agent != nil {
					scope = *f.Agent
				}
				fmt.Fprintf(&sb, "ID: %d | Scope: %s | Rule: %s\n", f.ID, scope, f.Content)
			}
			return sb.String(), nil
		},
	)
}
