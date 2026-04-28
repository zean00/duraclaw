package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientPropagatesHeaders(t *testing.T) {
	var sawRunID, sawToolCallID, sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRunID = r.Header.Get("X-Run-ID")
		sawToolCallID = r.Header.Get("X-Tool-Call-ID")
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"ok": true}})
	}))
	defer server.Close()

	got, err := (HTTPClient{BaseURL: server.URL, Token: "tok"}).CallTool(context.Background(), ExecutionContext{
		CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", RunID: "r", ToolCallID: "tc", RequestID: "req",
	}, "srv", "tool", map[string]any{"x": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if sawRunID != "r" || sawToolCallID != "tc" || sawAuth != "Bearer tok" || got["ok"] != true {
		t.Fatalf("headers run=%q tool=%q auth=%q got=%#v", sawRunID, sawToolCallID, sawAuth, got)
	}
}

func TestHTTPClientListsTools(t *testing.T) {
	var sawRunID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRunID = r.Header.Get("X-Run-ID")
		_ = json.NewEncoder(w).Encode(map[string]any{"tools": []map[string]any{{"name": "lookup", "description": "Lookup"}}})
	}))
	defer server.Close()
	tools, err := (HTTPClient{BaseURL: server.URL}).ListTools(context.Background(), ExecutionContext{RunID: "r"}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if sawRunID != "r" || len(tools) != 1 || tools[0].Name != "lookup" {
		t.Fatalf("run=%q tools=%#v", sawRunID, tools)
	}
}
