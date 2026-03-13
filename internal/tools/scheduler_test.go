package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jotavich/xnullclaw/internal/memory"
)

func newTestStore(t *testing.T) *memory.Store {
	t.Helper()
	store, err := memory.New(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestScheduleTask_HappyPath(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	triggerAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	result, err := r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "Check on alice",
		"trigger_at":  triggerAt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Task #") {
		t.Errorf("expected 'Task #' in result, got: %s", result)
	}
	if !strings.Contains(result, "Check on alice") {
		t.Errorf("expected description in result, got: %s", result)
	}
}

func TestScheduleTask_PastTime(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	pastTime := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	_, err := r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "Too late",
		"trigger_at":  pastTime,
	})
	if err == nil {
		t.Fatal("expected error for past time")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("expected 'future' in error, got: %v", err)
	}
}

func TestScheduleTask_TooFar(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	farTime := time.Now().Add(31 * 24 * time.Hour).UTC().Format(time.RFC3339)
	_, err := r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "Way out",
		"trigger_at":  farTime,
	})
	if err == nil {
		t.Fatal("expected error for time too far out")
	}
	if !strings.Contains(err.Error(), "30 days") {
		t.Errorf("expected '30 days' in error, got: %v", err)
	}
}

func TestScheduleTask_MaxPending(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	// Fill up to the limit.
	for i := 0; i < memory.MaxPendingTasks; i++ {
		triggerAt := time.Now().Add(time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		_, err := r.Execute(context.Background(), "schedule_task", map[string]any{
			"description": "task",
			"trigger_at":  triggerAt,
		})
		if err != nil {
			t.Fatalf("failed to add task %d: %v", i, err)
		}
	}

	// 51st should fail.
	triggerAt := time.Now().Add(100 * time.Hour).UTC().Format(time.RFC3339)
	_, err := r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "overflow",
		"trigger_at":  triggerAt,
	})
	if err == nil {
		t.Fatal("expected error at max pending tasks")
	}
	if !strings.Contains(err.Error(), "too many pending") {
		t.Errorf("expected 'too many pending' in error, got: %v", err)
	}
}

func TestListTasks_Empty(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	result, err := r.Execute(context.Background(), "list_tasks", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No") {
		t.Errorf("expected 'No' in empty result, got: %s", result)
	}
}

func TestListTasks_WithTasks(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	// Schedule a task.
	triggerAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "Check alice",
		"trigger_at":  triggerAt,
	})

	result, err := r.Execute(context.Background(), "list_tasks", map[string]any{"status": "pending"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Check alice") {
		t.Errorf("expected task in list, got: %s", result)
	}
}

func TestCancelTask(t *testing.T) {
	store := newTestStore(t)
	r := NewRegistry()
	registerSchedulerTools(r, store)

	// Schedule then cancel.
	triggerAt := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	r.Execute(context.Background(), "schedule_task", map[string]any{
		"description": "To cancel",
		"trigger_at":  triggerAt,
	})

	result, err := r.Execute(context.Background(), "cancel_task", map[string]any{
		"task_id": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "cancelled") {
		t.Errorf("expected 'cancelled' in result, got: %s", result)
	}

	// Verify it's cancelled.
	listResult, _ := r.Execute(context.Background(), "list_tasks", map[string]any{"status": "cancelled"})
	if !strings.Contains(listResult, "To cancel") {
		t.Errorf("expected cancelled task in list, got: %s", listResult)
	}
}
