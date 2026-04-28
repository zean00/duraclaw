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

func (c *PersistentStdioClient) ListResources(ctx context.Context, execCtx ExecutionContext, serverName string) ([]ResourceInfo, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "resources/list", "params": map[string]any{}}); err != nil {
		c.Close()
		return nil, err
	}
	result, err := session.readResult(id)
	if err != nil {
		c.Close()
		return nil, err
	}
	return decodeResourceList(result)
}

func (c *PersistentStdioClient) ReadResource(ctx context.Context, execCtx ExecutionContext, serverName, uri string) (*ResourceContent, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "resources/read", "params": map[string]any{"uri": uri}}); err != nil {
		c.Close()
		return nil, err
	}
	result, err := session.readResult(id)
	if err != nil {
		c.Close()
		return nil, err
	}
	return decodeResourceContent(result)
}

func (c *PersistentStdioClient) SubscribeResource(ctx context.Context, execCtx ExecutionContext, serverName, uri string) error {
	return c.resourceSubscription(ctx, execCtx, "resources/subscribe", uri)
}

func (c *PersistentStdioClient) UnsubscribeResource(ctx context.Context, execCtx ExecutionContext, serverName, uri string) error {
	return c.resourceSubscription(ctx, execCtx, "resources/unsubscribe", uri)
}

func (c *PersistentStdioClient) resourceSubscription(ctx context.Context, execCtx ExecutionContext, method, uri string) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": map[string]any{"uri": uri}}); err != nil {
		c.Close()
		return err
	}
	if _, err := session.readResult(id); err != nil {
		c.Close()
		return err
	}
	return nil
}

func (c *PersistentStdioClient) ListPrompts(ctx context.Context, execCtx ExecutionContext, serverName string) ([]PromptInfo, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "prompts/list", "params": map[string]any{}}); err != nil {
		c.Close()
		return nil, err
	}
	result, err := session.readResult(id)
	if err != nil {
		c.Close()
		return nil, err
	}
	return decodePromptList(result)
}

func (c *PersistentStdioClient) GetPrompt(ctx context.Context, execCtx ExecutionContext, serverName, promptName string, arguments map[string]any) (*PromptContent, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	session, err := c.ensureSession(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	id := c.next()
	if err := session.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "prompts/get", "params": map[string]any{"name": promptName, "arguments": arguments}}); err != nil {
		c.Close()
		return nil, err
	}
	result, err := session.readResult(id)
	if err != nil {
		c.Close()
		return nil, err
	}
	return decodePromptContent(result)
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
