package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPClient struct {
	BaseURL    string
	Token      string
	SSE        bool
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
	if c.SSE {
		req.Header.Set("Accept", "text/event-stream")
	}
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
	if err := decodeHTTPPayload(resp.Body, c.SSE || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"), &payload); err != nil {
		return nil, err
	}
	if payload.Result == nil {
		payload.Result = map[string]any{}
	}
	return payload.Result, nil
}

func (c HTTPClient) ListTools(ctx context.Context, exec ExecutionContext, serverName string) ([]ToolInfo, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, fmt.Errorf("mcp http base url is required")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/tools/list?server_name="+serverName, nil)
	if err != nil {
		return nil, err
	}
	if c.SSE {
		req.Header.Set("Accept", "text/event-stream")
	}
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
		return nil, fmt.Errorf("mcp http list tools failed with status %d", resp.StatusCode)
	}
	var payload struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := decodeHTTPPayload(resp.Body, c.SSE || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"), &payload); err != nil {
		return nil, err
	}
	return payload.Tools, nil
}

func decodeHTTPPayload(r io.Reader, sse bool, target any) error {
	if !sse {
		return json.NewDecoder(r).Decode(target)
	}
	data, err := readSSEData(r)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readSSEData(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				return []byte(strings.Join(data, "\n")), nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			if value == "[DONE]" {
				break
			}
			data = append(data, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("mcp http sse response did not contain data")
	}
	return []byte(strings.Join(data, "\n")), nil
}
