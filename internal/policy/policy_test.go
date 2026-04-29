package policy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"duraclaw/internal/db"
)

func TestAllowArtifact(t *testing.T) {
	rule := ArtifactRule{MaxSizeBytes: 10, MediaTypes: map[string]bool{"text/plain": true}}
	if err := AllowArtifact(9, "text/plain", rule); err != nil {
		t.Fatal(err)
	}
	if err := AllowArtifact(11, "text/plain", rule); err == nil {
		t.Fatalf("expected size denial")
	}
	if err := AllowArtifact(1, "image/png", rule); err == nil {
		t.Fatalf("expected media type denial")
	}
}

func TestRejectRawArtifactMetadata(t *testing.T) {
	if err := RejectRawArtifactMetadata(map[string]any{"safe": "value"}); err != nil {
		t.Fatal(err)
	}
	if err := RejectRawArtifactMetadata(map[string]any{"nested": map[string]any{"base64": "AAAA"}}); err == nil {
		t.Fatalf("expected raw payload metadata denial")
	}
}

type fakeRuleStore struct {
	rules       []db.PolicyRule
	pinnedRules []db.PolicyRule
	pinnedIDs   []string
	evals       []db.PolicyEvaluation
}

func (s *fakeRuleStore) PolicyRulesForScope(context.Context, string, string, string) ([]db.PolicyRule, error) {
	return s.rules, nil
}

func (s *fakeRuleStore) PolicyRulesForScopeAndPacks(_ context.Context, _, _, _ string, policyPackIDs []string) ([]db.PolicyRule, error) {
	s.pinnedIDs = policyPackIDs
	return s.pinnedRules, nil
}

func (s *fakeRuleStore) RecordPolicyEvaluation(_ context.Context, ev db.PolicyEvaluation) error {
	s.evals = append(s.evals, ev)
	return nil
}

func TestEngineDenyWinsAndRecordsEvaluation(t *testing.T) {
	condition := json.RawMessage(`{"contains":{"key":"content","value":"secret"}}`)
	store := &fakeRuleStore{rules: []db.PolicyRule{
		{ID: "allow", PolicyPackID: "pack", RuleType: "allow", EnforcementMode: "pre_model", Action: "allow", InstructionText: "ok"},
		{ID: "deny", PolicyPackID: "pack", RuleType: "deny", EnforcementMode: "pre_model", Action: "deny", Condition: condition, InstructionText: "no secrets"},
	}}
	decision, err := NewEngine(store).Evaluate(context.Background(), "pre_model", Context{RunID: "00000000-0000-0000-0000-000000000001", Content: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "deny" || decision.Reason != "no secrets" {
		t.Fatalf("decision=%#v", decision)
	}
	if len(store.evals) != 2 || store.evals[1].Decision != "deny" {
		t.Fatalf("evals=%#v", store.evals)
	}
}

func TestPromptInstructionsReturnsMatchedInstructions(t *testing.T) {
	store := &fakeRuleStore{rules: []db.PolicyRule{{ID: "r", PolicyPackID: "p", RuleType: "style", EnforcementMode: "prompt", Action: "modify", InstructionText: "Be concise."}}}
	got, err := NewEngine(store).PromptInstructions(context.Background(), Context{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "Be concise." {
		t.Fatalf("got=%#v", got)
	}
}

func TestEngineUsesPinnedPolicyPacksWhenPresent(t *testing.T) {
	store := &fakeRuleStore{
		rules:       []db.PolicyRule{{ID: "global", PolicyPackID: "global", RuleType: "style", EnforcementMode: "prompt", Action: "modify", InstructionText: "global"}},
		pinnedRules: []db.PolicyRule{{ID: "pinned", PolicyPackID: "pack-1", RuleType: "style", EnforcementMode: "prompt", Action: "modify", InstructionText: "pinned"}},
	}
	got, err := NewEngine(store).PromptInstructions(context.Background(), Context{PolicyPackIDs: []string{"pack-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "pinned" || len(store.pinnedIDs) != 1 || store.pinnedIDs[0] != "pack-1" {
		t.Fatalf("got=%#v pinned=%#v", got, store.pinnedIDs)
	}
}

func TestRuleMatchesCompositeConditions(t *testing.T) {
	condition := json.RawMessage(`{"all":[{"prefix":{"key":"tool_name","value":"duraclaw."}},{"in":{"key":"workflow_id","values":["wf-1","wf-2"]}},{"not":{"contains":{"key":"content","value":"blocked"}}}]}`)
	store := &fakeRuleStore{rules: []db.PolicyRule{{ID: "r", PolicyPackID: "p", RuleType: "deny", EnforcementMode: "pre_tool", Action: "deny", Condition: condition}}}
	decision, err := NewEngine(store).Evaluate(context.Background(), "pre_tool", Context{ToolName: "duraclaw.run_workflow", WorkflowID: "wf-1", Content: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "deny" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRuleMatchesRegexCondition(t *testing.T) {
	condition := json.RawMessage(`{"matches":{"key":"content","pattern":"invoice-[0-9]+"}}`)
	store := &fakeRuleStore{rules: []db.PolicyRule{{ID: "r", PolicyPackID: "p", RuleType: "modify", EnforcementMode: "prompt", Action: "modify", Condition: condition}}}
	decision, err := NewEngine(store).Evaluate(context.Background(), "prompt", Context{Content: "invoice-123"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "modify" {
		t.Fatalf("decision=%#v", decision)
	}
}

func TestRedactContextPayload(t *testing.T) {
	pc := Context{
		Content: "email a@example.com token: abc123",
		AdditionalFields: map[string]any{
			"authorization": "Bearer secret",
			"nested":        map[string]any{"note": "card 4111 1111 1111 1111"},
		},
	}.Redacted()
	if strings.Contains(pc.Content, "a@example.com") || strings.Contains(pc.Content, "abc123") {
		t.Fatalf("content not redacted: %q", pc.Content)
	}
	if pc.AdditionalFields["authorization"] != "[REDACTED]" {
		t.Fatalf("secret key not redacted: %#v", pc.AdditionalFields)
	}
	nested := pc.AdditionalFields["nested"].(map[string]any)
	if strings.Contains(nested["note"].(string), "4111") {
		t.Fatalf("nested value not redacted: %#v", nested)
	}
}
