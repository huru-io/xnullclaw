package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func registerCostTools(r *Registry, d Deps) {
	// get_costs
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

			summary, err := d.Store.CostSummary(start, end)
			if err != nil {
				return "", fmt.Errorf("failed to query costs: %w", err)
			}

			var total float64
			for _, v := range summary {
				total += v
			}

			report := map[string]any{
				"period":    period,
				"mux_costs": summary,
				"mux_total": total,
				"budget": map[string]any{
					"daily_usd":   d.Cfg.Costs.DailyBudgetUSD,
					"monthly_usd": d.Cfg.Costs.MonthlyBudgetUSD,
				},
			}

			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// get_agent_costs — reads directly from agent's costs.jsonl
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
			agentName, err := stringArg(args, "agent")
			if err != nil {
				return "", err
			}
			period, err := stringArg(args, "period")
			if err != nil {
				return "", err
			}

			dir := agent.Dir(d.Home, agentName)

			var since time.Time
			now := time.Now().UTC()
			switch period {
			case "today":
				since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			case "week":
				since = now.AddDate(0, 0, -7)
			case "month":
				since = now.AddDate(0, -1, 0)
			default:
				// all
			}

			entries, err := agent.ReadCosts(dir, since)
			if err != nil {
				return "", err
			}

			summary := agent.SummarizeCosts(entries)
			data, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	)

	// set_budget
	r.Register(
		Definition{
			Name:        "set_budget",
			Description: "Set a budget limit. Scope: 'total' or agent name. Period: 'daily' or 'monthly'.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"scope":     map[string]any{"type": "string", "description": "Budget scope: 'total' or agent name"},
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
				d.Cfg.Costs.DailyBudgetUSD = limitUSD
				fmt.Fprintf(&result, "Daily budget set to $%.2f", limitUSD)
			case scope == "total" && period == "monthly":
				d.Cfg.Costs.MonthlyBudgetUSD = limitUSD
				fmt.Fprintf(&result, "Monthly budget set to $%.2f", limitUSD)
			case period == "daily":
				d.Cfg.Costs.PerAgentDailyLimit = limitUSD
				fmt.Fprintf(&result, "Per-agent daily limit set to $%.2f (scope: %s)", limitUSD, scope)
			default:
				return "", fmt.Errorf("unsupported scope/period combination: %s/%s", scope, period)
			}

			if err := d.Cfg.Save(d.CfgPath); err != nil {
				return "", fmt.Errorf("failed to save config: %w", err)
			}
			return result.String(), nil
		},
	)
}

func periodToRange(period string) (time.Time, time.Time, error) {
	now := time.Now()
	end := now

	switch period {
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return start, end, nil
	case "week":
		return now.AddDate(0, 0, -7), end, nil
	case "month":
		return now.AddDate(0, -1, 0), end, nil
	case "all":
		return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), end, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid period: %s", period)
	}
}
