package tools

import (
	"context"
	"strings"
	"testing"
)

type testTool struct {
	name string
	fn   func()
}

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return "test" }
func (t testTool) Parameters() map[string]any {
	return map[string]any{
		"properties": map[string]any{"message": map[string]any{"type": "string"}},
		"required":   []any{"message"},
	}
}
func (t testTool) Execute(context.Context, ExecutionContext, map[string]any) *Result {
	if t.fn != nil {
		t.fn()
	}
	return NewResult("ok")
}

func TestRegistryOrdersToolsDeterministically(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "zeta"})
	r.Register(testTool{name: "alpha"})
	got := r.List()
	if got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("got %v", got)
	}
}

func TestRegistryConvertsToProviderDefs(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "echo"})
	defs := r.ToProviderDefs()
	if len(defs) != 1 {
		t.Fatalf("defs=%#v", defs)
	}
	if defs[0].Type != "function" || defs[0].Function.Name != "echo" {
		t.Fatalf("defs=%#v", defs)
	}
	if defs[0].Function.Parameters["type"] != "object" {
		t.Fatalf("provider tool parameters must include JSON Schema object type: %#v", defs[0].Function.Parameters)
	}
}

func TestRegistryConvertsNilParametersToObjectSchema(t *testing.T) {
	r := NewRegistry()
	r.Register(nilTool{name: "nil"})
	defs := r.ToProviderDefs()
	if len(defs) != 1 || defs[0].Function.Parameters["type"] != "object" {
		t.Fatalf("defs=%#v", defs)
	}
}

func TestReminderToolGuidanceDistinguishesFromRemember(t *testing.T) {
	remember := RememberTool{}
	reminder := CreateReminderTool{}
	if !strings.Contains(remember.Description(), "use create_reminder instead") {
		t.Fatalf("remember description should reject reminder-like requests: %q", remember.Description())
	}
	if !strings.Contains(reminder.Description(), "ingatkan saya") || !strings.Contains(reminder.Description(), "Do not use remember") || !strings.Contains(reminder.Description(), "Do not assume ambiguous times") {
		t.Fatalf("create_reminder description should cover reminder phrasing: %q", reminder.Description())
	}
	if !strings.Contains(reminder.Description(), "generic notes") || !strings.Contains(remember.Description(), "todo lists") {
		t.Fatalf("persistence tools should defer notes and todos to customer capture tools")
	}
	params := reminder.Parameters()
	props := params["properties"].(map[string]any)
	title := props["title"].(map[string]any)
	if !strings.Contains(title["description"].(string), "bawa tas hitam") {
		t.Fatalf("title guidance should preserve Indonesian reminder text: %#v", title)
	}
}

func TestRememberRejectsReminderLikeContent(t *testing.T) {
	res := RememberTool{Store: &fakeMemoryStore{}}.Execute(context.Background(), ExecutionContext{}, map[string]any{
		"type":    "reminder",
		"content": "Ingatkan aku besok jam 7 pagi",
	})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "create_reminder") {
		t.Fatalf("expected reminder-like memory rejection, got %#v", res)
	}
}

func TestRememberRejectsCaptureLikeContent(t *testing.T) {
	res := RememberTool{Store: &fakeMemoryStore{}}.Execute(context.Background(), ExecutionContext{}, map[string]any{
		"type":    "note",
		"content": "Catat ide menu bekal sehat untuk anak",
	})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "capture tool") {
		t.Fatalf("expected capture-like memory rejection, got %#v", res)
	}
}

func TestRememberAllowsStableFactsContainingIdeSubstring(t *testing.T) {
	for _, content := range []string{
		"User works in video production",
		"User's IDE is VS Code",
		"User resides in Jakarta",
	} {
		store := &fakeMemoryStore{}
		res := RememberTool{Store: store}.Execute(context.Background(), ExecutionContext{}, map[string]any{
			"type":    "fact",
			"content": content,
		})
		if res == nil || res.IsError || store.addedContent != content {
			t.Fatalf("expected stable fact to be saved for %q, got res=%#v store=%#v", content, res, store)
		}
	}
}

func TestRegistryRetryableDefaults(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "echo"})
	if !r.Retryable("echo") || !r.Retryable("missing") {
		t.Fatalf("expected retryable default")
	}
	r.Register(RememberTool{})
	if r.Retryable("remember") {
		t.Fatalf("remember should be non-retryable")
	}
}

func TestRegistryFiltered(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "alpha"})
	r.Register(testTool{name: "beta"})
	filtered := r.Filtered(map[string]bool{"alpha": true}, map[string]bool{})
	if got := filtered.List(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("allowed filter got %v", got)
	}
	filtered = r.Filtered(nil, map[string]bool{"beta": true})
	if got := filtered.List(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("disabled filter got %v", got)
	}
}

func TestRegistryValidatesArgs(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "echo"})
	res := r.Execute(context.Background(), ExecutionContext{}, "echo", map[string]any{"extra": true})
	if !res.IsError {
		t.Fatalf("expected validation error")
	}
}

func TestRegistryRecoversPanics(t *testing.T) {
	r := NewRegistry()
	r.Register(testTool{name: "panic", fn: func() { panic("bad") }})
	res := r.Execute(context.Background(), ExecutionContext{}, "panic", map[string]any{"message": "x"})
	if !res.IsError {
		t.Fatalf("expected panic recovery result")
	}
}

func TestValidateArgsTypeCoverage(t *testing.T) {
	schema := map[string]any{
		"required":             []string{"name"},
		"additionalProperties": true,
		"properties": map[string]any{
			"name":    map[string]any{"type": "string"},
			"count":   map[string]any{"type": "integer"},
			"price":   map[string]any{"type": "number"},
			"enabled": map[string]any{"type": "boolean"},
			"tags":    map[string]any{"type": "array"},
			"nested": map[string]any{
				"type":       "object",
				"required":   []any{"child"},
				"properties": map[string]any{"child": map[string]any{"type": "string"}},
			},
		},
	}
	valid := map[string]any{
		"name": "duraclaw", "count": float64(3), "price": 1.25, "enabled": true, "tags": []any{"a"}, "nested": map[string]any{"child": "ok"}, "extra": true,
	}
	if err := ValidateArgs(schema, valid); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	invalidCases := []map[string]any{
		{"count": 1},
		{"name": 42},
		{"name": "x", "count": 1.5},
		{"name": "x", "price": "1"},
		{"name": "x", "enabled": "true"},
		{"name": "x", "tags": "a"},
		{"name": "x", "nested": "bad"},
		{"name": "x", "nested": map[string]any{}},
	}
	for _, args := range invalidCases {
		if err := ValidateArgs(schema, args); err == nil {
			t.Fatalf("expected validation error for %#v", args)
		}
	}
}

func TestRegistryExecuteMissingAndNilResult(t *testing.T) {
	r := NewRegistry()
	if res := r.Execute(context.Background(), ExecutionContext{}, "missing", nil); !res.IsError {
		t.Fatalf("expected missing tool error")
	}
	r.Register(nilTool{name: "nil"})
	if res := r.Execute(context.Background(), ExecutionContext{}, "nil", nil); !res.IsError {
		t.Fatalf("expected nil result error")
	}
}

type nilTool struct {
	name string
}

func (t nilTool) Name() string               { return t.name }
func (t nilTool) Description() string        { return "nil" }
func (t nilTool) Parameters() map[string]any { return nil }
func (t nilTool) Execute(context.Context, ExecutionContext, map[string]any) *Result {
	return nil
}
