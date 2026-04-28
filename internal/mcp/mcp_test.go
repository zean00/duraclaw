package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
	if _, ok := client.(*managedClient); !ok {
		t.Fatalf("client=%T", client)
	}
	status, ok := manager.Status("srv")
	if !ok || status.Transport != "http" {
		t.Fatalf("status=%#v ok=%v", status, ok)
	}
}

func TestRegisterHTTPDoesNotRetryByDefault(t *testing.T) {
	manager := NewManager()
	client := &retryClient{}
	manager.RegisterWithSpec(ServerSpec{Name: "srv", Transport: "http"}, client)
	managed, ok := manager.Client("srv")
	if !ok {
		t.Fatal("missing client")
	}
	_, err := managed.CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err == nil {
		t.Fatal("expected first call failure without implicit retry")
	}
	if client.calls != 1 {
		t.Fatalf("calls=%d", client.calls)
	}
}

type retryClient struct {
	calls int
}

func (c *retryClient) CallTool(context.Context, ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	c.calls++
	if c.calls == 1 {
		return nil, errors.New("temporary")
	}
	return map[string]any{"ok": true}, nil
}

func TestManagerManagedClientRetriesAndTracksStatus(t *testing.T) {
	manager := NewManager()
	client := &retryClient{}
	manager.RegisterWithSpec(ServerSpec{Name: "srv", Transport: "custom", MaxRetries: 1, RetryDelay: time.Millisecond}, client)
	managed, ok := manager.Client("srv")
	if !ok {
		t.Fatal("missing client")
	}
	got, err := managed.CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || client.calls != 2 {
		t.Fatalf("got=%#v calls=%d", got, client.calls)
	}
	status, ok := manager.Status("srv")
	if !ok || status.CallCount != 2 || status.FailureCount != 1 || status.LastError != "" || status.LastUsedAt == nil {
		t.Fatalf("status=%#v ok=%v", status, ok)
	}
}

func TestManagerWithConfigRegistersHTTPServers(t *testing.T) {
	manager := NewManager()
	configured, err := manager.WithConfig([]byte(`{"servers":[{"name":"srv","transport":"http","base_url":"http://example.test","max_retries":2,"retry_delay_ms":5,"max_concurrent":1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := configured.Client("srv")
	if !ok {
		t.Fatal("missing configured client")
	}
	managed, ok := client.(*managedClient)
	if !ok {
		t.Fatalf("client=%T", client)
	}
	if managed.spec.MaxRetries != 2 || managed.spec.RetryDelay != 5*time.Millisecond || managed.sem == nil {
		t.Fatalf("spec=%#v sem=%v", managed.spec, managed.sem)
	}
}

func TestManagerWithConfigRegistersStdioServers(t *testing.T) {
	manager := NewManager()
	configured, err := manager.WithConfig([]byte(`{"servers":[{"name":"local","transport":"stdio","command":"sh","args":["-c","cat"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := configured.Client("local")
	if !ok {
		t.Fatal("missing configured client")
	}
	managed, ok := client.(*managedClient)
	if !ok {
		t.Fatalf("client=%T", client)
	}
	if managed.spec.Transport != "stdio" || managed.spec.Command != "sh" {
		t.Fatalf("spec=%#v", managed.spec)
	}
}

func TestManagerListToolsDelegates(t *testing.T) {
	manager := NewManager()
	manager.Register("srv", listingClient{})
	tools, err := manager.ListTools(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "lookup" {
		t.Fatalf("tools=%#v", tools)
	}
	status, ok := manager.Status("srv")
	if !ok || status.CallCount != 1 || status.LastUsedAt == nil || status.LastError != "" {
		t.Fatalf("status=%#v ok=%v", status, ok)
	}
}

type listingClient struct{}

func (listingClient) CallTool(context.Context, ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (listingClient) ListTools(context.Context, ExecutionContext, string) ([]ToolInfo, error) {
	return []ToolInfo{{Name: "lookup"}}, nil
}
