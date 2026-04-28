package mcp

import (
	"context"
	"sync"
)

type PersistentStdioClient struct {
	Command string
	Args    []string
	Env     map[string]string

	mu      sync.Mutex
	callMu  sync.Mutex
	session *stdioSession
	nextID  int
}

func (c *PersistentStdioClient) CallTool(ctx context.Context, execCtx ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": toolName, "arguments": arguments},
	}); err != nil {
		c.Close()
		return nil, err
	}
	return session.readResult(id)
}

func (c *PersistentStdioClient) ListTools(ctx context.Context, execCtx ExecutionContext, serverName string) ([]ToolInfo, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/list", "params": map[string]any{}}); err != nil {
		c.Close()
		return nil, err
	}
	result, err := session.readResult(id)
	if err != nil {
		c.Close()
		return nil, err
	}
	return decodeToolList(result)
}

func (c *PersistentStdioClient) ensureSession(ctx context.Context, execCtx ExecutionContext) (*stdioSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		return c.session, nil
	}
	session, err := (StdioClient{Command: c.Command, Args: c.Args, Env: c.Env}).open(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	if err := session.initialize(); err != nil {
		session.close()
		return nil, err
	}
	c.session = session
	c.nextID = 2
	return session, nil
}

func (c *PersistentStdioClient) next() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *PersistentStdioClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		c.session.close()
		c.session = nil
	}
}
