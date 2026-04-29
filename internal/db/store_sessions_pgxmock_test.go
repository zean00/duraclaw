package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreSessionSummaryAndMonitorMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectExec("INSERT INTO session_summaries").WithArgs("c1", "s1", nil, "summary", []byte(`{}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.UpsertSessionSummary(ctx, "c1", "s1", "", "summary", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSessionSummary(ctx, "", "s1", "", "summary", nil); err == nil {
		t.Fatal("expected validation error")
	}
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "s1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "session_id", "source_run_id", "summary", "metadata", "updated_at"}).
			AddRow("c1", "s1", nil, "summary", []byte(`{}`), now))
	summary, err := store.SessionSummary(ctx, "c1", "s1")
	if err != nil || summary.Summary != "summary" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	mock.ExpectExec("UPDATE runs").WithArgs("run-1", []byte(`{"pct":50}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetRunProgress(ctx, "run-1", map[string]int{"pct": 50}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT progress").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"progress"}).AddRow([]byte{}))
	progress, err := store.RunProgress(ctx, "run-1")
	if err != nil || string(progress) != "{}" {
		t.Fatalf("progress=%s err=%v", progress, err)
	}
	mock.ExpectExec("UPDATE runs").WithArgs("run-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.MarkRunBackground(ctx, "run-1"); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("UPDATE sessions").WithArgs("1800.000000 seconds", 50, "duraclaw-session-monitor", "300.000000 seconds").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "user_id", "agent_instance_id", "id", "metadata", "active_pattern", "updated_at", "last_monitored_at", "last_message_at"}).
			AddRow("c1", "u1", "a1", "s1", []byte(`{}`), []byte(`{}`), now, nil, &now))
	claimed, err := store.ClaimIdleSessions(ctx, "", 0, 0, 0)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	mock.ExpectExec("UPDATE sessions").WithArgs("c1", "s1", pgxmock.AnyArg(), []byte(`{}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteSessionMonitor(ctx, "c1", "s1", time.Time{}, nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE sessions").WithArgs("c1", "s1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.ReleaseSessionMonitor(ctx, "c1", "s1"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", "s1", 40).
		WillReturnRows(pgxmock.NewRows([]string{"id", "role", "content", "created_at"}).AddRow("msg-1", "user", []byte(`{"text":"hi"}`), now))
	messages, err := store.RecentUserMessages(ctx, "c1", "s1", 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%#v err=%v", messages, err)
	}
	if pgInterval(-time.Second) != "1.000000 seconds" {
		t.Fatal("unexpected interval")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSessionTransferAndBackgroundRunsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.ReassignSession(ctx, "", "s1", "a2", "", nil); err == nil {
		t.Fatal("expected validation error")
	}
	mock.ExpectQuery("SELECT agent_instance_id").WithArgs("c1", "s1").WillReturnRows(pgxmock.NewRows([]string{"agent_instance_id"}).AddRow("a1"))
	agentID, err := store.sessionAgentInstanceID(ctx, "c1", "s1")
	if err != nil || agentID != "a1" {
		t.Fatalf("agentID=%q err=%v", agentID, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", "s1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "session_id", "from_agent_instance_id", "to_agent_instance_id", "reason", "metadata", "created_at"}).
			AddRow("transfer-1", "c1", "s1", "a1", "a2", "handoff", []byte(`{}`), now))
	transfer, err := store.LatestSessionTransfer(ctx, "c1", "s1")
	if err != nil || transfer.ID != "transfer-1" {
		t.Fatalf("transfer=%#v err=%v", transfer, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", "missing").WillReturnError(pgx.ErrNoRows)
	none, err := store.LatestSessionTransfer(ctx, "c1", "missing")
	if err != nil || none != nil {
		t.Fatalf("none=%#v err=%v", none, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "", 100).
		WillReturnRows(runRows().AddRow("run-1", "c1", "u1", "a1", "v1", "s1", "req", "idem", "queued", []byte(`{}`), nil, now, now, nil))
	runs, err := store.BackgroundRuns(ctx, "c1", "", 0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	if backgroundQuotaLockKey("c1", "a1") == 0 {
		t.Fatal("unexpected lock key")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
