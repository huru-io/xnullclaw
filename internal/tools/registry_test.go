package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// --- Registry tests ---

func TestRegistryRegisterAndExecute(t *testing.T) {
	r := NewRegistry()
	r.Register(
		Definition{Name: "greet", Description: "Say hello"},
		func(ctx context.Context, args map[string]any) (string, error) {
			name, _ := args["name"].(string)
			return "Hello, " + name + "!", nil
		},
	)

	result, err := r.Execute(context.Background(), "greet", map[string]any{"name": "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, Alice!" {
		t.Errorf("got %q, want %q", result, "Hello, Alice!")
	}
}

func TestRegistryUnknownTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestRegistryDefinitionsReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(
		Definition{Name: "tool1", Description: "first"},
		func(ctx context.Context, args map[string]any) (string, error) { return "", nil },
	)

	defs := r.Definitions()
	defs[0].Name = "tampered"

	// Original should be unchanged.
	if r.Definitions()[0].Name != "tool1" {
		t.Error("Definitions() should return a copy")
	}
}

func TestRegistryMultipleTools(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("tool_%d", i)
		r.Register(
			Definition{Name: name, Description: name},
			func(ctx context.Context, args map[string]any) (string, error) {
				return name, nil
			},
		)
	}
	if len(r.Definitions()) != 5 {
		t.Errorf("expected 5 definitions, got %d", len(r.Definitions()))
	}
}

// --- Arg extraction tests ---

func TestStringArg_Present(t *testing.T) {
	args := map[string]any{"name": "alice"}
	v, err := stringArg(args, "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "alice" {
		t.Errorf("got %q, want %q", v, "alice")
	}
}

func TestStringArg_Missing(t *testing.T) {
	args := map[string]any{}
	_, err := stringArg(args, "name")
	if err == nil {
		t.Fatal("expected error for missing arg")
	}
}

func TestStringArg_WrongType(t *testing.T) {
	args := map[string]any{"name": 42}
	_, err := stringArg(args, "name")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestFloat64Arg_Float(t *testing.T) {
	args := map[string]any{"val": 3.14}
	v, err := float64Arg(args, "val")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 3.14 {
		t.Errorf("got %f, want 3.14", v)
	}
}

func TestFloat64Arg_Int(t *testing.T) {
	args := map[string]any{"val": 42}
	v, err := float64Arg(args, "val")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 42 {
		t.Errorf("got %f, want 42", v)
	}
}

func TestFloat64Arg_JSONNumber(t *testing.T) {
	args := map[string]any{"val": json.Number("99.9")}
	v, err := float64Arg(args, "val")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 99.9 {
		t.Errorf("got %f, want 99.9", v)
	}
}

func TestFloat64Arg_Missing(t *testing.T) {
	args := map[string]any{}
	_, err := float64Arg(args, "val")
	if err == nil {
		t.Fatal("expected error for missing arg")
	}
}

func TestFloat64Arg_WrongType(t *testing.T) {
	args := map[string]any{"val": "not a number"}
	_, err := float64Arg(args, "val")
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestStringSliceArg_AnySlice(t *testing.T) {
	args := map[string]any{"names": []any{"alice", "bob"}}
	v, err := stringSliceArg(args, "names")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v) != 2 || v[0] != "alice" || v[1] != "bob" {
		t.Errorf("got %v", v)
	}
}

func TestStringSliceArg_StringSlice(t *testing.T) {
	args := map[string]any{"names": []string{"x", "y"}}
	v, err := stringSliceArg(args, "names")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v) != 2 {
		t.Errorf("got %v", v)
	}
}

func TestStringSliceArg_MixedTypes(t *testing.T) {
	args := map[string]any{"names": []any{"alice", 42}}
	_, err := stringSliceArg(args, "names")
	if err == nil {
		t.Fatal("expected error for mixed types")
	}
}

func TestStringSliceArg_Missing(t *testing.T) {
	args := map[string]any{}
	_, err := stringSliceArg(args, "names")
	if err == nil {
		t.Fatal("expected error for missing arg")
	}
}

func TestOptionalStringArg(t *testing.T) {
	args := map[string]any{"x": "hello"}
	if v := optionalStringArg(args, "x", "default"); v != "hello" {
		t.Errorf("present: got %q", v)
	}
	if v := optionalStringArg(args, "missing", "default"); v != "default" {
		t.Errorf("missing: got %q", v)
	}
	args2 := map[string]any{"x": 42}
	if v := optionalStringArg(args2, "x", "default"); v != "default" {
		t.Errorf("wrong type: got %q", v)
	}
}

func TestPeriodToRange_Valid(t *testing.T) {
	for _, period := range []string{"today", "week", "month", "all"} {
		start, end, err := periodToRange(period)
		if err != nil {
			t.Errorf("period %q: unexpected error: %v", period, err)
			continue
		}
		if !start.Before(end) {
			t.Errorf("period %q: start %v not before end %v", period, start, end)
		}
	}
}

func TestPeriodToRange_Today(t *testing.T) {
	start, _, err := periodToRange("today")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	now := time.Now()
	expected := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if !start.Equal(expected) {
		t.Errorf("today start: got %v, want %v", start, expected)
	}
}

func TestPeriodToRange_Invalid(t *testing.T) {
	_, _, err := periodToRange("invalid")
	if err == nil {
		t.Fatal("expected error for invalid period")
	}
}
