package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
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

func (c ExecutionContext) Headers() map[string]string {
	return map[string]string{
		"X-Customer-ID": c.CustomerID, "X-User-ID": c.UserID, "X-Agent-Instance-ID": c.AgentInstanceID,
		"X-Session-ID": c.SessionID, "X-Run-ID": c.RunID, "X-Tool-Call-ID": c.ToolCallID, "X-Request-ID": c.RequestID,
	}
}

type Client interface {
	CallTool(ctx context.Context, exec ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error)
}

type ToolLister interface {
	ListTools(ctx context.Context, exec ExecutionContext, serverName string) ([]ToolInfo, error)
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type ServerSpec struct {
	Name          string            `json:"name"`
	Transport     string            `json:"transport"`
	BaseURL       string            `json:"base_url,omitempty"`
	Token         string            `json:"-"`
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"-"`
	MaxRetries    int               `json:"max_retries"`
	RetryDelay    time.Duration     `json:"retry_delay"`
	MaxConcurrent int               `json:"max_concurrent"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
}

type ServerStatus struct {
	Name         string     `json:"name"`
	Transport    string     `json:"transport"`
	RegisteredAt time.Time  `json:"registered_at"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	CallCount    int64      `json:"call_count"`
	FailureCount int64      `json:"failure_count"`
}

type managedClient struct {
	spec   ServerSpec
	client Client
	sem    chan struct{}
	mu     sync.RWMutex
	status ServerStatus
}

type Manager struct {
	mu      sync.RWMutex
	clients map[string]*managedClient
}

func NewManager() *Manager { return &Manager{clients: map[string]*managedClient{}} }

func (m *Manager) Register(name string, client Client) {
	m.RegisterWithSpec(ServerSpec{Name: name, Transport: "custom"}, client)
}

func (m *Manager) RegisterHTTP(name, baseURL, token string) {
	m.RegisterWithSpec(ServerSpec{Name: name, Transport: "http", BaseURL: baseURL, Token: token}, HTTPClient{BaseURL: baseURL, Token: token})
}

func (m *Manager) RegisterWithSpec(spec ServerSpec, client Client) {
	if m == nil || client == nil {
		return
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return
	}
	if spec.Transport == "" {
		spec.Transport = "custom"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clients == nil {
		m.clients = map[string]*managedClient{}
	}
	now := time.Now().UTC()
	var sem chan struct{}
	if spec.MaxConcurrent > 0 {
		sem = make(chan struct{}, spec.MaxConcurrent)
	}
	m.clients[spec.Name] = &managedClient{
		spec:   spec,
		client: client,
		sem:    sem,
		status: ServerStatus{Name: spec.Name, Transport: spec.Transport, RegisteredAt: now},
	}
}

func (m *Manager) WithConfig(raw json.RawMessage) (*Manager, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return m, nil
	}
	next := NewManager()
	if m != nil {
		m.mu.RLock()
		for name, client := range m.clients {
			next.clients[name] = client
		}
		m.mu.RUnlock()
	}
	var cfg struct {
		Servers []struct {
			Name          string            `json:"name"`
			Transport     string            `json:"transport"`
			BaseURL       string            `json:"base_url"`
			Token         string            `json:"token"`
			Command       string            `json:"command"`
			Args          []string          `json:"args"`
			Env           map[string]string `json:"env"`
			MaxRetries    int               `json:"max_retries"`
			RetryDelayMS  int               `json:"retry_delay_ms"`
			MaxConcurrent int               `json:"max_concurrent"`
			Metadata      map[string]any    `json:"metadata"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	for _, server := range cfg.Servers {
		transport := strings.TrimSpace(server.Transport)
		if transport == "" {
			transport = "http"
		}
		spec := ServerSpec{
			Name: server.Name, Transport: transport, BaseURL: server.BaseURL, Token: server.Token, Command: server.Command, Args: server.Args, Env: server.Env,
			MaxRetries: server.MaxRetries, RetryDelay: time.Duration(server.RetryDelayMS) * time.Millisecond,
			MaxConcurrent: server.MaxConcurrent, Metadata: server.Metadata,
		}
		switch transport {
		case "http", "sse":
			next.RegisterWithSpec(spec, HTTPClient{BaseURL: server.BaseURL, Token: server.Token})
		case "stdio":
			next.RegisterWithSpec(spec, StdioClient{Command: server.Command, Args: server.Args, Env: server.Env})
		default:
			return nil, fmt.Errorf("unsupported mcp transport %q for server %q", transport, server.Name)
		}
	}
	return next, nil
}

func (m *Manager) Unregister(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, name)
}

func (m *Manager) Client(name string) (Client, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	c, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return c, true
}

func (m *Manager) Status(name string) (ServerStatus, bool) {
	if m == nil {
		return ServerStatus{}, false
	}
	m.mu.RLock()
	c, ok := m.clients[name]
	m.mu.RUnlock()
	if !ok {
		return ServerStatus{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status, true
}

func (m *Manager) Statuses() []ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	m.mu.RUnlock()
	sort.Strings(names)
	out := make([]ServerStatus, 0, len(names))
	for _, name := range names {
		if status, ok := m.Status(name); ok {
			out = append(out, status)
		}
	}
	return out
}

func (m *Manager) ListTools(ctx context.Context, exec ExecutionContext, serverName string) ([]ToolInfo, error) {
	if m == nil {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	m.mu.RLock()
	client, ok := m.clients[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("mcp server %q not found", serverName)
	}
	return client.ListTools(ctx, exec, serverName)
}

func (c *managedClient) CallTool(ctx context.Context, exec ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	attempts := c.spec.MaxRetries + 1
	if attempts <= 0 {
		attempts = 1
	}
	delay := c.spec.RetryDelay
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		now := time.Now().UTC()
		c.mu.Lock()
		c.status.LastUsedAt = &now
		c.status.CallCount++
		c.mu.Unlock()
		result, err := c.client.CallTool(ctx, exec, serverName, toolName, arguments)
		if err == nil {
			c.mu.Lock()
			c.status.LastError = ""
			c.mu.Unlock()
			return result, nil
		}
		lastErr = err
		c.mu.Lock()
		c.status.LastError = err.Error()
		c.status.FailureCount++
		c.mu.Unlock()
		if attempt == attempts-1 || delay <= 0 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (c *managedClient) ListTools(ctx context.Context, exec ExecutionContext, serverName string) ([]ToolInfo, error) {
	if c.sem != nil {
		select {
		case c.sem <- struct{}{}:
			defer func() { <-c.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	lister, ok := c.client.(ToolLister)
	if !ok {
		return nil, fmt.Errorf("mcp server %q does not support tool discovery", serverName)
	}
	now := time.Now().UTC()
	c.mu.Lock()
	c.status.LastUsedAt = &now
	c.status.CallCount++
	c.mu.Unlock()
	tools, err := lister.ListTools(ctx, exec, serverName)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.status.LastError = err.Error()
		c.status.FailureCount++
		return nil, err
	}
	c.status.LastError = ""
	return tools, nil
}
