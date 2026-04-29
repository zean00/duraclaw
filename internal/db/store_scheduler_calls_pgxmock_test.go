package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func schedulerRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "customer_id", "job_type", "schedule", "next_run_at", "payload", "enabled", "lease_owner", "lease_expires_at", "last_fired_at", "metadata"})
}

func expectRunEventAndObservability(mock pgxmock.PgxPoolIface, runID, eventType string) {
	mock.ExpectExec("INSERT INTO run_events").WithArgs(runID, eventType, pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs(runID).WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", runID, eventType, pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
}

func TestStoreProcessorModelAndMCPCallsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	errText := errors.New("boom").Error()

	mock.ExpectQuery("INSERT INTO processor_calls").WithArgs("run-1", "art-1", "ocr", []byte(`{"mode":"fast"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("proc-1"))
	expectRunEventAndObservability(mock, "run-1", "processor.started")
	procID, err := store.StartProcessorCall(ctx, "run-1", "art-1", "ocr", map[string]string{"mode": "fast"})
	if err != nil || procID != "proc-1" {
		t.Fatalf("procID=%q err=%v", procID, err)
	}
	mock.ExpectExec("UPDATE processor_calls").WithArgs("proc-1", "failed", []byte(`{"partial":true}`), &errText).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectRunEventAndObservability(mock, "run-1", "processor.failed")
	if err := store.CompleteProcessorCall(ctx, "proc-1", "run-1", map[string]bool{"partial": true}, &errText); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("INSERT INTO model_calls").WithArgs("run-1", "openai", "gpt", []byte(`{"prompt":"hi"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("model-1"))
	expectRunEventAndObservability(mock, "run-1", "model.started")
	modelID, err := store.StartModelCall(ctx, "run-1", "openai", "gpt", map[string]string{"prompt": "hi"})
	if err != nil || modelID != "model-1" {
		t.Fatalf("modelID=%q err=%v", modelID, err)
	}
	mock.ExpectExec("UPDATE model_calls").WithArgs("model-1", "succeeded", []byte(`{"text":"ok"}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectRunEventAndObservability(mock, "run-1", "model.succeeded")
	if err := store.CompleteModelCall(ctx, "model-1", "run-1", map[string]string{"text": "ok"}, nil); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("INSERT INTO mcp_calls").WithArgs("run-1", "srv", "tool", []byte(`{"arg":1}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("mcp-1"))
	expectRunEventAndObservability(mock, "run-1", "mcp.started")
	mcpID, err := store.StartMCPCall(ctx, "run-1", "srv", "tool", map[string]int{"arg": 1})
	if err != nil || mcpID != "mcp-1" {
		t.Fatalf("mcpID=%q err=%v", mcpID, err)
	}
	mock.ExpectExec("UPDATE mcp_calls").WithArgs("mcp-1", "succeeded", []byte(`{"ok":true}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectRunEventAndObservability(mock, "run-1", "mcp.succeeded")
	if err := store.CompleteMCPCall(ctx, "mcp-1", "run-1", map[string]bool{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSchedulerJobMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	owner := "worker"

	mock.ExpectQuery("UPDATE scheduler_jobs").WithArgs(1, owner, "60.000000 seconds").
		WillReturnRows(schedulerRows().AddRow("job-1", "c1", "cron", "* * * * *", later, []byte(`{}`), true, &owner, &later, nil, []byte(`{}`)))
	jobs, err := store.ClaimDueSchedulerJobs(ctx, owner, 1, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "job-1" {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", 100).
		WillReturnRows(schedulerRows().AddRow("job-1", "c1", "cron", "* * * * *", later, []byte(`{}`), true, nil, nil, nil, []byte(`{}`)))
	listed, err := store.ListSchedulerJobs(ctx, "c1", 0)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}

	mock.ExpectExec("UPDATE scheduler_jobs").WithArgs("job-1", "c1", false).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetSchedulerJobEnabled(ctx, "job-1", "c1", false); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", "u1", 100).
		WillReturnRows(schedulerRows().AddRow("job-1", "c1", "cron", "* * * * *", later, []byte(`{"user_id":"u1"}`), true, nil, nil, nil, []byte(`{}`)))
	userJobs, err := store.ListUserSchedulerJobs(ctx, "c1", "u1", 0)
	if err != nil || len(userJobs) != 1 {
		t.Fatalf("userJobs=%#v err=%v", userJobs, err)
	}
	enabled := true
	mock.ExpectQuery("UPDATE scheduler_jobs").
		WithArgs("job-1", "c1", "u1", nil, nil, []byte(`{"text":"go"}`), nil, enabled).
		WillReturnRows(schedulerRows().AddRow("job-1", "c1", "cron", "* * * * *", later, []byte(`{"user_id":"u1","input":{"text":"go"}}`), true, nil, nil, nil, []byte(`{}`)))
	updated, err := store.UpdateUserSchedulerJob(ctx, "job-1", "c1", "u1", SchedulerJobUpdate{Input: map[string]any{"text": "go"}, Enabled: &enabled})
	if err != nil || updated.ID != "job-1" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	mock.ExpectExec("DELETE FROM scheduler_jobs").WithArgs("job-1", "c1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeleteUserSchedulerJob(ctx, "job-1", "c1", "u1"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE scheduler_jobs").WithArgs("job-1", now).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteSchedulerJob(ctx, "job-1", now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE scheduler_jobs").WithArgs("job-1", now, later).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteSchedulerJob(ctx, "job-1", now, later); err != nil {
		t.Fatal(err)
	}

	if workflowWakeMetadata(nil) != nil || workflowWakeMetadata(map[string]any{"workflow_wake": true}) != true {
		t.Fatal("unexpected workflow wake metadata")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
