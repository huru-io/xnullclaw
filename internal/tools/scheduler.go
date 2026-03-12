package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/memory"
)

func registerSchedulerTools(r *Registry, store *memory.Store) {
	// schedule_task
	r.Register(
		Definition{
			Name:        "schedule_task",
			Description: "Schedule a task for the mux to execute at a later time. Use for reminders, check-ins, follow-ups, or any delayed action.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description": map[string]any{
						"type":        "string",
						"description": "What to do when the task fires (e.g. 'Check on alice's progress', 'Remind user about meeting')",
					},
					"trigger_at": map[string]any{
						"type":        "string",
						"description": "When to fire, in ISO 8601 format (e.g. '2026-03-12T15:00:00Z' or '2026-03-12T15:00:00-05:00')",
					},
					"context": map[string]any{
						"type":        "string",
						"description": "Optional JSON metadata (e.g. agent name, extra instructions)",
					},
				},
				"required": []string{"description", "trigger_at"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			desc, err := stringArg(args, "description")
			if err != nil {
				return "", err
			}
			triggerStr, err := stringArg(args, "trigger_at")
			if err != nil {
				return "", err
			}
			triggerAt, err := time.Parse(time.RFC3339, triggerStr)
			if err != nil {
				return "", fmt.Errorf("invalid trigger_at format (use ISO 8601): %w", err)
			}
			if triggerAt.Before(time.Now()) {
				return "", fmt.Errorf("trigger_at must be in the future")
			}

			task := memory.ScheduledTask{
				Description: desc,
				TriggerAt:   triggerAt,
			}
			if ctxStr := optionalStringArg(args, "context", ""); ctxStr != "" {
				task.Context = &ctxStr
			}

			id, err := store.AddScheduledTask(task)
			if err != nil {
				return "", fmt.Errorf("failed to schedule task: %w", err)
			}
			return fmt.Sprintf("Task #%d scheduled for %s: %s", id, triggerAt.Format(time.RFC3339), desc), nil
		},
	)

	// list_tasks
	r.Register(
		Definition{
			Name:        "list_tasks",
			Description: "List scheduled tasks. Filter by status or list all.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "Filter by status",
						"enum":        []string{"pending", "fired", "cancelled", "all"},
					},
				},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			status := optionalStringArg(args, "status", "pending")
			if status == "all" {
				status = ""
			}

			tasks, err := store.ListScheduledTasks(status)
			if err != nil {
				return "", fmt.Errorf("failed to list tasks: %w", err)
			}
			if len(tasks) == 0 {
				if status != "" {
					return fmt.Sprintf("No %s tasks.", status), nil
				}
				return "No scheduled tasks.", nil
			}

			now := time.Now()
			var sb strings.Builder
			for _, t := range tasks {
				eta := ""
				if t.Status == "pending" {
					d := t.TriggerAt.Sub(now)
					if d > 0 {
						eta = fmt.Sprintf(" (in %s)", d.Truncate(time.Second))
					} else {
						eta = " (overdue)"
					}
				}
				fmt.Fprintf(&sb, "#%d [%s] %s — %s%s\n",
					t.ID, t.Status, t.TriggerAt.Format("2006-01-02 15:04"), t.Description, eta)
			}
			return sb.String(), nil
		},
	)

	// cancel_task
	r.Register(
		Definition{
			Name:        "cancel_task",
			Description: "Cancel a pending scheduled task by its ID.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "number",
						"description": "The task ID to cancel",
					},
				},
				"required": []string{"task_id"},
			},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			idFloat, err := float64Arg(args, "task_id")
			if err != nil {
				return "", err
			}
			id := int(idFloat)

			if err := store.CancelScheduledTask(id); err != nil {
				return "", fmt.Errorf("failed to cancel task: %w", err)
			}
			return fmt.Sprintf("Task #%d cancelled.", id), nil
		},
	)
}
