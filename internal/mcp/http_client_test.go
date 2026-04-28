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

func TestHTTPClientSSESetsAcceptHeader(t *testing.T) {
	var sawAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"result\":{\"ok\":true}}\n\n"))
	}))
	defer server.Close()
	_, err := (HTTPClient{BaseURL: server.URL, SSE: true}).CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sawAccept != "text/event-stream" {
		t.Fatalf("accept=%q", sawAccept)
	}
}

func TestHTTPClientSSEParsesEventData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: {\"result\":{\"ok\":true}}\n\n"))
	}))
	defer server.Close()
	got, err := (HTTPClient{BaseURL: server.URL, SSE: true}).CallTool(context.Background(), ExecutionContext{}, "srv", "tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true {
		t.Fatalf("got=%#v", got)
	}
}

func TestHTTPClientSSEParsesToolListEventData(t *testing.T) {
	var sawAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"tools\":[{\"name\":\"lookup\",\"description\":\"Lookup\"}]}\n\n"))
	}))
	defer server.Close()
	tools, err := (HTTPClient{BaseURL: server.URL, SSE: true}).ListTools(context.Background(), ExecutionContext{}, "srv")
	if err != nil {
		t.Fatal(err)
	}
	if sawAccept != "text/event-stream" || len(tools) != 1 || tools[0].Name != "lookup" {
		t.Fatalf("accept=%q tools=%#v", sawAccept, tools)
	}
}
