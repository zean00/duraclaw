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
}

type RememberTool struct {
	Store MemoryStore
}

func (RememberTool) Name() string { return "remember" }
func (RememberTool) Description() string {
	return "Persist a user memory or preference for future context."
}
func (RememberTool) Retryable() bool { return false }
func (RememberTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"type":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
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
		memoryType = "preference"
	}
	content, _ := args["content"].(string)
	id, err := t.Store.AddMemory(ctx, exec.CustomerID, exec.UserID, exec.SessionID, memoryType, content, map[string]any{"run_id": exec.RunID})
	if err != nil {
		return ErrorResult(err.Error())
	}
	return NewResult("memory saved: " + id)
}

type ListMemoriesTool struct {
	Store MemoryStore
}

func (ListMemoriesTool) Name() string { return "list_memories" }
func (ListMemoriesTool) Description() string {
	return "List recent stored memories for the current user."
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
