package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func runRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "customer_id", "user_id", "agent_instance_id", "agent_instance_version_id", "session_id", "request_id", "idempotency_key", "state", "input", "error", "created_at", "updated_at", "completed_at"})
}

func TestStoreCoreRunReadAndEventMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT id").WithArgs("run-1").
		WillReturnRows(runRows().AddRow("run-1", "c1", "u1", "a1", "v1", "s1", "req", "idem", "queued", []byte(`{"text":"hi"}`), nil, now, now, nil))
	run, err := store.GetRun(ctx, "run-1")
	if err != nil || run.ID != "run-1" {
		t.Fatalf("run=%#v err=%v", run, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "s1").
		WillReturnRows(runRows().AddRow("run-1", "c1", "u1", "a1", "v1", "s1", "req", "idem", "queued", []byte(`{}`), nil, now, now, nil))
	latest, err := store.LatestRun(ctx, "c1", "s1")
	if err != nil || latest.ID != "run-1" {
		t.Fatalf("latest=%#v err=%v", latest, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "s1", "idem").
		WillReturnRows(runRows().AddRow("run-1", "c1", "u1", "a1", "v1", "s1", "req", "idem", "queued", []byte(`{}`), nil, now, now, nil))
	byKey, err := store.RunByIdempotencyKey(ctx, "c1", "s1", "idem")
	if err != nil || byKey.IdempotencyKey != "idem" {
		t.Fatalf("byKey=%#v err=%v", byKey, err)
	}

	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "run.queued", []byte(`{"state":"queued"}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.AddEvent(ctx, "run-1", "run.queued", map[string]string{"state": "queued"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("run-1", int64(10), 500).
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "event_type", "payload", "created_at"}).
			AddRow(int64(11), "run-1", "run.queued", []byte(`{}`), now))
	events, err := store.Events(ctx, "run-1", 10)
	if err != nil || len(events) != 1 || events[0].ID != 11 {
		t.Fatalf("events=%#v err=%v", events, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCoreRunMutationMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("WITH candidate").WithArgs("worker", "60.000000 seconds").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "agent_instance_id", "agent_instance_version_id", "session_id", "request_id", "idempotency_key", "state", "input", "error", "created_at", "updated_at", "completed_at", "refinement_parent_run_id", "refinement_depth", "suppress_direct_outbound", "interrupt_window_started_at"}).
			AddRow("run-1", "c1", "u1", "a1", "v1", "s1", "req", "idem", "leased", []byte(`{}`), nil, now, now, nil, "", 0, false, nil))
	mock.ExpectCommit()
	mock.ExpectRollback()
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "run.leased", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	claimed, err := store.ClaimRun(ctx, "worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != "run-1" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}

	mock.ExpectExec("UPDATE runs").WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	recovered, err := store.RecoverExpiredLeases(ctx)
	if err != nil || recovered != 2 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	mock.ExpectExec("UPDATE runs").WithArgs("run-1", "worker", "30.000000 seconds").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	extended, err := store.ExtendRunLease(ctx, "run-1", "worker", 30*time.Second)
	if err != nil || !extended {
		t.Fatalf("extended=%v err=%v", extended, err)
	}
	mock.ExpectQuery("SELECT state").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"state"}).AddRow("running"))
	state, err := store.RunState(ctx, "run-1")
	if err != nil || state != "running" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if err := store.SetRunState(ctx, "run-1", "bogus", nil); err == nil {
		t.Fatal("expected invalid state")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDeferRunIfActiveReturnsDeferredAck(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	c := ACPContext{
		CustomerID: "c1", UserID: "u1", AgentInstanceID: "a1", SessionID: "s1",
		RequestID: "req-2", IdempotencyKey: "idem-2",
	}

	mock.ExpectQuery("SELECT id::text, active_run_id::text").WithArgs("c1", "s1", "idem-2").WillReturnError(pgx.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id::text").WithArgs("c1", "s1", "2.000000 seconds", 2).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("run-1"))
	mock.ExpectQuery("INSERT INTO deferred_run_messages").WithArgs("c1", "u1", "a1", "s1", "run-1", "req-2", "idem-2", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "active_run_id", "message_id"}).AddRow("defer-2", "run-1", ""))
	mock.ExpectQuery("INSERT INTO messages").WithArgs("c1", "s1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("msg-2"))
	mock.ExpectQuery("UPDATE deferred_run_messages").WithArgs("defer-2", "msg-2").
		WillReturnRows(pgxmock.NewRows([]string{"message_id"}).AddRow("msg-2"))
	mock.ExpectCommit()
	mock.ExpectRollback()

	ack, err := store.DeferRunIfActive(ctx, c, map[string]any{"text": "follow up"}, 2*time.Second, 2)
	if err != nil || ack == nil || ack.State != "deferred" || ack.ActiveRunID != "run-1" || ack.MessageID != "msg-2" || ack.DeferredMessageID != "defer-2" {
		t.Fatalf("ack=%#v err=%v", ack, err)
	}
}

func TestRefinementInputExposesContextInText(t *testing.T) {
	input := refinementInput([]byte(`{"text":"first"}`), []DeferredRunMessage{{
		ID: "defer-1", MessageID: "msg-1", Input: []byte(`{"parts":[{"type":"text","text":"second"}]}`), CreatedAt: time.Date(2026, 4, 30, 1, 2, 3, 0, time.UTC),
	}}, "draft answer")
	text, _ := input["text"].(string)
	for _, want := range []string{"draft answer", "first", "second", "defer-1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q: %s", want, text)
		}
	}
}

func TestStoreMessagesCheckpointsAndStepsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("INSERT INTO messages").WithArgs("c1", "s1", "run-1", "user", []byte(`{"text":"hi"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("msg-1"))
	msgID, err := store.InsertMessage(ctx, "c1", "s1", "run-1", "user", map[string]string{"text": "hi"})
	if err != nil || msgID != "msg-1" {
		t.Fatalf("msgID=%q err=%v", msgID, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("c1", "s1", 12).
		WillReturnRows(pgxmock.NewRows([]string{"id", "role", "content", "created_at"}).AddRow("msg-1", "user", []byte(`{"text":"hi"}`), now))
	messages, err := store.RecentMessages(ctx, "c1", "s1", 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%#v err=%v", messages, err)
	}

	mock.ExpectQuery("SELECT channel_context").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"channel_context"}).AddRow([]byte(`{"trace_id":"trace","traceparent":"parent"}`)))
	mock.ExpectExec("INSERT INTO checkpoints").WithArgs("run-1", "step", []byte(`{"span_name":"step","trace_id":"trace","traceparent":"parent","value":"state"}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.Checkpoint(ctx, "run-1", "step", "state"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT channel_context").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"channel_context"}).AddRow([]byte(`{"trace_id":"trace","traceparent":"parent"}`)))
	traceID, traceParent, err := store.RunTraceContext(ctx, "run-1")
	if err != nil || traceID != "trace" || traceParent != "parent" {
		t.Fatalf("traceID=%q traceParent=%q err=%v", traceID, traceParent, err)
	}
	mock.ExpectQuery("SELECT channel_context").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"channel_context"}).AddRow([]byte(`{"channel_type":"web","channel_user_id":"cu","channel_conversation_id":"conv","trace_id":"trace","traceparent":"parent"}`)))
	channel, err := store.RunChannelContext(ctx, "run-1")
	if err != nil || channel.ChannelType != "web" || channel.ChannelUserID != "cu" || channel.ChannelConversationID != "conv" {
		t.Fatalf("channel=%#v err=%v", channel, err)
	}

	mock.ExpectQuery("INSERT INTO run_steps").WithArgs("run-1", "model", []byte(`{"prompt":"hi"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("step-1"))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "step.started", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	stepID, err := store.StartRunStep(ctx, "run-1", "model", map[string]string{"prompt": "hi"})
	if err != nil || stepID != "step-1" {
		t.Fatalf("stepID=%q err=%v", stepID, err)
	}
	mock.ExpectExec("UPDATE run_steps").WithArgs("step-1", "succeeded", []byte(`{"ok":true}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "step.succeeded", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.CompleteRunStep(ctx, "run-1", "step-1", "succeeded", map[string]bool{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunStep(ctx, "run-1", "step-1", "bogus", nil, nil); err == nil {
		t.Fatal("expected invalid step state")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCompleteRunAndAttachArtifactWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()

	mock.ExpectExec("UPDATE runs").WithArgs("run-1", "msg-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "run.completed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO observability_events").WithArgs("c1", "run-1", "run_state_changed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.CompleteRunWithMessage(ctx, "run-1", "msg-1"); err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT customer_id").WithArgs("run-1").WillReturnRows(pgxmock.NewRows([]string{"customer_id"}).AddRow("c1"))
	mock.ExpectExec("INSERT INTO artifacts").WithArgs("art-1", "c1", "run-1", "image", "image/png", "a.png", int64(10), "sum", "s3://a", "web", "msg-1", "available", []byte(`{"k":"v"}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	if err := store.AttachArtifact(ctx, "run-1", Artifact{ID: "art-1", Modality: "image", MediaType: "image/png", Filename: "a.png", SizeBytes: 10, Checksum: "sum", StorageRef: "s3://a", SourceChannel: "web", SourceMessageID: "msg-1", State: "available", Metadata: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AttachArtifact(ctx, "run-1", Artifact{ID: "art-1", State: "bogus"}); err == nil {
		t.Fatal("expected invalid artifact state")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
