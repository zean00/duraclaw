package db

import "testing"

func TestValidateAgentInstanceVersionSpecRejectsUnknownConfigKeys(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a", ToolConfig: map[string]any{"unknown": true},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAgentInstanceVersionSpecAllowsKnownConfigKeys(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		ModelConfig:    map[string]any{"primary": "mock/duraclaw", "fallbacks": []string{"mock/other"}},
		ToolConfig:     map[string]any{"allowed_tools": []string{"echo"}, "max_iterations": 4, "max_tool_calls_per_run": 2},
		MCPConfig:      map[string]any{"servers": []map[string]any{{"name": "srv", "transport": "http", "base_url": "http://example.test"}}},
		WorkflowConfig: map[string]any{"disabled_workflows": []string{"wf"}},
		PolicyConfig:   map[string]any{"instructions": []string{"be concise"}, "blocked_terms": []string{"secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidToolConfigValues(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		ToolConfig: map[string]any{"allowed_tools": "echo", "max_iterations": -1},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidMCPServer(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		MCPConfig: map[string]any{"servers": []map[string]any{{"name": "srv", "transport": "http"}}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}
