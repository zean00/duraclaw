package tools

import (
	"context"
	"duraclaw/internal/db"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
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
	return "Persist a stable user fact for future context, such as family, work, home, or facts that rarely change. Do not use for reminders, alarms, scheduled tasks, or requests like 'ingatkan saya'; use create_reminder instead. Do not use for generic notes, ideas, bookmarks, todo lists, or unscheduled tasks; use a customer notes/todo tool when available."
}
func (RememberTool) Retryable() bool { return false }
func (RememberTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"type":    map[string]any{"type": "string", "description": "Stable memory type, usually fact."},
			"content": map[string]any{"type": "string", "description": "Stable fact to remember permanently. Not a reminder request, scheduled task, note, idea, bookmark, or todo item."},
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
	if isReminderLikeMemory(memoryType, content) {
		return ErrorResult("remember is only for stable user facts. Use create_reminder for reminder, alarm, and scheduled notification requests.")
	}
	if isCaptureLikeMemory(memoryType, content) {
		return ErrorResult("remember is only for stable user facts. Use the customer's notes/todo/capture tool for notes, ideas, bookmarks, todo lists, and unscheduled tasks when available.")
	}
	id, err := t.Store.AddMemory(ctx, exec.CustomerID, exec.UserID, exec.SessionID, memoryType, content, map[string]any{"run_id": exec.RunID})
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref := memoryReference(exec, id, memoryType, content)
	raw, _ := json.Marshal(map[string]any{
		"status":           "created",
		"memory_reference": ref,
	})
	return &Result{ForLLM: string(raw), Artifacts: []Reference{ref}}
}

func isReminderLikeMemory(memoryType, content string) bool {
	text := strings.ToLower(strings.TrimSpace(memoryType + " " + content))
	for _, token := range []string{"reminder", "pengingat", "ingatkan", "remind me", "alarm", "scheduled task", "notifikasi terjadwal"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func isCaptureLikeMemory(memoryType, content string) bool {
	text := strings.TrimSpace(memoryType + " " + content)
	lowerText := strings.ToLower(text)
	for _, token := range []string{"note", "notes", "catatan", "catat", "catet", "bookmark", "todo", "idea"} {
		if containsWholeWord(lowerText, token) {
			return true
		}
	}
	if containsWholeWord(lowerText, "to-do") || strings.Contains(lowerText, "daftar tugas") {
		return true
	}
	if containsWholeWord(lowerText, "ide") && !containsWholeWord(text, "IDE") {
		return true
	}
	if isOneOffCaptureLikeContent(lowerText) {
		return true
	}
	return false
}

func isOneOffCaptureLikeContent(lowerText string) bool {
	if strings.Contains(lowerText, "http://") || strings.Contains(lowerText, "https://") || strings.Contains(lowerText, "www.") || strings.Contains(lowerText, "github.com") {
		return true
	}
	for _, token := range []string{"repo", "repository", "link", "url", "product", "produk"} {
		if containsWholeWord(lowerText, token) {
			return true
		}
	}
	if !containsAnyWholeWord(lowerText, []string{"place", "places", "tempat", "location", "lokasi", "address", "alamat"}) {
		return false
	}
	return strings.Contains(lowerText, "namanya") || strings.Contains(lowerText, "nama ") || strings.Contains(lowerText, "jalan ") || strings.Contains(lowerText, "jl.")
}

func containsAnyWholeWord(text string, words []string) bool {
	for _, word := range words {
		if containsWholeWord(text, word) {
			return true
		}
	}
	return false
}

func containsWholeWord(text, word string) bool {
	if text == "" || word == "" {
		return false
	}
	start := 0
	for start < len(text) {
		idx := strings.Index(text[start:], word)
		if idx < 0 {
			return false
		}
		idx += start
		end := idx + len(word)
		if isWordBoundary(text, idx-1) && isWordBoundary(text, end) {
			return true
		}
		start = end
	}
	return false
}

func isWordBoundary(text string, idx int) bool {
	if idx < 0 || idx >= len(text) {
		return true
	}
	if idx > 0 && !utf8.RuneStart(text[idx]) {
		r, _ := utf8.DecodeLastRuneInString(text[:idx+1])
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}
	r, _ := utf8.DecodeRuneInString(text[idx:])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

type SavePreferenceTool struct {
	Store MemoryStore
}

func (SavePreferenceTool) Name() string { return "save_preference" }
func (SavePreferenceTool) Description() string {
	return "Persist a user preference and return a preference reference artifact. Use this only for preferences, choices, response style, format, habits, likes/dislikes, or conditional preferences about how the user wants to be helped. Do not use for generic notes, ideas, bookmarks, todo lists, unscheduled tasks, or place/product/link notes; use a customer notes/todo/capture tool when available. Do not merely say the preference was saved; call this tool so the response can include a preference_reference artifact."
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
	if isCaptureLikeMemory(category, content) {
		return ErrorResult("save_preference is only for user preferences. Use the customer's notes/todo/capture tool for notes, ideas, bookmarks, todo lists, place notes, product notes, links, and unscheduled tasks when available.")
	}
	condition, _ := args["condition"].(map[string]any)
	id, err := t.Store.AddPreference(ctx, exec.CustomerID, exec.UserID, exec.SessionID, category, content, condition, map[string]any{"run_id": exec.RunID})
	if err != nil {
		return ErrorResult(err.Error())
	}
	ref := preferenceReference(exec, id, category, content, condition)
	raw, _ := json.Marshal(map[string]any{
		"status":               "created",
		"preference_reference": ref,
	})
	return &Result{ForLLM: string(raw), Artifacts: []Reference{ref}}
}

func memoryReference(exec ExecutionContext, id, memoryType, content string) Reference {
	return Reference{
		Type: "memory_reference",
		ID:   id,
		Data: map[string]any{
			"reference_type":     "memory",
			"memory_id":          id,
			"customer_id":        exec.CustomerID,
			"user_id":            exec.UserID,
			"session_id":         exec.SessionID,
			"memory_type":        memoryType,
			"content":            content,
			"update_api":         "PUT /admin/memories/" + id,
			"delete_api":         "DELETE /admin/memories/" + id,
			"current_request_id": exec.RequestID,
		},
	}
}

func preferenceReference(exec ExecutionContext, id, category, content string, condition map[string]any) Reference {
	data := map[string]any{
		"reference_type":     "preference",
		"preference_id":      id,
		"customer_id":        exec.CustomerID,
		"user_id":            exec.UserID,
		"session_id":         exec.SessionID,
		"category":           category,
		"content":            content,
		"update_api":         "PUT /admin/preferences/" + id,
		"delete_api":         "DELETE /admin/preferences/" + id,
		"current_request_id": exec.RequestID,
	}
	if len(condition) > 0 {
		data["condition"] = condition
	}
	return Reference{Type: "preference_reference", ID: id, Data: data}
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
