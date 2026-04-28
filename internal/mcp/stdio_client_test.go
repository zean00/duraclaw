package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStdioClientSpeaksMCPJSONRPC(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"test","version":"1"}}}\n'
IFS= read initialized
IFS= read call
printf '{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"ok"}],"structuredContent":{"run":"%s"}}}\n' "$DURACLAW_X_RUN_ID"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	client := StdioClient{Command: script}
	got, err := client.CallTool(context.Background(), ExecutionContext{RunID: "run-1"}, "srv", "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := got["structuredContent"].(map[string]any)
	if structured["run"] != "run-1" {
		t.Fatalf("got=%#v", got)
	}
}

func TestStdioClientRequiresCommand(t *testing.T) {
	_, err := (StdioClient{}).CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEnvName(t *testing.T) {
	if got := envName("X-Run-ID"); got != "DURACLAW_X_RUN_ID" {
		t.Fatalf("got %q", got)
	}
}

func TestJSONRPCError(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read call
printf '{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"boom"}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (StdioClient{Command: script}).CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err == nil {
		t.Fatal("expected json-rpc error")
	}
}
