package policy

import (
	"context"
	"encoding/json"
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
	rules []db.PolicyRule
	evals []db.PolicyEvaluation
}

func (s *fakeRuleStore) PolicyRulesForScope(context.Context, string, string, string) ([]db.PolicyRule, error) {
	return s.rules, nil
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
