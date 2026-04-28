package mcp

import "testing"

func TestExecutionContextHeaders(t *testing.T) {
	headers := (ExecutionContext{
		CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", RunID: "r", ToolCallID: "t", RequestID: "req",
	}).Headers()
	for _, key := range []string{"X-Customer-ID", "X-User-ID", "X-Agent-Instance-ID", "X-Session-ID", "X-Run-ID", "X-Tool-Call-ID", "X-Request-ID"} {
		if headers[key] == "" {
			t.Fatalf("missing %s in %#v", key, headers)
		}
	}
}

func TestManagerRegistersHTTPClient(t *testing.T) {
	manager := NewManager()
	manager.RegisterHTTP("srv", "http://example.test", "tok")
	client, ok := manager.Client("srv")
	if !ok {
		t.Fatalf("missing client")
	}
	if _, ok := client.(HTTPClient); !ok {
		t.Fatalf("client=%T", client)
	}
}
