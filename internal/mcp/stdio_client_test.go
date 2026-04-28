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

func TestStdioClientListsTools(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read list
printf '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"lookup","description":"Lookup"}]}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	tools, err := (StdioClient{Command: script}).ListTools(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "lookup" {
		t.Fatalf("tools=%#v", tools)
	}
}

func TestStdioClientReadsResources(t *testing.T) {
	script := filepath.Join(t.TempDir(), "server.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read list
printf '{"jsonrpc":"2.0","id":2,"result":{"resources":[{"uri":"file://doc","name":"doc"}]}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	resources, err := (StdioClient{Command: script}).ListResources(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].URI != "file://doc" {
		t.Fatalf("resources=%#v", resources)
	}
	script = filepath.Join(t.TempDir(), "reader.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read read_request
printf '{"jsonrpc":"2.0","id":2,"result":{"contents":[{"uri":"file://doc","text":"hello"}]}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := (StdioClient{Command: script}).ReadResource(context.Background(), ExecutionContext{}, "srv", "file://doc")
	if err != nil {
		t.Fatal(err)
	}
	if content.Text != "hello" {
		t.Fatalf("content=%#v", content)
	}
	script = filepath.Join(t.TempDir(), "subscribe.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read subscribe
printf '{"jsonrpc":"2.0","id":2,"result":{}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (StdioClient{Command: script}).SubscribeResource(context.Background(), ExecutionContext{}, "srv", "file://doc"); err != nil {
		t.Fatal(err)
	}
}

func TestStdioClientPrompts(t *testing.T) {
	script := filepath.Join(t.TempDir(), "prompts.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read list
printf '{"jsonrpc":"2.0","id":2,"result":{"prompts":[{"name":"summarize"}]}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	prompts, err := (StdioClient{Command: script}).ListPrompts(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 || prompts[0].Name != "summarize" {
		t.Fatalf("prompts=%#v", prompts)
	}
	script = filepath.Join(t.TempDir(), "prompt.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
IFS= read init
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
IFS= read initialized
IFS= read get
printf '{"jsonrpc":"2.0","id":2,"result":{"name":"summarize","messages":[{"role":"user","content":{"type":"text","text":"Summarize"}}]}}\n'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt, err := (StdioClient{Command: script}).GetPrompt(context.Background(), ExecutionContext{}, "srv", "summarize", nil)
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Name != "summarize" || len(prompt.Messages) != 1 {
		t.Fatalf("prompt=%#v", prompt)
	}
}
