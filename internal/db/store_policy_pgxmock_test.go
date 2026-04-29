package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func policyRuleRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "policy_pack_id", "rule_type", "enforcement_mode", "priority", "condition", "action", "instruction_text", "status", "created_at"})
}

func TestStorePolicyPackAndRuleMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectExec("INSERT INTO agent_policies").WithArgs("c1", "a1", int64(1024), []byte(`["image/png"]`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.UpsertAgentPolicy(ctx, AgentPolicy{CustomerID: "c1", AgentInstanceID: "a1", ArtifactMaxSizeBytes: 1024, ArtifactMediaTypes: []string{"image/png"}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "artifact_max_size_bytes", "artifact_media_types"}).
			AddRow("c1", "a1", int64(1024), []byte(`["image/png"]`)))
	policy, err := store.AgentPolicy(ctx, "c1", "a1")
	if err != nil || len(policy.ArtifactMediaTypes) != 1 {
		t.Fatalf("policy=%#v err=%v", policy, err)
	}

	mock.ExpectQuery("INSERT INTO policy_packs").WithArgs("baseline", 1, "customer").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("pack-1"))
	packID, err := store.CreatePolicyPack(ctx, "baseline", 1, "")
	if err != nil || packID != "pack-1" {
		t.Fatalf("packID=%q err=%v", packID, err)
	}
	if _, err := store.CreatePolicyPack(ctx, "", 0, ""); err == nil {
		t.Fatal("expected invalid policy pack")
	}

	mock.ExpectExec("UPDATE policy_packs").WithArgs("pack-1", "active").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetPolicyPackStatus(ctx, "pack-1", "active"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPolicyPackStatus(ctx, "pack-1", "archived"); err == nil {
		t.Fatal("expected invalid status")
	}

	mock.ExpectQuery("SELECT id").WithArgs(100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "owner_scope", "created_at"}).
			AddRow("pack-1", "baseline", 1, "active", "customer", now))
	packs, err := store.ListPolicyPacks(ctx, 0)
	if err != nil || len(packs) != 1 {
		t.Fatalf("packs=%#v err=%v", packs, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("pack-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "owner_scope", "created_at"}).
			AddRow("pack-1", "baseline", 1, "active", "customer", now))
	pack, err := store.PolicyPack(ctx, "pack-1")
	if err != nil || pack.Name != "baseline" {
		t.Fatalf("pack=%#v err=%v", pack, err)
	}

	mock.ExpectQuery("INSERT INTO policy_rules").
		WithArgs("pack-1", "artifact", "block", 10, json.RawMessage(`{}`), "deny", "no pii", "active").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("rule-1"))
	ruleID, err := store.UpsertPolicyRule(ctx, PolicyRule{PolicyPackID: "pack-1", RuleType: "artifact", EnforcementMode: "block", Priority: 10, Action: "deny", InstructionText: "no pii"})
	if err != nil || ruleID != "rule-1" {
		t.Fatalf("ruleID=%q err=%v", ruleID, err)
	}

	mock.ExpectQuery("UPDATE policy_rules").
		WithArgs("rule-1", "pack-1", "artifact", "block", 20, json.RawMessage(`{"x":true}`), "deny", "updated", "disabled").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("rule-1"))
	ruleID, err = store.UpsertPolicyRule(ctx, PolicyRule{ID: "rule-1", PolicyPackID: "pack-1", RuleType: "artifact", EnforcementMode: "block", Priority: 20, Condition: json.RawMessage(`{"x":true}`), Action: "deny", InstructionText: "updated", Status: "disabled"})
	if err != nil || ruleID != "rule-1" {
		t.Fatalf("ruleID=%q err=%v", ruleID, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("pack-1").
		WillReturnRows(policyRuleRows().AddRow("rule-1", "pack-1", "artifact", "block", 20, []byte(`{"x":true}`), "deny", "updated", "disabled", now))
	rules, err := store.ListPolicyRules(ctx, "pack-1")
	if err != nil || len(rules) != 1 || rules[0].ID != "rule-1" {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}

	mock.ExpectQuery("INSERT INTO policy_assignments").WithArgs("pack-1", "c1", "a1", true).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("assignment-1"))
	assignmentID, err := store.AssignPolicyPack(ctx, "pack-1", "c1", "a1", true)
	if err != nil || assignmentID != "assignment-1" {
		t.Fatalf("assignmentID=%q err=%v", assignmentID, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorePolicyRuleQueriesAndEvaluationsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	runID := "run-1"

	mock.ExpectQuery("SELECT r.id").WithArgs("c1", "a1", "block").
		WillReturnRows(policyRuleRows().AddRow("rule-1", "pack-1", "artifact", "block", 10, []byte(`{}`), "deny", "no pii", "active", now))
	rules, err := store.PolicyRulesForScope(ctx, "c1", "a1", "block")
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}

	mock.ExpectQuery("SELECT r.id").WithArgs("block", []string{"pack-1"}, "c1", "a1").
		WillReturnRows(policyRuleRows().AddRow("rule-1", "pack-1", "artifact", "block", 10, []byte(`{}`), "deny", "no pii", "active", now))
	rules, err = store.PolicyRulesForScopeAndPacks(ctx, "c1", "a1", "block", []string{"pack-1"})
	if err != nil || len(rules) != 1 {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}

	mock.ExpectExec("INSERT INTO policy_evaluations").WithArgs(runID, nil, nil, "node", nil, "rule-1", "block", "deny", "reason", json.RawMessage(`{}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.RecordPolicyEvaluation(ctx, PolicyEvaluation{RunID: &runID, WorkflowNodeKey: "node", PolicyRuleID: ptrString("rule-1"), EnforcementMode: "block", Decision: "deny", Reason: "reason"}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT id").WithArgs(100, "run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "step_id", "workflow_run_id", "workflow_node_key", "policy_pack_id", "policy_rule_id", "enforcement_mode", "decision", "reason", "payload", "created_at"}).
			AddRow(int64(1), &runID, nil, nil, "node", nil, ptrString("rule-1"), "block", "deny", "reason", []byte(`{}`), now))
	evaluations, err := store.ListPolicyEvaluations(ctx, "run-1", 0)
	if err != nil || len(evaluations) != 1 || evaluations[0].ID != 1 {
		t.Fatalf("evaluations=%#v err=%v", evaluations, err)
	}

	if nullableStringPtr(nil) != nil || nullableStringPtr(ptrString("")) != nil || nullableStringPtr(ptrString("x")) != "x" {
		t.Fatal("unexpected nullable string behavior")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func ptrString(value string) *string { return &value }
