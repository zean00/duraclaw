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

type ReminderUpdateStore interface {
	UpdateUserReminderSubscription(ctx context.Context, id, customerID, userID string, update db.ReminderSubscriptionUpdate) (*db.ReminderSubscription, error)
}

type CreateReminderTool struct {
	Store ReminderStore
}

func (CreateReminderTool) Name() string { return "create_reminder" }

func (CreateReminderTool) Description() string {
	return "Create a scheduled reminder/alarm for the current user and return a reminder reference artifact. Use this for 'remind me', 'ingatkan saya', alarms, cron-like notifications, recurring reminders, and future scheduled tasks only when the user explicitly asks to be reminded/notified and the reminder has a clear future date/time or cron schedule. For fixed-interval bounded reminders such as 'every 8 hours for 3 days starting today at 8am', set schedule to @interval, set next_run_at to the first absolute RFC3339 fire time, set repeat_interval_seconds, and set repeat_until or repeat_count. For relative dates such as tomorrow/besok, first use trusted runtime time or duraclaw.current_time with the user's timezone, then pass an absolute RFC3339 next_run_at. Do not use this for generic notes, ideas, bookmarks, todo lists, or unscheduled tasks; use a customer notes/todo tool when available. Do not assume ambiguous times such as 'later', 'tomorrow', or 'morning' without enough context; ask the user for clarification first. If a recent reminder_reference exists and the user adds or corrects timing/details, use update_reminder instead of creating a duplicate. Do not use remember for reminders."
}

func (CreateReminderTool) Retryable() bool { return false }

func (CreateReminderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":                   map[string]any{"type": "string", "description": "Human-readable reminder text, for example 'bawa tas hitam ke sekolah anak'."},
			"schedule":                map[string]any{"type": "string", "description": "Cron expression, @once, or @interval. Use @once for a single future reminder. Use @interval with repeat_interval_seconds for fixed-interval recurring reminders."},
			"timezone":                map[string]any{"type": "string", "description": "User timezone when known, for example Asia/Jakarta."},
			"payload":                 map[string]any{"type": "object", "description": "Optional run input for the future reminder. Omit when title is enough; the scheduler will use the title as reminder text."},
			"next_run_at":             map[string]any{"type": "string", "description": "RFC3339 due time. Required for @once; optional for cron schedules. Example: tomorrow 7 AM converted to an absolute timestamp."},
			"repeat_interval_seconds": map[string]any{"type": "integer", "description": "Optional fixed repeat interval in seconds for @interval reminders, for example 28800 for every 8 hours."},
			"repeat_until":            map[string]any{"type": "string", "description": "Optional RFC3339 stop boundary for bounded recurring reminders. The reminder will not be scheduled again after this time."},
			"repeat_count":            map[string]any{"type": "integer", "description": "Optional maximum number of firings for bounded recurring reminders. 0 means no count limit."},
			"metadata":                map[string]any{"type": "object"},
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
	repeatIntervalSeconds := intArg(args, "repeat_interval_seconds")
	repeatUntil, err := parseOptionalRepeatUntil(args, "repeat_until")
	if err != nil {
		return ErrorResult(err.Error())
	}
	repeatCount := intArg(args, "repeat_count")
	nextRunAt, err := reminderNextRunAt(scheduleText, stringArg(args, "next_run_at"), repeatIntervalSeconds)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if repeatIntervalSeconds < 0 || repeatCount < 0 {
		return ErrorResult("repeat_interval_seconds and repeat_count must be non-negative")
	}
	if repeatUntil != nil && !repeatUntil.After(nextRunAt) {
		return ErrorResult("repeat_until must be after next_run_at")
	}
	if !nextRunAt.IsZero() && !nextRunAt.After(time.Now()) {
		return ErrorResult("next_run_at must be in the future. Ask the user for a future reminder time or retry with a future absolute timestamp.")
	}
	payload := objectArg(args, "payload")
	metadata := objectArg(args, "metadata")
	metadata["created_by"] = "create_reminder"
	metadata["run_id"] = exec.RunID
	sub, err := t.Store.CreateReminderSubscription(ctx, db.ReminderSubscriptionSpec{
		CustomerID:            exec.CustomerID,
		UserID:                exec.UserID,
		SessionID:             exec.SessionID,
		AgentInstanceID:       exec.AgentInstanceID,
		Title:                 stringArg(args, "title"),
		Schedule:              scheduleText,
		Timezone:              stringArg(args, "timezone"),
		Payload:               payload,
		NextRunAt:             nextRunAt,
		RepeatIntervalSeconds: repeatIntervalSeconds,
		RepeatUntil:           repeatUntil,
		RepeatCount:           repeatCount,
		Metadata:              metadata,
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

type UpdateReminderTool struct {
	Store ReminderUpdateStore
}

func (UpdateReminderTool) Name() string { return "update_reminder" }

func (UpdateReminderTool) Description() string {
	return "Update an existing reminder subscription and return the updated reminder reference artifact. Use this when the user corrects, adds timing, or changes details for a recent reminder_reference, especially in short follow-up messages like 'at 8am'. Requires subscription_id from an existing reminder_reference. Do not create a new reminder when updating the referenced reminder satisfies the request."
}

func (UpdateReminderTool) Retryable() bool { return false }

func (UpdateReminderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subscription_id":         map[string]any{"type": "string", "description": "Existing reminder subscription_id from a reminder_reference artifact."},
			"title":                   map[string]any{"type": "string", "description": "Updated human-readable reminder text."},
			"schedule":                map[string]any{"type": "string", "description": "Updated cron expression, @once, or @interval."},
			"timezone":                map[string]any{"type": "string", "description": "Updated user timezone when known, for example Asia/Jakarta."},
			"payload":                 map[string]any{"type": "object", "description": "Updated future reminder run input."},
			"next_run_at":             map[string]any{"type": "string", "description": "Updated RFC3339 due time. Required when schedule is @once."},
			"repeat_interval_seconds": map[string]any{"type": "integer", "description": "Updated fixed repeat interval in seconds for @interval reminders. Must be positive while schedule is @interval."},
			"repeat_until":            map[string]any{"type": "string", "description": "Updated RFC3339 stop boundary for bounded recurring reminders."},
			"repeat_count":            map[string]any{"type": "integer", "description": "Updated maximum number of firings. 0 means no count limit."},
			"metadata":                map[string]any{"type": "object"},
			"enabled":                 map[string]any{"type": "boolean"},
		},
		"required":             []any{"subscription_id"},
		"additionalProperties": false,
	}
}

func (t UpdateReminderTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("reminder store is unavailable")
	}
	subscriptionID := strings.TrimSpace(stringArg(args, "subscription_id"))
	if subscriptionID == "" {
		return ErrorResult("subscription_id is required")
	}
	update := db.ReminderSubscriptionUpdate{}
	if _, ok := args["repeat_interval_seconds"]; ok {
		value := intArg(args, "repeat_interval_seconds")
		if value < 0 {
			return ErrorResult("repeat_interval_seconds must be non-negative")
		}
		update.RepeatIntervalSeconds = &value
	}
	if _, ok := args["repeat_count"]; ok {
		value := intArg(args, "repeat_count")
		if value < 0 {
			return ErrorResult("repeat_count must be non-negative")
		}
		update.RepeatCount = &value
	}
	if _, ok := args["repeat_until"]; ok {
		repeatUntil, err := parseOptionalRepeatUntil(args, "repeat_until")
		if err != nil {
			return ErrorResult(err.Error())
		}
		update.RepeatUntil = repeatUntil
	}
	if value := strings.TrimSpace(stringArg(args, "title")); value != "" {
		update.Title = &value
	}
	if value := strings.TrimSpace(stringArg(args, "schedule")); value != "" {
		update.Schedule = &value
		if value == "@interval" && (update.RepeatIntervalSeconds == nil || *update.RepeatIntervalSeconds <= 0) {
			return ErrorResult("repeat_interval_seconds is required when updating schedule to @interval")
		}
		repeatInterval := 0
		if update.RepeatIntervalSeconds != nil {
			repeatInterval = *update.RepeatIntervalSeconds
		}
		nextRunAt, err := reminderNextRunAt(value, stringArg(args, "next_run_at"), repeatInterval)
		if err != nil {
			return ErrorResult(err.Error())
		}
		if !nextRunAt.IsZero() {
			update.NextRunAt = &nextRunAt
		}
	} else if rawNext := strings.TrimSpace(stringArg(args, "next_run_at")); rawNext != "" {
		nextRunAt, err := parseFutureReminderTime(rawNext)
		if err != nil {
			return ErrorResult(err.Error())
		}
		update.NextRunAt = &nextRunAt
	}
	if value := strings.TrimSpace(stringArg(args, "timezone")); value != "" {
		update.Timezone = &value
	}
	if payload, ok := args["payload"]; ok {
		update.Payload = payload
	}
	if metadata, ok := args["metadata"]; ok {
		update.Metadata = metadata
	}
	if enabled, ok := args["enabled"].(bool); ok {
		update.Enabled = &enabled
	}
	if update.RepeatUntil != nil && update.NextRunAt != nil && !update.RepeatUntil.After(*update.NextRunAt) {
		return ErrorResult("repeat_until must be after next_run_at")
	}
	sub, err := t.Store.UpdateUserReminderSubscription(ctx, subscriptionID, exec.CustomerID, exec.UserID, update)
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref := reminderReference(exec, sub)
	raw, _ := json.Marshal(map[string]any{
		"status":                 "updated",
		"reminder_reference":     ref,
		"user_response_guidance": "If this update happened while refining an undelivered draft response, tell the user the reminder has been set/scheduled with the latest details. Do not mention that an earlier hidden reminder was updated unless the user explicitly asks.",
	})
	return &Result{
		ForLLM:    string(raw),
		Artifacts: []Reference{ref},
	}
}

func reminderNextRunAt(scheduleText, rawNext string, repeatIntervalSeconds int) (time.Time, error) {
	rawNext = strings.TrimSpace(rawNext)
	if rawNext != "" {
		return parseFutureReminderTime(rawNext)
	}
	if scheduleText == "@once" {
		return time.Time{}, fmt.Errorf("next_run_at is required for @once reminders")
	}
	if scheduleText == "@interval" {
		if repeatIntervalSeconds <= 0 {
			return time.Time{}, fmt.Errorf("repeat_interval_seconds is required for @interval reminders")
		}
		return time.Time{}, fmt.Errorf("next_run_at is required for @interval reminders")
	}
	return scheduler.Next(scheduleText, time.Now().UTC())
}

func parseOptionalRepeatUntil(args map[string]any, key string) (*time.Time, error) {
	raw := strings.TrimSpace(stringArg(args, key))
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339: %w", key, err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func parseFutureReminderTime(rawNext string) (time.Time, error) {
	next, err := time.Parse(time.RFC3339, strings.TrimSpace(rawNext))
	if err != nil {
		return time.Time{}, fmt.Errorf("next_run_at must be RFC3339: %w", err)
	}
	next = next.UTC()
	if !next.After(time.Now()) {
		return time.Time{}, fmt.Errorf("next_run_at must be in the future. Ask the user for a future reminder time or retry with a future absolute timestamp")
	}
	return next, nil
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
	if sub.RepeatIntervalSeconds > 0 {
		data["repeat_interval_seconds"] = sub.RepeatIntervalSeconds
	}
	if sub.RepeatUntil != nil {
		data["repeat_until"] = sub.RepeatUntil.UTC().Format(time.RFC3339)
	}
	if sub.RepeatCount > 0 {
		data["repeat_count"] = sub.RepeatCount
	}
	if sub.FiredCount > 0 {
		data["fired_count"] = sub.FiredCount
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
