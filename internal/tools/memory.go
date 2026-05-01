package tools

import (
	"context"
	"duraclaw/internal/db"
	"fmt"
	"strings"
)

type MemoryStore interface {
	AddMemory(ctx context.Context, customerID, userID, sessionID, memoryType, content string, metadata any) (string, error)
	ListMemories(ctx context.Context, customerID, userID string, limit int) ([]db.Memory, error)
	AddPreference(ctx context.Context, customerID, userID, sessionID, category, content string, condition, metadata any) (string, error)
	ListPreferences(ctx context.Context, customerID, userID string, limit int) ([]db.Preference, error)
}

type RememberTool struct {
	Store MemoryStore
}

func (RememberTool) Name() string { return "remember" }
func (RememberTool) Description() string {
	return "Persist a stable user fact for future context, such as family, work, home, or facts that rarely change. Do not use for reminders, alarms, scheduled tasks, or requests like 'ingatkan saya'; use create_reminder instead."
}
func (RememberTool) Retryable() bool { return false }
func (RememberTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"type":    map[string]any{"type": "string", "description": "Stable memory type, usually fact."},
			"content": map[string]any{"type": "string", "description": "Stable fact to remember permanently. Not a reminder request or scheduled task."},
		},
		"required":             []any{"content"},
		"additionalProperties": false,
	}
}

func (t RememberTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("memory store is unavailable")
	}
	memoryType, _ := args["type"].(string)
	if strings.TrimSpace(memoryType) == "" {
		memoryType = "fact"
	}
	content, _ := args["content"].(string)
	id, err := t.Store.AddMemory(ctx, exec.CustomerID, exec.UserID, exec.SessionID, memoryType, content, map[string]any{"run_id": exec.RunID})
	if err != nil {
		return ErrorResult(err.Error())
	}
	return NewResult("memory saved: " + id)
}

type SavePreferenceTool struct {
	Store MemoryStore
}

func (SavePreferenceTool) Name() string { return "save_preference" }
func (SavePreferenceTool) Description() string {
	return "Persist a conditional user preference, such as preferences that apply only in a season, context, location, or situation."
}
func (SavePreferenceTool) Retryable() bool { return false }
func (SavePreferenceTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"category":  map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
			"condition": map[string]any{"type": "object"},
		},
		"required":             []any{"content"},
		"additionalProperties": false,
	}
}

func (t SavePreferenceTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("preference store is unavailable")
	}
	category, _ := args["category"].(string)
	if strings.TrimSpace(category) == "" {
		category = "general"
	}
	content, _ := args["content"].(string)
	condition, _ := args["condition"].(map[string]any)
	id, err := t.Store.AddPreference(ctx, exec.CustomerID, exec.UserID, exec.SessionID, category, content, condition, map[string]any{"run_id": exec.RunID})
	if err != nil {
		return ErrorResult(err.Error())
	}
	return NewResult("preference saved: " + id)
}

type ListMemoriesTool struct {
	Store MemoryStore
}

func (ListMemoriesTool) Name() string { return "list_memories" }
func (ListMemoriesTool) Description() string {
	return "List recent stable factual memories for the current user."
}
func (ListMemoriesTool) Retryable() bool { return true }
func (ListMemoriesTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
		},
		"additionalProperties": false,
	}
}

type ListPreferencesTool struct {
	Store MemoryStore
}

func (ListPreferencesTool) Name() string { return "list_preferences" }
func (ListPreferencesTool) Description() string {
	return "List recent conditional preferences for the current user."
}
func (ListPreferencesTool) Retryable() bool { return true }
func (ListPreferencesTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
		},
		"additionalProperties": false,
	}
}

func (t ListPreferencesTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("preference store is unavailable")
	}
	limit := 10
	if raw, ok := args["limit"]; ok {
		switch v := raw.(type) {
		case int:
			limit = v
		case int64:
			limit = int(v)
		case float64:
			limit = int(v)
		}
	}
	preferences, err := t.Store.ListPreferences(ctx, exec.CustomerID, exec.UserID, limit)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if len(preferences) == 0 {
		return NewResult("no preferences found")
	}
	var b strings.Builder
	for _, p := range preferences {
		condition := strings.TrimSpace(string(p.Condition))
		if condition == "" || condition == "null" {
			condition = "{}"
		}
		fmt.Fprintf(&b, "- %s: %s when %s\n", p.Category, p.Content, condition)
	}
	return NewResult(strings.TrimSpace(b.String()))
}

func (t ListMemoriesTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	if t.Store == nil {
		return ErrorResult("memory store is unavailable")
	}
	limit := 10
	if raw, ok := args["limit"]; ok {
		switch v := raw.(type) {
		case int:
			limit = v
		case int64:
			limit = int(v)
		case float64:
			limit = int(v)
		}
	}
	memories, err := t.Store.ListMemories(ctx, exec.CustomerID, exec.UserID, limit)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if len(memories) == 0 {
		return NewResult("no memories found")
	}
	var b strings.Builder
	for _, m := range memories {
		fmt.Fprintf(&b, "- %s: %s\n", m.Type, m.Content)
	}
	return NewResult(strings.TrimSpace(b.String()))
}
