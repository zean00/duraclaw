package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func intPtr(v int) *int { return &v }

func newMockStore(t *testing.T) (*Store, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mock.Close)
	return NewStore(mock), mock
}

func expectRetentionExec(mock pgxmock.PgxPoolIface, sql string, cutoff time.Time, rows int64) {
	mock.ExpectExec(sql).WithArgs(cutoff).WillReturnResult(pgxmock.NewResult("DELETE", rows))
}

func TestStoreRetentionMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	cutoff := time.Unix(1700000000, 0).UTC()

	mock.ExpectExec("UPDATE artifacts").WithArgs(cutoff).WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	expectRetentionExec(mock, "DELETE FROM run_events", cutoff, 3)
	expectRetentionExec(mock, "DELETE FROM async_outbox", cutoff, 4)
	expectRetentionExec(mock, "DELETE FROM observability_events", cutoff, 5)
	expectRetentionExec(mock, "DELETE FROM broadcasts", cutoff, 6)
	expectRetentionExec(mock, "DELETE FROM async_write_jobs", cutoff, 7)

	cases := []struct {
		name string
		run  func(context.Context, time.Time) (int64, error)
		want int64
	}{
		{"artifacts", store.ExpireArtifactsOlderThan, 2},
		{"run_events", store.DeleteRunEventsOlderThan, 3},
		{"outbox", store.DeleteCompletedOutboxOlderThan, 4},
		{"observability", store.DeleteObservabilityEventsOlderThan, 5},
		{"broadcasts", store.DeleteTerminalBroadcastsOlderThan, 6},
		{"async_writes", store.DeleteTerminalAsyncWriteJobsOlderThan, 7},
	}
	for _, tc := range cases {
		got, err := tc.run(ctx, cutoff)
		if err != nil || got != tc.want {
			t.Fatalf("%s got=%d err=%v", tc.name, got, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRuntimeLimitsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	active, queued, buffer := 2, 3, 25

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO customer_runtime_limits").
		WithArgs("c1", pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, nil, nil, &buffer, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	limits, err := store.UpsertCustomerRuntimeLimits(ctx, RuntimeLimits{
		CustomerID:      "c1",
		MaxActiveRuns:   &active,
		MaxQueuedRuns:   &queued,
		AsyncBufferSize: &buffer,
	})
	if err != nil || limits.CustomerID != "c1" || limits.MaxActiveRuns == nil || *limits.MaxActiveRuns != active {
		t.Fatalf("limits=%#v err=%v", limits, err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, nil, nil, &buffer, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	got, err := store.CustomerRuntimeLimits(ctx, "c1")
	if err != nil || got.CustomerID != "c1" || got.AsyncBufferSize == nil || *got.AsyncBufferSize != buffer {
		t.Fatalf("customer limits=%#v err=%v", got, err)
	}

	if _, err := store.UpsertCustomerRuntimeLimits(ctx, RuntimeLimits{}); !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if err := validateRuntimeLimits(RuntimeLimits{MaxActiveRuns: intPtr(-1)}); !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreEffectiveRuntimeLimitsAndQuotasWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	active, queued, workflow, background := 2, 1, 1, 1

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, &workflow, &background, nil, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "agent_instance_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", "a1", nil, nil, nil, nil, intPtr(50), nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	effective, err := store.EffectiveRuntimeLimits(ctx, "c1", "a1")
	if err != nil || effective.MaxActiveRuns != active || effective.AsyncBufferSize != 50 {
		t.Fatalf("effective=%#v err=%v", effective, err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, &workflow, &background, nil, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT count").WithArgs("c1", "a1", []string{"queued"}).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	if err := store.EnforceRunQuota(ctx, "c1", "a1"); !IsQuotaExceeded(err) {
		t.Fatalf("expected quota exceeded, got %v", err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", &active, &queued, &workflow, &background, nil, nil, nil, nil, nil, nil, nil, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT count").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	if err := store.EnforceWorkflowQuota(ctx, "c1", "a1"); !IsQuotaExceeded(err) {
		t.Fatalf("expected workflow quota exceeded, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreModelUsageQuotaWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	dailyTokens := 10
	dailyCost := int64(100)

	mock.ExpectQuery("SELECT customer_id").WithArgs("c1").
		WillReturnRows(pgxmock.NewRows([]string{
			"customer_id", "max_active_runs", "max_queued_runs", "max_workflow_runs", "max_background_runs",
			"async_buffer_size", "max_async_payload_bytes", "async_degrade_threshold_bytes",
			"max_daily_tokens", "max_weekly_tokens", "max_monthly_tokens", "max_daily_model_cost_micros", "max_weekly_model_cost_micros", "max_monthly_model_cost_micros",
			"metadata", "updated_at",
		}).AddRow("c1", nil, nil, nil, nil, nil, nil, nil, &dailyTokens, nil, nil, &dailyCost, nil, nil, []byte(`{}`), now))
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "a1").WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery("SELECT COALESCE\\(sum\\(total_tokens\\),0\\)").WithArgs("c1", "a1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"sum"}).AddRow(int64(10)))
	if err := store.EnforceModelUsageQuota(ctx, "c1", "a1"); !IsQuotaExceeded(err) {
		t.Fatalf("expected token quota exceeded, got %v", err)
	}

	mock.ExpectExec("INSERT INTO model_usage_ledger").WithArgs("c1", "a1", "run-1", "call-1", "openrouter", "model", 1, 2, 3, int64(4)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.RecordModelUsage(ctx, ModelUsage{CustomerID: "c1", AgentInstanceID: "a1", RunID: "run-1", ModelCallID: "call-1", Provider: "openrouter", Model: "model", InputTokens: 1, OutputTokens: 2, CostMicros: 4}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreOutboundIntentWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	runID := "run-1"

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO outbound_intents").
		WithArgs("c1", "u1", "s1", runID, "email", []byte(`{"subject":"hi"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("intent-1"))
	mock.ExpectQuery("INSERT INTO async_outbox").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(99)))
	mock.ExpectExec("UPDATE outbound_intents").WithArgs("intent-1", int64(99)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	id, outboxID, err := store.CreateOutboundIntent(ctx, OutboundIntent{
		CustomerID: "c1", UserID: "u1", SessionID: "s1", RunID: &runID, Type: "email", Payload: json.RawMessage(`{"subject":"hi"}`),
	})
	if err != nil || id != "intent-1" || outboxID != 99 {
		t.Fatalf("id=%q outbox=%d err=%v", id, outboxID, err)
	}

	now := time.Now().UTC()
	outboxIDPtr := int64(99)
	mock.ExpectQuery("SELECT id").WithArgs("c1", 100, "sent_to_nexus").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "run_id", "intent_type", "payload", "status", "outbox_id", "created_at", "updated_at"}).
			AddRow("intent-1", "c1", "u1", "s1", &runID, "email", []byte(`{}`), "sent_to_nexus", &outboxIDPtr, now, now))
	intents, err := store.ListOutboundIntents(ctx, "c1", "sent", 0)
	if err != nil || len(intents) != 1 || intents[0].Status != "sent_to_nexus" {
		t.Fatalf("intents=%#v err=%v", intents, err)
	}
	if _, err := NormalizeOutboundIntentStatus("bogus"); err == nil {
		t.Fatal("expected invalid status error")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
