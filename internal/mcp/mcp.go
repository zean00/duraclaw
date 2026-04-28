package mcp

import (
	"context"
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

type ServerSpec struct {
	Name       string         `json:"name"`
	Transport  string         `json:"transport"`
	BaseURL    string         `json:"base_url,omitempty"`
	Token      string         `json:"-"`
	MaxRetries int            `json:"max_retries"`
	RetryDelay time.Duration  `json:"retry_delay"`
	Metadata   map[string]any `json:"metadata,omitempty"`
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
	m.clients[spec.Name] = &managedClient{
		spec:   spec,
		client: client,
		status: ServerStatus{Name: spec.Name, Transport: spec.Transport, RegisteredAt: now},
	}
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

func (c *managedClient) CallTool(ctx context.Context, exec ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
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
