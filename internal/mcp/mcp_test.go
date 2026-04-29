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

func TestManagerStatusesAndUnregister(t *testing.T) {
	manager := NewManager()
	manager.Register("b", listingClient{})
	manager.Register("a", listingClient{})
	statuses := manager.Statuses()
	if len(statuses) != 2 || statuses[0].Name != "a" || statuses[1].Name != "b" {
		t.Fatalf("statuses=%#v", statuses)
	}
	manager.Unregister("a")
	if _, ok := manager.Client("a"); ok {
		t.Fatal("expected client to be unregistered")
	}
	if (*Manager)(nil).Statuses() != nil {
		t.Fatal("nil manager should have no statuses")
	}
	(*Manager)(nil).Unregister("missing")
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

func TestManagerWithConfigRegistersLongLivedStdioServers(t *testing.T) {
	manager := NewManager()
	configured, err := manager.WithConfig([]byte(`{"servers":[{"name":"local","transport":"stdio","command":"sh","args":["-c","cat"],"long_lived":true}]}`))
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
	if _, ok := managed.client.(*PersistentStdioClient); !ok {
		t.Fatalf("client=%T", managed.client)
	}
}

func TestManagerWithConfigRegistersSSEClient(t *testing.T) {
	manager := NewManager()
	configured, err := manager.WithConfig([]byte(`{"servers":[{"name":"events","transport":"sse","base_url":"http://example.test"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	client, ok := configured.Client("events")
	if !ok {
		t.Fatal("missing configured client")
	}
	managed := client.(*managedClient)
	httpClient, ok := managed.client.(HTTPClient)
	if !ok || !httpClient.SSE {
		t.Fatalf("client=%T %#v", managed.client, managed.client)
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

func TestManagerResourcesDelegate(t *testing.T) {
	manager := NewManager()
	manager.Register("srv", resourceClient{})
	resources, err := manager.ListResources(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].URI != "file://doc" {
		t.Fatalf("resources=%#v", resources)
	}
	content, err := manager.ReadResource(context.Background(), ExecutionContext{}, "srv", "file://doc")
	if err != nil {
		t.Fatal(err)
	}
	if content.Text != "hello" {
		t.Fatalf("content=%#v", content)
	}
	if err := manager.SubscribeResource(context.Background(), ExecutionContext{}, "srv", "file://doc"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UnsubscribeResource(context.Background(), ExecutionContext{}, "srv", "file://doc"); err != nil {
		t.Fatal(err)
	}
	status, ok := manager.Status("srv")
	if !ok || status.CallCount != 4 || status.LastError != "" {
		t.Fatalf("status=%#v ok=%v", status, ok)
	}
}

func TestManagerPromptsDelegate(t *testing.T) {
	manager := NewManager()
	manager.Register("srv", promptClient{})
	prompts, err := manager.ListPrompts(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "summarize" {
		t.Fatalf("prompts=%#v", prompts)
	}
	prompt, err := manager.GetPrompt(context.Background(), ExecutionContext{}, "srv", "summarize", map[string]any{"topic": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "summarize" || len(prompt.Messages) != 1 {
		t.Fatalf("prompt=%#v", prompt)
	}
}

type listingClient struct{}

func (listingClient) CallTool(context.Context, ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (listingClient) ListTools(context.Context, ExecutionContext, string) ([]ToolInfo, error) {
	return []ToolInfo{{Name: "lookup"}}, nil
}

type resourceClient struct{}

func (resourceClient) CallTool(context.Context, ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (resourceClient) ListResources(context.Context, ExecutionContext, string) ([]ResourceInfo, error) {
	return []ResourceInfo{{URI: "file://doc", Name: "doc"}}, nil
}

func (resourceClient) ReadResource(context.Context, ExecutionContext, string, string) (*ResourceContent, error) {
	return &ResourceContent{URI: "file://doc", Text: "hello"}, nil
}

func (resourceClient) SubscribeResource(context.Context, ExecutionContext, string, string) error {
	return nil
}

func (resourceClient) UnsubscribeResource(context.Context, ExecutionContext, string, string) error {
	return nil
}

type promptClient struct{}

func (promptClient) CallTool(context.Context, ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func (promptClient) ListPrompts(context.Context, ExecutionContext, string) ([]PromptInfo, error) {
	return []PromptInfo{{Name: "summarize"}}, nil
}

func (promptClient) GetPrompt(context.Context, ExecutionContext, string, string, map[string]any) (*PromptContent, error) {
	return &PromptContent{Name: "summarize", Messages: []PromptMessage{{Role: "user", Content: map[string]any{"type": "text", "text": "Summarize"}}}}, nil
}
