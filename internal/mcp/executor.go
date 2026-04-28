package mcp

import (
	"context"
	"fmt"
	"time"

	"duraclaw/internal/observability"
)

type CallStore interface {
	StartMCPCall(ctx context.Context, runID, serverName, toolName string, request any) (string, error)
	CompleteMCPCall(ctx context.Context, callID, runID string, response any, errText *string) error
}

type Executor struct {
	manager  *Manager
	store    CallStore
	counters *observability.Counters
}

func NewExecutor(manager *Manager, store CallStore) *Executor {
	if manager == nil {
		manager = NewManager()
	}
	return &Executor{manager: manager, store: store}
}

func (e *Executor) WithCounters(counters *observability.Counters) *Executor {
	e.counters = counters
	return e
}

func (e *Executor) CallTool(ctx context.Context, exec ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
	start := time.Now()
	if e.store == nil {
		return nil, fmt.Errorf("mcp executor store is nil")
	}
	client, ok := e.manager.Client(serverName)
	if !ok {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	callID, err := e.store.StartMCPCall(ctx, exec.RunID, serverName, toolName, map[string]any{
		"headers":   exec.Headers(),
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	exec.ToolCallID = callID
	result, err := client.CallTool(ctx, exec, serverName, toolName, arguments)
	if e.counters != nil {
		e.counters.ObserveDuration("mcp_call_duration_seconds", time.Since(start))
	}
	if err != nil {
		if e.counters != nil {
			e.counters.Inc("mcp_call_failed")
		}
		msg := err.Error()
		_ = e.store.CompleteMCPCall(context.Background(), callID, exec.RunID, nil, &msg)
		return nil, err
	}
	if err := e.store.CompleteMCPCall(ctx, callID, exec.RunID, result, nil); err != nil {
		return nil, err
	}
	if e.counters != nil {
		e.counters.Inc("mcp_call_succeeded")
	}
	return result, nil
}
