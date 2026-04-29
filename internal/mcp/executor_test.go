package mcp

import (
	"context"
	"errors"
	"testing"

	"duraclaw/internal/observability"
)

type fakeMCPStore struct {
	started   bool
	completed bool
	errText   *string
	accessErr error
}

func (s *fakeMCPStore) CheckMCPToolAccess(context.Context, string, string, string, string, string) error {
	return s.accessErr
}

func (s *fakeMCPStore) StartMCPCall(context.Context, string, string, string, any) (string, error) {
	s.started = true
	return "mcp-call-1", nil
}

func (s *fakeMCPStore) CompleteMCPCall(_ context.Context, _ string, _ string, _ any, errText *string) error {
	s.completed = true
	s.errText = errText
	return nil
}

type fakeMCPClient struct {
	seenToolCallID string
	err            error
}

func (c *fakeMCPClient) CallTool(_ context.Context, exec ExecutionContext, _, _ string, _ map[string]any) (map[string]any, error) {
	c.seenToolCallID = exec.ToolCallID
	if c.err != nil {
		return nil, c.err
	}
	return map[string]any{"ok": true}, nil
}

func TestExecutorPersistsAndInjectsToolCallID(t *testing.T) {
	store := &fakeMCPStore{}
	client := &fakeMCPClient{}
	manager := NewManager()
	manager.Register("srv", client)
	got, err := NewExecutor(manager, store).CallTool(context.Background(), ExecutionContext{RunID: "run-1"}, "srv", "tool", map[string]any{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !store.started || !store.completed || client.seenToolCallID != "mcp-call-1" || got["ok"] != true {
		t.Fatalf("store=%#v client=%#v got=%#v", store, client, got)
	}
}

func TestExecutorChecksToolAccessBeforePersisting(t *testing.T) {
	store := &fakeMCPStore{accessErr: errors.New("denied")}
	client := &fakeMCPClient{}
	manager := NewManager()
	manager.Register("srv", client)
	_, err := NewExecutor(manager, store).CallTool(context.Background(), ExecutionContext{RunID: "run-1"}, "srv", "tool", nil)
	if err == nil {
		t.Fatalf("expected access error")
	}
	if store.started || store.completed {
		t.Fatalf("access denial should not persist MCP call: %#v", store)
	}
}

func TestExecutorPersistsFailure(t *testing.T) {
	store := &fakeMCPStore{}
	client := &fakeMCPClient{err: errors.New("boom")}
	manager := NewManager()
	manager.Register("srv", client)
	_, err := NewExecutor(manager, store).CallTool(context.Background(), ExecutionContext{RunID: "run-1"}, "srv", "tool", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !store.completed || store.errText == nil {
		t.Fatalf("expected failed completion: %#v", store)
	}
}

func TestExecutorRecordsCounters(t *testing.T) {
	store := &fakeMCPStore{}
	client := &fakeMCPClient{}
	manager := NewManager()
	manager.Register("srv", client)
	counters := observability.NewCounters()
	_, err := NewExecutor(manager, store).WithCounters(counters).CallTool(context.Background(), ExecutionContext{RunID: "run-1"}, "srv", "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if counters.Snapshot()["mcp_call_succeeded"] != 1 || counters.DurationSnapshot()["mcp_call_duration_seconds"].Count != 1 {
		t.Fatalf("counters=%#v durations=%#v", counters.Snapshot(), counters.DurationSnapshot())
	}
}
