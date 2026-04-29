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
		PolicyConfig:   map[string]any{"instructions": []string{"be concise"}, "blocked_terms": []string{"secret"}, "policy_pack_ids": []string{"pack-1"}},
		ProfileConfig: map[string]any{
			"personality":           "direct",
			"communication_style":   "concise",
			"language_capabilities": []string{"en", "id"},
			"domain_scope": map[string]any{
				"allowed_domains":       []string{"support"},
				"forbidden_domains":     []string{"legal advice"},
				"out_of_scope_guidance": "decline briefly",
			},
			"recommendation": map[string]any{
				"enabled":          true,
				"timeout_ms":       1500,
				"model":            "openrouter/qwen/qwen3.6-35b-a3b",
				"merge_model":      "openrouter/openai/gpt-4.1-mini",
				"max_candidates":   5,
				"allow_sponsored":  true,
				"disclosure_style": "soft",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentInstanceVersionSpecRejectsEnabledRecommendationWithoutTimeout(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		ProfileConfig: map[string]any{"recommendation": map[string]any{"enabled": true}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidProfileConfigValues(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		ProfileConfig: map[string]any{"domain_scope": map[string]any{"allowed_domains": "support"}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidPolicyConfigValues(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		PolicyConfig: map[string]any{"policy_pack_ids": "pack-1"},
	})
	if err == nil {
		t.Fatal("expected validation error")
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
