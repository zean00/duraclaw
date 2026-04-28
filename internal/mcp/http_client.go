package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func (c HTTPClient) CallTool(ctx context.Context, exec ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("mcp http base url is required")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"server_name": serverName, "tool_name": toolName, "arguments": arguments})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/tools/call", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for key, value := range exec.Headers() {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp http call failed with status %d", resp.StatusCode)
	}
	var payload struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Result == nil {
		payload.Result = map[string]any{}
	}
	return payload.Result, nil
}
