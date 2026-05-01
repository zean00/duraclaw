package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreArtifactAndCallHelpersWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT id").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "modality", "media_type", "filename", "size_bytes", "checksum", "storage_ref", "source_channel", "source_message_id", "state", "metadata"}).
			AddRow("art-1", "image", "image/png", "a.png", int64(10), "sum", "s3://a", "web", "msg", "available", []byte(`{"k":"v"}`)))
	artifacts, err := store.ArtifactsForRun(ctx, "run-1")
	if err != nil || len(artifacts) != 1 || artifacts[0].Metadata["k"] != "v" {
		t.Fatalf("artifacts=%#v err=%v", artifacts, err)
	}
	mock.ExpectQuery("SELECT r.id").WithArgs("art-1", "c1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "artifact_id", "representation_type", "summary", "metadata", "created_at"}).
			AddRow("rep-1", "art-1", "summary", "text", []byte(`{}`), now))
	reps, err := store.ArtifactRepresentations(ctx, "c1", "art-1")
	if err != nil || len(reps) != 1 {
		t.Fatalf("reps=%#v err=%v", reps, err)
	}
	mock.ExpectExec("INSERT INTO artifact_representations").WithArgs("art-1", "summary", "text", []byte(`{}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.InsertArtifactRepresentation(ctx, "art-1", "summary", "text", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE artifacts").WithArgs("art-1", "processed").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetArtifactState(ctx, "art-1", "processed"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetArtifactState(ctx, "art-1", "bogus"); err == nil {
		t.Fatal("expected invalid artifact state")
	}

	mock.ExpectQuery("INSERT INTO tool_calls").WithArgs("run-1", "lookup", []byte(`{"q":"x"}`), false, StableArgsHash("lookup", map[string]string{"q": "x"})).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("tool-1"))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "tool.started", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "tool.started", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	toolID, err := store.StartToolCall(ctx, "run-1", "lookup", map[string]string{"q": "x"}, false)
	if err != nil || toolID != "tool-1" {
		t.Fatalf("toolID=%q err=%v", toolID, err)
	}
	mock.ExpectExec("UPDATE tool_calls").WithArgs("tool-1", "succeeded", []byte(`{"ok":true}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "tool.succeeded", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "tool.succeeded", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.CompleteToolCall(ctx, "tool-1", "run-1", map[string]bool{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "tool_name", "state", "arguments", "result", "retryable", "args_hash", "error"}).
			AddRow("tool-1", "run-1", "lookup", "succeeded", []byte(`{}`), []byte(`{"ok":true}`), false, "hash", nil))
	calls, err := store.CompletedNonRetryableToolCalls(ctx, "run-1")
	if err != nil || len(calls) != 1 {
		t.Fatalf("calls=%#v err=%v", calls, err)
	}
	mock.ExpectQuery("SELECT count").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
	count, err := store.ToolCallCount(ctx, "run-1")
	if err != nil || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	mock.ExpectQuery("SELECT").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(4))
	executions, err := store.ToolExecutionCount(ctx, "run-1")
	if err != nil || executions != 4 {
		t.Fatalf("executions=%d err=%v", executions, err)
	}

	if StableArgsHash("t", map[string]any{"b": 2, "a": []any{jsonRaw(`{"z":1}`)}}) == "" {
		t.Fatal("expected stable hash")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreOutboxAndObservabilityWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := "worker"
	runID := "run-1"

	mock.ExpectQuery("INSERT INTO async_outbox").WithArgs("topic", []byte(`{"ok":true}`), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(9)))
	id, err := store.EnqueueOutbox(ctx, "topic", map[string]bool{"ok": true}, time.Time{})
	if err != nil || id != 9 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	mock.ExpectQuery("UPDATE async_outbox").WithArgs(50, owner).
		WillReturnRows(pgxmock.NewRows([]string{"id", "topic", "payload", "available_at", "claimed_at", "claim_owner"}).
			AddRow(int64(9), "topic", []byte(`{}`), now, &now, &owner))
	items, err := store.ClaimOutbox(ctx, owner, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	mock.ExpectExec("UPDATE async_outbox").WithArgs(int64(9), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.ReleaseOutbox(ctx, 9, time.Minute); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE async_outbox").WithArgs(int64(9)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteOutbox(ctx, 9); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "event", []byte(`{"ok":true}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.AddObservabilityEvent(ctx, "", "run-1", "event", map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", 100, "run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "run_id", "event_type", "payload", "created_at"}).
			AddRow(int64(1), "c1", &runID, "event", []byte(`{}`), now))
	events, err := store.ListObservabilityEvents(ctx, "c1", "run-1", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%#v err=%v", events, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func jsonRaw(raw string) json.RawMessage {
	return json.RawMessage(raw)
}
