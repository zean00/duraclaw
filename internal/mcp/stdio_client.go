package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type StdioClient struct {
	Command string
	Args    []string
	Env     map[string]string
}

func (c StdioClient) CallTool(ctx context.Context, execCtx ExecutionContext, serverName, toolName string, arguments map[string]any) (map[string]any, error) {
	session, err := c.open(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if err := session.initialize(); err != nil {
		return nil, err
	}
	if err := session.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": toolName, "arguments": arguments},
	}); err != nil {
		return nil, err
	}
	return session.readResult(2)
}

func (c StdioClient) ListTools(ctx context.Context, execCtx ExecutionContext, serverName string) ([]ToolInfo, error) {
	session, err := c.open(ctx, execCtx)
	if err != nil {
		return nil, err
	}
	defer session.close()
	if err := session.initialize(); err != nil {
		return nil, err
	}
	if err := session.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	}); err != nil {
		return nil, err
	}
	result, err := session.readResult(2)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result["tools"])
	var tools []ToolInfo
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

type stdioSession struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
}

func (c StdioClient) open(ctx context.Context, execCtx ExecutionContext) (*stdioSession, error) {
	if strings.TrimSpace(c.Command) == "" {
		return nil, fmt.Errorf("mcp stdio command is required")
	}
	cmd := exec.CommandContext(ctx, c.Command, c.Args...)
	cmd.Env = os.Environ()
	for key, value := range execCtx.Headers() {
		if value != "" {
			cmd.Env = append(cmd.Env, envName(key)+"="+value)
		}
	}
	for key, value := range c.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &stdioSession{cmd: cmd, stdin: stdin, scanner: bufio.NewScanner(stdout)}, nil
}

func (s *stdioSession) close() {
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

func (s *stdioSession) initialize() error {
	if err := s.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "duraclaw", "version": "dev"},
		},
	}); err != nil {
		return err
	}
	if _, err := s.readResult(1); err != nil {
		return err
	}
	return s.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
}

func (s *stdioSession) write(payload map[string]any) error {
	return writeJSONRPC(s.stdin, payload)
}

func (s *stdioSession) readResult(id int) (map[string]any, error) {
	return readJSONRPCResult(s.scanner, id)
}

func envName(header string) string {
	replacer := strings.NewReplacer("-", "_")
	return "DURACLAW_" + strings.ToUpper(replacer.Replace(header))
}

func writeJSONRPC(w io.Writer, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

func readJSONRPCResult(scanner *bufio.Scanner, id int) (map[string]any, error) {
	for scanner.Scan() {
		var msg struct {
			ID     any            `json:"id"`
			Result map[string]any `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return nil, err
		}
		if !jsonRPCIDMatches(msg.ID, id) {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("mcp stdio json-rpc error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if msg.Result == nil {
			msg.Result = map[string]any{}
		}
		return msg.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mcp stdio response %d not received", id)
}

func jsonRPCIDMatches(raw any, want int) bool {
	switch v := raw.(type) {
	case float64:
		return int(v) == want
	case string:
		return v == fmt.Sprintf("%d", want)
	default:
		return false
	}
}
