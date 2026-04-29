package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreOutboundStatusAndAgentLimitsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	runID := "run-1"
	active, background := 2, 1

	mock.ExpectBegin()
	mock.ExpectQuery("UPDATE outbound_intents").WithArgs("intent-1", "c1", "delivered").WillReturnRows(pgxmock.NewRows([]string{"run_id"}).AddRow(&runID))
	mock.ExpectExec("UPDATE broadcast_targets").WithArgs("intent-1", "delivered").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE broadcasts b").WithArgs("intent-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", &runID, "outbound.delivered", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	if err := store.SetOutboundIntentStatus(ctx, "intent-1", "c1", "delivered"); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO agent_instances").WithArgs("c1", "a1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO agent_instance_runtime_limits").
		WithArgs("c1", "a1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "agent_instance_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes", "metadata", "updated_at",
		}).AddRow("c1", "a1", &active, nil, nil, &background, nil, nil, nil, []byte(`{}`), now))
	limits, err := store.UpsertAgentInstanceRuntimeLimits(ctx, RuntimeLimits{CustomerID: "c1", AgentInstanceID: "a1", MaxActiveRuns: &active, MaxBackgroundRuns: &background})
	if err != nil || limits.AgentInstanceID != "a1" {
		t.Fatalf("limits=%#v err=%v", limits, err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs", "async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes", "metadata", "updated_at"}).
			AddRow("c1", nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs", "async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes", "metadata", "updated_at"}).
			AddRow("c1", "a1", &active, nil, nil, &background, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT count").WithArgs("c1", "a1", "run-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	if err := store.EnforceRunStartQuota(ctx, "run-1", "c1", "a1"); !IsQuotaExceeded(err) {
		t.Fatalf("expected run start quota, got %v", err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs", "async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes", "metadata", "updated_at"}).
			AddRow("c1", nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs", "async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes", "metadata", "updated_at"}).
			AddRow("c1", "a1", &active, nil, nil, &background, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT count").WithArgs("c1", "a1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	if err := store.EnforceBackgroundQuota(ctx, "c1", "a1"); !IsQuotaExceeded(err) {
		t.Fatalf("expected background quota, got %v", err)
	}

	if (QuotaExceededError{Kind: "x", Limit: 1, Count: 2}).Error() == "" || (ValidationError{Message: "bad"}).Error() != "bad" {
		t.Fatal("unexpected error strings")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStorePolicyPackVersionAndDiffWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT name").WithArgs("pack-1").WillReturnRows(pgxmock.NewRows([]string{"name", "owner_scope"}).AddRow("baseline", "customer"))
	mock.ExpectQuery("INSERT INTO policy_packs").WithArgs("baseline", 2, "draft", "customer").WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("pack-2"))
	mock.ExpectExec("INSERT INTO policy_rules").WithArgs("pack-2", "pack-1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	versionID, err := store.CreatePolicyPackVersion(ctx, "pack-1", 2, "")
	if err != nil || versionID != "pack-2" {
		t.Fatalf("versionID=%q err=%v", versionID, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("pack-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "owner_scope", "created_at"}).AddRow("pack-1", "baseline", 1, "active", "customer", now))
	mock.ExpectQuery("SELECT id").WithArgs("baseline").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "owner_scope", "created_at"}).AddRow("pack-2", "baseline", 2, "draft", "customer", now))
	versions, err := store.PolicyPackVersions(ctx, "pack-1")
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("pack-1").
		WillReturnRows(policyRuleRows().AddRow("rule-1", "pack-1", "artifact", "block", 10, []byte(`{}`), "deny", "old", "active", now))
	mock.ExpectQuery("SELECT id").WithArgs("pack-2").
		WillReturnRows(policyRuleRows().
			AddRow("rule-1b", "pack-2", "artifact", "block", 10, []byte(`{}`), "deny", "new", "active", now).
			AddRow("rule-2", "pack-2", "tool", "warn", 1, []byte(`{}`), "allow", "added", "active", now))
	diff, err := store.PolicyPackDiff(ctx, "pack-1", "pack-2")
	if err != nil || len(diff.Modified) != 1 || len(diff.Added) != 1 {
		t.Fatalf("diff=%#v err=%v", diff, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreStartWorkflowRunAndResumeTimerWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id", "agent_instance_id"}).AddRow("c1", "a1"))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("INSERT INTO workflow_runs").WithArgs("run-1", "wf-1", 1, "start", []byte(`{"input":true}`)).WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("wfr-1"))
	mock.ExpectExec("UPDATE runs").WithArgs("run-1", "running_workflow", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "run.running_workflow", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "run_state_changed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "workflow.running", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	workflowRunID, err := store.StartWorkflowRun(ctx, "run-1", "wf-1", 1, "start", map[string]bool{"input": true})
	if err != nil || workflowRunID != "wfr-1" {
		t.Fatalf("workflowRunID=%q err=%v", workflowRunID, err)
	}

	if err := store.ResumeWorkflowTimer(ctx, "", "wfr-1", "timer", nil); err == nil {
		t.Fatal("expected resume validation error")
	}
	mock.ExpectExec("UPDATE workflow_node_states").WithArgs("wfr-1", "timer", "succeeded", []byte(`{"timer":{"done":true}}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE workflow_runs").WithArgs("wfr-1", "running", "timer", []byte(`{"timer":{"done":true}}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "workflow.running", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE runs").WithArgs("run-1", "queued", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "run.queued", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "run_state_changed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "workflow.timer_resumed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.ResumeWorkflowTimer(ctx, "run-1", "wfr-1", "timer", map[string]any{"done": true}); err != nil {
		t.Fatal(err)
	}

	_ = now
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
