package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/scheduler"
)

type ReminderStore interface {
	CreateReminderSubscription(ctx context.Context, spec db.ReminderSubscriptionSpec) (*db.ReminderSubscription, error)
}

type CreateReminderTool struct {
	Store ReminderStore
}

func (CreateReminderTool) Name() string { return "create_reminder" }

func (CreateReminderTool) Description() string {
	return "Create a scheduled reminder/alarm for the current user and return a reminder reference artifact. Use this for 'remind me', 'ingatkan saya', 'besok pagi jam 7', alarms, cron-like notifications, and future scheduled tasks. Do not use remember for these."
}

func (CreateReminderTool) Retryable() bool { return false }

func (CreateReminderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":       map[string]any{"type": "string", "description": "Human-readable reminder text, for example 'bawa tas hitam ke sekolah anak'."},
			"schedule":    map[string]any{"type": "string", "description": "Cron expression or @once. Use @once for a single future reminder."},
			"timezone":    map[string]any{"type": "string", "description": "User timezone when known, for example Asia/Jakarta."},
			"payload":     map[string]any{"type": "object", "description": "Optional run input for the future reminder. Omit when title is enough; the scheduler will use the title as reminder text."},
			"next_run_at": map[string]any{"type": "string", "description": "RFC3339 due time. Required for @once; optional for cron schedules. Example: tomorrow 7 AM converted to an absolute timestamp."},
			"metadata":    map[string]any{"type": "object"},
		},
		"required":             []any{"schedule"},
		"additionalProperties": false,
	}
}

func (t CreateReminderTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("reminder store is unavailable")
	}
	scheduleText := strings.TrimSpace(stringArg(args, "schedule"))
	if scheduleText == "" {
		return ErrorResult("schedule is required")
	}
	nextRunAt, err := reminderNextRunAt(scheduleText, stringArg(args, "next_run_at"))
	if err != nil {
		return ErrorResult(err.Error())
	}
	if !nextRunAt.IsZero() && !nextRunAt.After(time.Now()) {
		return ErrorResult("next_run_at must be in the future. Ask the user for a future reminder time or retry with a future absolute timestamp.")
	}
	payload := objectArg(args, "payload")
	metadata := objectArg(args, "metadata")
	metadata["created_by"] = "create_reminder"
	metadata["run_id"] = exec.RunID
	sub, err := t.Store.CreateReminderSubscription(ctx, db.ReminderSubscriptionSpec{
		CustomerID:      exec.CustomerID,
		UserID:          exec.UserID,
		SessionID:       exec.SessionID,
		AgentInstanceID: exec.AgentInstanceID,
		Title:           stringArg(args, "title"),
		Schedule:        scheduleText,
		Timezone:        stringArg(args, "timezone"),
		Payload:         payload,
		NextRunAt:       nextRunAt,
		Metadata:        metadata,
	})
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref := reminderReference(exec, sub)
	raw, _ := json.Marshal(map[string]any{
		"status":             "created",
		"reminder_reference": ref,
	})
	return &Result{
		ForLLM:    string(raw),
		Artifacts: []Reference{ref},
	}
}

func reminderNextRunAt(scheduleText, rawNext string) (time.Time, error) {
	rawNext = strings.TrimSpace(rawNext)
	if rawNext != "" {
		next, err := time.Parse(time.RFC3339, rawNext)
		if err != nil {
			return time.Time{}, fmt.Errorf("next_run_at must be RFC3339: %w", err)
		}
		return next.UTC(), nil
	}
	if scheduleText == "@once" {
		return time.Time{}, fmt.Errorf("next_run_at is required for @once reminders")
	}
	return scheduler.Next(scheduleText, time.Now().UTC())
}

func reminderReference(exec ExecutionContext, sub *db.ReminderSubscription) Reference {
	data := map[string]any{
		"reference_type":     "reminder_subscription",
		"subscription_id":    sub.ID,
		"customer_id":        sub.CustomerID,
		"user_id":            sub.UserID,
		"session_id":         sub.SessionID,
		"agent_instance_id":  sub.AgentInstanceID,
		"title":              sub.Title,
		"schedule":           sub.Schedule,
		"timezone":           sub.Timezone,
		"enabled":            sub.Enabled,
		"next_run_at":        sub.NextRunAt.UTC().Format(time.RFC3339),
		"pause_api":          "PATCH /acp/reminders/" + sub.ID,
		"resume_api":         "PATCH /acp/reminders/" + sub.ID,
		"delete_api":         "DELETE /acp/reminders/" + sub.ID,
		"admin_pause_api":    "PATCH /admin/reminders/subscriptions/" + sub.ID,
		"admin_resume_api":   "PATCH /admin/reminders/subscriptions/" + sub.ID,
		"current_request_id": exec.RequestID,
	}
	return Reference{Type: "reminder_reference", ID: sub.ID, Data: data}
}

func objectArg(args map[string]any, key string) map[string]any {
	out := map[string]any{}
	raw, ok := args[key]
	if !ok || raw == nil {
		return out
	}
	if m, ok := raw.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
