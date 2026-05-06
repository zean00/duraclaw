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
		ModelConfig: map[string]any{"primary": "mock/duraclaw", "fallbacks": []string{"mock/other"}, "options": map[string]any{"max_tokens": 256}},
		ToolConfig: map[string]any{
			"allowed_tools": []string{"echo"}, "max_iterations": 4, "max_tool_calls_per_run": 2,
			"interleave_tool_calls": true,
			"tool_aliases":          map[string]any{"duraclaw.ask_user": "duraclaw_ask_user"},
			"tool_metadata": map[string]any{"echo": map[string]any{
				"tags":             []string{"debug"},
				"trigger_phrases":  []string{"ping"},
				"negative_phrases": []string{"ignore"},
				"side_effect":      "read",
				"conflicts_with":   []string{"remember"},
			}},
		},
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
			"tool_selection": map[string]any{
				"enabled":              true,
				"mode":                 "hybrid",
				"model":                "openrouter/openai/gpt-4.1-mini",
				"max_tools":            6,
				"confidence_threshold": 0.65,
				"options": map[string]any{
					"max_tokens": 128,
					"reasoning":  map[string]any{"max_tokens": 64},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidModelOptions(t *testing.T) {
	err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
		CustomerID: "c", AgentInstanceID: "a",
		ModelConfig: map[string]any{"options": "bad"},
	})
	if err == nil {
		t.Fatal("expected validation error")
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
	cases := []map[string]any{
		{"allowed_tools": "echo", "max_iterations": -1},
		{"interleave_tool_calls": "yes"},
	}
	for _, toolConfig := range cases {
		err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
			CustomerID: "c", AgentInstanceID: "a",
			ToolConfig: toolConfig,
		})
		if err == nil {
			t.Fatalf("expected validation error for %#v", toolConfig)
		}
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidToolSelectionValues(t *testing.T) {
	cases := []map[string]any{
		{"tool_selection": "bad"},
		{"tool_selection": map[string]any{"enabled": "yes"}},
		{"tool_selection": map[string]any{"mode": "always"}},
		{"tool_selection": map[string]any{"max_tools": -1}},
		{"tool_selection": map[string]any{"max_tools": 1.5}},
		{"tool_selection": map[string]any{"confidence_threshold": 2}},
		{"tool_selection": map[string]any{"options": "bad"}},
	}
	for _, profileConfig := range cases {
		err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
			CustomerID: "c", AgentInstanceID: "a", ProfileConfig: profileConfig,
		})
		if err == nil {
			t.Fatalf("expected validation error for %#v", profileConfig)
		}
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidToolMetadata(t *testing.T) {
	cases := []map[string]any{
		{"tool_metadata": "bad"},
		{"tool_metadata": map[string]any{"": map[string]any{}}},
		{"tool_metadata": map[string]any{"echo": "bad"}},
		{"tool_metadata": map[string]any{"echo": map[string]any{"tags": "debug"}}},
		{"tool_metadata": map[string]any{"echo": map[string]any{"trigger_phrases": "ping"}}},
		{"tool_metadata": map[string]any{"echo": map[string]any{"negative_phrases": "ignore"}}},
		{"tool_metadata": map[string]any{"echo": map[string]any{"side_effect": true}}},
	}
	for _, toolConfig := range cases {
		err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
			CustomerID: "c", AgentInstanceID: "a", ToolConfig: toolConfig,
		})
		if err == nil {
			t.Fatalf("expected validation error for %#v", toolConfig)
		}
	}
}

func TestValidateAgentInstanceVersionSpecRejectsInvalidToolAliases(t *testing.T) {
	cases := []map[string]any{
		{"tool_aliases": "bad"},
		{"tool_aliases": map[string]any{"duraclaw.ask_user": "duraclaw.ask_user"}},
		{"tool_aliases": map[string]any{"duraclaw.ask_user": "same", "duraclaw.run_workflow": "same"}},
	}
	for _, toolConfig := range cases {
		err := ValidateAgentInstanceVersionSpecForTest(AgentInstanceVersionSpec{
			CustomerID: "c", AgentInstanceID: "a", ToolConfig: toolConfig,
		})
		if err == nil {
			t.Fatalf("expected validation error for %#v", toolConfig)
		}
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
