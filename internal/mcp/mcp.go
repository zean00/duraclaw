package mcp

import "context"

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

type Manager struct{ clients map[string]Client }

func NewManager() *Manager { return &Manager{clients: map[string]Client{}} }

func (m *Manager) Register(name string, client Client) { m.clients[name] = client }

func (m *Manager) RegisterHTTP(name, baseURL, token string) {
	m.Register(name, HTTPClient{BaseURL: baseURL, Token: token})
}

func (m *Manager) Client(name string) (Client, bool) {
	c, ok := m.clients[name]
	return c, ok
}
