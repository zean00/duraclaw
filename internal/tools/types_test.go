package tools

import (
	"context"
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
