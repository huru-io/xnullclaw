// Package tools — costs.go implements the cost tracking and budget tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/memory"
)

func registerCostTools(r *Registry, cfg *config.Config, configPath string, store *memory.Store, wrapperPath string) {
	// -----------------------------------------------------------------------
	// get_costs
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_costs",
			Description: "Get full system cost report for a period: today, week, month, or all",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"period": map[string]any{"type": "string", "description": "Time period", "enum": []string{"today", "week", "month", "all"}},
				},
				"required": []string{"period"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			period, err := stringArg(args, "period")
			if err != nil {
				return "", err
			}
			start, end, err := periodToRange(period)
			if err != nil {
				return "", err
			}

			// Mux costs from the local store.
			summary, err := store.CostSummary(start, end)
			if err != nil {
				return "", fmt.Errorf("failed to query costs: %w", err)
			}

			var total float64
			for _, v := range summary {
				total += v
			}

			report := map[string]any{
				"period":     period,
				"mux_costs":  summary,
				"mux_total":  total,
				"budget": map[string]any{
					"daily_usd":   cfg.Costs.DailyBudgetUSD,
					"monthly_usd": cfg.Costs.MonthlyBudgetUSD,
				},
			}

			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal cost report: %w", err)
			}
			return string(data), nil
		},
	)

	// -----------------------------------------------------------------------
	// get_agent_costs
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "get_agent_costs",
			Description: "Get cost details for a specific agent",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent":  map[string]any{"type": "string", "description": "Agent name"},
					"period": map[string]any{"type": "string", "description": "Time period", "enum": []string{"today", "week", "month", "all"}},
				},
				"required": []string{"agent", "period"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			agent, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			period, err := stringArg(args, "period")
			if err != nil {
				return "", err
			}

			// Map period to the xnullclaw flag.
			var flag string
			switch period {
			case "today":
				flag = "--today"
			case "week":
				flag = "--week"
			case "month":
				flag = "--month"
			default:
				flag = "--all"
			}

			return runWrapper(ctx, wrapperPath, agent, "costs", "--json", flag)
		},
	)

	// -----------------------------------------------------------------------
	// set_budget
	// -----------------------------------------------------------------------
	r.Register(
		Definition{
			Name:        "set_budget",
			Description: "Set a budget limit. Scope: 'total' or an agent name. Period: 'daily' or 'monthly'.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":     map[string]any{"type": "string", "description": "Budget scope: 'total' for system-wide, or agent name for per-agent"},
					"limit_usd": map[string]any{"type": "number", "description": "Budget limit in USD"},
					"period":    map[string]any{"type": "string", "description": "Budget period", "enum": []string{"daily", "monthly"}},
				},
				"required": []string{"scope", "limit_usd", "period"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			scope, err := stringArg(args, "scope")
			if err != nil {
				return "", err
			}
			limitUSD, err := float64Arg(args, "limit_usd")
			if err != nil {
				return "", err
			}
			period, err := stringArg(args, "period")
			if err != nil {
				return "", err
			}
			if limitUSD < 0 {
				return "", fmt.Errorf("limit_usd must be non-negative")
			}

			var result strings.Builder
			switch {
			case scope == "total" && period == "daily":
				cfg.Costs.DailyBudgetUSD = limitUSD
				fmt.Fprintf(&result, "Daily budget set to $%.2f", limitUSD)
			case scope == "total" && period == "monthly":
				cfg.Costs.MonthlyBudgetUSD = limitUSD
				fmt.Fprintf(&result, "Monthly budget set to $%.2f", limitUSD)
			case period == "daily":
				// Per-agent daily limit — stored in the global config for now.
				cfg.Costs.PerAgentDailyLimit = limitUSD
				fmt.Fprintf(&result, "Per-agent daily limit set to $%.2f (scope: %s)", limitUSD, scope)
			default:
				return "", fmt.Errorf("unsupported scope/period combination: %s/%s", scope, period)
			}

			if err := cfg.Save(configPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return result.String(), nil
		},
	)
}

// periodToRange converts a period string to a start/end time range.
func periodToRange(period string) (time.Time, time.Time, error) {
	now := time.Now()
	end := now

	switch period {
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return start, end, nil
	case "week":
		start := now.AddDate(0, 0, -7)
		return start, end, nil
	case "month":
		start := now.AddDate(0, -1, 0)
		return start, end, nil
	case "all":
		start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		return start, end, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid period: %s (must be one of: today, week, month, all)", period)
	}
}
