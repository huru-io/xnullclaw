package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/memory"
)

func TestGetCosts_Today(t *testing.T) {
	d, _ := newTestDeps(t)

	// Add a cost entry so there's data.
	d.Store.AddCost(memory.Cost{
		Category: "loop",
		CostUSD:  0.50,
	})

	r := NewRegistry()
	registerCostTools(r, d)

	result, err := r.Execute(context.Background(), "get_costs", map[string]any{"period": "today"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nresult: %s", err, result)
	}
	if _, ok := parsed["period"]; !ok {
		t.Error("expected 'period' field in result")
	}
	if _, ok := parsed["mux_costs"]; !ok {
		t.Error("expected 'mux_costs' field in result")
	}
	if _, ok := parsed["budget"]; !ok {
		t.Error("expected 'budget' field in result")
	}
}

func TestGetCosts_AllPeriods(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	for _, period := range []string{"today", "week", "month", "all"} {
		result, err := r.Execute(context.Background(), "get_costs", map[string]any{"period": period})
		if err != nil {
			t.Errorf("period %q: unexpected error: %v", period, err)
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Errorf("period %q: invalid JSON: %v", period, err)
		}
	}
}

func TestGetCosts_InvalidPeriod(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	_, err := r.Execute(context.Background(), "get_costs", map[string]any{"period": "yesterday"})
	if err == nil {
		t.Fatal("expected error for invalid period")
	}
	if !strings.Contains(err.Error(), "invalid period") {
		t.Errorf("expected 'invalid period' in error, got: %v", err)
	}
}

func TestSetBudget_Daily(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	result, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": 10.0,
		"period":    "daily",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Daily budget set to $10.00") {
		t.Errorf("unexpected result: %q", result)
	}
	if d.Cfg.Costs.DailyBudgetUSD != 10.0 {
		t.Errorf("expected DailyBudgetUSD=10.0, got %f", d.Cfg.Costs.DailyBudgetUSD)
	}
}

func TestSetBudget_Monthly(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	result, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": 100.0,
		"period":    "monthly",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Monthly budget set to $100.00") {
		t.Errorf("unexpected result: %q", result)
	}
	if d.Cfg.Costs.MonthlyBudgetUSD != 100.0 {
		t.Errorf("expected MonthlyBudgetUSD=100.0, got %f", d.Cfg.Costs.MonthlyBudgetUSD)
	}
}

func TestSetBudget_InvalidScope(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	_, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "alice",
		"limit_usd": 5.0,
		"period":    "daily",
	})
	if err == nil {
		t.Fatal("expected error for non-total scope")
	}
	if !strings.Contains(err.Error(), "only scope 'total'") {
		t.Errorf("expected scope error, got: %v", err)
	}
}

func TestSetBudget_NegativeLimit(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	_, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": -5.0,
		"period":    "daily",
	})
	if err == nil {
		t.Fatal("expected error for negative limit")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Errorf("expected 'non-negative' in error, got: %v", err)
	}
}

func TestSetBudget_InvalidPeriod(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	_, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": 5.0,
		"period":    "yearly",
	})
	if err == nil {
		t.Fatal("expected error for invalid period")
	}
	if !strings.Contains(err.Error(), "unsupported period") {
		t.Errorf("expected 'unsupported period' in error, got: %v", err)
	}
}

func TestPeriodToRange_AllPeriods(t *testing.T) {
	tests := []struct {
		period string
		wantOK bool
	}{
		{"today", true},
		{"week", true},
		{"month", true},
		{"all", true},
		{"bad", false},
	}

	for _, tc := range tests {
		start, end, err := periodToRange(tc.period)
		if tc.wantOK {
			if err != nil {
				t.Errorf("period %q: unexpected error: %v", tc.period, err)
				continue
			}
			if !start.Before(end) {
				t.Errorf("period %q: start (%v) not before end (%v)", tc.period, start, end)
			}
		} else {
			if err == nil {
				t.Errorf("period %q: expected error", tc.period)
			}
		}
	}
}

func TestPeriodToRange_TodayStartIsMidnight(t *testing.T) {
	start, _, err := periodToRange("today")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now().UTC()
	expected := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !start.Equal(expected) {
		t.Errorf("today start: got %v, want %v", start, expected)
	}
}

func TestSetBudget_ConfigSaved(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerCostTools(r, d)

	_, err := r.Execute(context.Background(), "set_budget", map[string]any{
		"scope":     "total",
		"limit_usd": 25.0,
		"period":    "daily",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the in-memory value was updated.
	if d.Cfg.Costs.DailyBudgetUSD != 25.0 {
		t.Errorf("expected DailyBudgetUSD=25.0, got %f", d.Cfg.Costs.DailyBudgetUSD)
	}
}
