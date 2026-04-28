// Package tools contains durable tool contracts adapted from PicoClaw.
//
// Portions are derived from PicoClaw's MIT-licensed tool registry patterns:
// Copyright (c) Sipeed PicoClaw contributors.
package tools

import (
	"context"
	"duraclaw/internal/providers"
	"fmt"
	"math"
	"sort"
	"sync"
)

type ExecutionContext struct {
	CustomerID      string
	UserID          string
	AgentInstanceID string
	SessionID       string
	RunID           string
	ToolCallID      string
	RequestID       string
}

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result
}

type RetryableTool interface {
	Retryable() bool
}

type Result struct {
	ForLLM  string `json:"for_llm"`
	ForUser string `json:"for_user,omitempty"`
	IsError bool   `json:"is_error"`
	Err     error  `json:"-"`
}

func NewResult(forLLM string) *Result { return &Result{ForLLM: forLLM} }
func ErrorResult(msg string) *Result {
	return &Result{ForLLM: msg, ForUser: msg, IsError: true, Err: fmt.Errorf("%s", msg)}
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Filtered(allowed, disabled map[string]bool) *Registry {
	out := NewRegistry()
	if r == nil {
		return out
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, tool := range r.tools {
		if len(allowed) > 0 && !allowed[name] {
			continue
		}
		if disabled[name] {
			continue
		}
		out.tools[name] = tool
	}
	return out
}

func (r *Registry) ToProviderDefs() []providers.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	defs := make([]providers.ToolDefinition, 0, len(names))
	for _, name := range names {
		tool := r.tools[name]
		defs = append(defs, providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return defs
}

func (r *Registry) Execute(ctx context.Context, exec ExecutionContext, name string, args map[string]any) (result *Result) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return ErrorResult(fmt.Sprintf("tool %q not found", name))
	}
	if err := ValidateArgs(tool.Parameters(), args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments for tool %q: %s", name, err))
	}
	defer func() {
		if re := recover(); re != nil {
			result = ErrorResult(fmt.Sprintf("tool %q crashed: %v", name, re))
		}
		if result == nil {
			result = ErrorResult(fmt.Sprintf("tool %q returned nil result", name))
		}
	}()
	return tool.Execute(ctx, exec, args)
}

func (r *Registry) Retryable(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	if !ok {
		return true
	}
	if meta, ok := tool.(RetryableTool); ok {
		return meta.Retryable()
	}
	return true
}

func ValidateArgs(schema map[string]any, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	if args == nil {
		args = map[string]any{}
	}
	if raw, ok := schema["required"]; ok {
		for _, field := range stringSlice(raw) {
			if _, exists := args[field]; !exists {
				return fmt.Errorf("missing required property %q", field)
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	additional, _ := schema["additionalProperties"].(bool)
	for key, val := range args {
		raw, known := props[key]
		if !known {
			if !additional {
				return fmt.Errorf("unexpected property %q", key)
			}
			continue
		}
		prop, _ := raw.(map[string]any)
		if err := checkType(key, val, prop); err != nil {
			return err
		}
	}
	return nil
}

func stringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func checkType(key string, val any, schema map[string]any) error {
	typ, _ := schema["type"].(string)
	switch typ {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("property %q: expected string, got %T", key, val)
		}
	case "integer":
		switch n := val.(type) {
		case int, int64:
		case float64:
			if n != math.Trunc(n) {
				return fmt.Errorf("property %q: expected integer, got fractional number", key)
			}
		default:
			return fmt.Errorf("property %q: expected integer, got %T", key, val)
		}
	case "number":
		switch val.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("property %q: expected number, got %T", key, val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("property %q: expected boolean, got %T", key, val)
		}
	case "array":
		if _, ok := val.([]any); !ok {
			return fmt.Errorf("property %q: expected array, got %T", key, val)
		}
	case "object":
		obj, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("property %q: expected object, got %T", key, val)
		}
		return ValidateArgs(schema, obj)
	}
	return nil
}
