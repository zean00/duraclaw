package main

import "testing"

func TestParseAgentConfigFlags(t *testing.T) {
	got := parseAgentConfigFlags([]string{"--file", "agent.yaml", "--format=json", "--activate"})
	if got["file"] != "agent.yaml" || got["format"] != "json" || got["activate"] != "true" {
		t.Fatalf("got=%#v", got)
	}
}

func TestFormatFromPath(t *testing.T) {
	if got := formatFromPath("agent.yaml"); got != "yaml" {
		t.Fatalf("yaml got %q", got)
	}
	if got := formatFromPath("agent.json"); got != "json" {
		t.Fatalf("json got %q", got)
	}
	if got := formatFromPath("agent"); got != "" {
		t.Fatalf("empty got %q", got)
	}
}
