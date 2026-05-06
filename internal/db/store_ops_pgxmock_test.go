package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func reminderRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "agent_instance_id", "channel_type", "title", "schedule", "timezone", "payload", "enabled", "next_run_at", "last_fired_at", "repeat_interval_seconds", "repeat_until", "repeat_count", "fired_count", "metadata"})
}

func TestStoreReminderSubscriptionMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	owner := "worker"

	if _, err := store.CreateReminderSubscription(ctx, ReminderSubscriptionSpec{}); err == nil {
		t.Fatal("expected validation error")
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", 100, "u1").
		WillReturnRows(reminderRows().AddRow("sub-1", "c1", "u1", "s1", "a1", "", "title", "* * * * *", "UTC", []byte(`{}`), true, later, nil, 0, nil, 0, 0, []byte(`{}`)))
	subs, err := store.ListReminderSubscriptions(ctx, "c1", "u1", 0)
	if err != nil || len(subs) != 1 || subs[0].ID != "sub-1" {
		t.Fatalf("subs=%#v err=%v", subs, err)
	}

	mock.ExpectQuery("UPDATE reminder_subscriptions").
		WithArgs(1, owner, "60.000000 seconds").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "agent_instance_id", "channel_type", "title", "schedule", "timezone", "payload", "enabled", "next_run_at", "last_fired_at", "repeat_interval_seconds", "repeat_until", "repeat_count", "fired_count", "metadata", "lease_owner", "lease_expires_at"}).
			AddRow("sub-1", "c1", "u1", "s1", "a1", "", "title", "* * * * *", "UTC", []byte(`{}`), true, later, nil, 0, nil, 0, 0, []byte(`{}`), &owner, &later))
	claimed, err := store.ClaimDueReminderSubscriptions(ctx, owner, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].LeaseOwner == nil {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}

	mock.ExpectExec("UPDATE reminder_subscriptions").WithArgs("sub-1", now).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteReminderSubscription(ctx, "sub-1", now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE reminder_subscriptions").WithArgs("sub-1", now, later).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteReminderSubscription(ctx, "sub-1", now, later); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE reminder_subscriptions").WithArgs("sub-1", "c1", false).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := store.SetReminderSubscriptionEnabled(ctx, "sub-1", "c1", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
	title := "updated"
	enabled := false
	mock.ExpectQuery("UPDATE reminder_subscriptions").
		WithArgs("sub-1", "c1", "u1", title, nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, enabled, false).
		WillReturnRows(reminderRows().AddRow("sub-1", "c1", "u1", "s1", "a1", "", title, "* * * * *", "UTC", []byte(`{}`), false, later, nil, 0, nil, 0, 0, []byte(`{}`)))
	updated, err := store.UpdateUserReminderSubscription(ctx, "sub-1", "c1", "u1", ReminderSubscriptionUpdate{Title: &title, Enabled: &enabled})
	if err != nil || updated.Title != title || updated.Enabled {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	mock.ExpectExec("DELETE FROM reminder_subscriptions").WithArgs("sub-1", "c1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeleteUserReminderSubscription(ctx, "sub-1", "c1", "u1"); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateUserReminderSubscriptionClearsRepeatUntil(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	later := time.Now().UTC().Add(time.Hour)

	mock.ExpectQuery("SELECT schedule, next_run_at, repeat_interval_seconds, repeat_until").
		WithArgs("sub-1", "c1", "u1").
		WillReturnRows(pgxmock.NewRows([]string{"schedule", "next_run_at", "repeat_interval_seconds", "repeat_until"}).
			AddRow("@interval", later, 3600, nil))
	mock.ExpectQuery("UPDATE reminder_subscriptions").
		WithArgs("sub-1", "c1", "u1", nil, nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, true).
		WillReturnRows(reminderRows().AddRow("sub-1", "c1", "u1", "s1", "a1", "", "title", "@interval", "UTC", []byte(`{}`), true, later, nil, 3600, nil, 0, 0, []byte(`{}`)))
	updated, err := store.UpdateUserReminderSubscription(ctx, "sub-1", "c1", "u1", ReminderSubscriptionUpdate{RepeatUntilSet: true})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.RepeatUntil != nil {
		t.Fatalf("repeat_until should be cleared, got %v", updated.RepeatUntil)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateUserReminderSubscriptionRejectsZeroExistingInterval(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	later := time.Now().UTC().Add(time.Hour)
	zero := 0

	mock.ExpectQuery("SELECT schedule, next_run_at, repeat_interval_seconds, repeat_until").
		WithArgs("sub-1", "c1", "u1").
		WillReturnRows(pgxmock.NewRows([]string{"schedule", "next_run_at", "repeat_interval_seconds", "repeat_until"}).
			AddRow("@interval", later, 3600, nil))
	_, err := store.UpdateUserReminderSubscription(ctx, "sub-1", "c1", "u1", ReminderSubscriptionUpdate{RepeatIntervalSeconds: &zero})
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("expected positive interval error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateUserReminderSubscriptionRejectsRepeatUntilBeforeExistingNextRun(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	later := time.Now().UTC().Add(time.Hour)
	repeatUntil := later.Add(-time.Minute)

	mock.ExpectQuery("SELECT schedule, next_run_at, repeat_interval_seconds, repeat_until").
		WithArgs("sub-1", "c1", "u1").
		WillReturnRows(pgxmock.NewRows([]string{"schedule", "next_run_at", "repeat_interval_seconds", "repeat_until"}).
			AddRow("@interval", later, 3600, nil))
	_, err := store.UpdateUserReminderSubscription(ctx, "sub-1", "c1", "u1", ReminderSubscriptionUpdate{RepeatUntil: &repeatUntil})
	if err == nil || !strings.Contains(err.Error(), "repeat_until") {
		t.Fatalf("expected repeat_until error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreBroadcastMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	intentID := "intent-1"

	if _, _, err := store.CreateBroadcast(ctx, "", "", nil, nil); err == nil {
		t.Fatal("expected create broadcast validation error")
	}
	if _, err := store.ResolveBroadcastTargets(ctx, "", BroadcastTargetSelection{}); err == nil {
		t.Fatal("expected missing customer error")
	}
	if _, err := store.resolveBroadcastSegment(ctx, "c1", map[string]any{}, 10); err == nil {
		t.Fatal("expected empty segment error")
	}

	mock.ExpectQuery("SELECT DISTINCT").WithArgs("c1", 10).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "id"}).AddRow("u1", "s1").AddRow("u1", "s1"))
	mock.ExpectQuery("SELECT user_id").WithArgs("c1", "u2").
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "id"}).AddRow("u2", "s2"))
	targets, err := store.ResolveBroadcastTargets(ctx, "c1", BroadcastTargetSelection{AllUsers: true, UserIDs: []string{"u2"}, Limit: 10})
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets=%#v err=%v", targets, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", 100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "external_broadcast_id", "title", "payload", "generation_mode", "agent_instance_id", "generation_request", "status", "created_at", "updated_at"}).
			AddRow("b1", "c1", "", "title", []byte(`{}`), "direct", nil, []byte(`{}`), "queued", now, now))
	broadcasts, err := store.ListBroadcasts(ctx, "c1", 0)
	if err != nil || len(broadcasts) != 1 {
		t.Fatalf("broadcasts=%#v err=%v", broadcasts, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "b1", 250).
		WillReturnRows(pgxmock.NewRows([]string{"id", "broadcast_id", "customer_id", "user_id", "session_id", "status", "outbound_intent_id", "generation_run_id", "last_error", "created_at", "updated_at"}).
			AddRow("target-1", "b1", "c1", "u1", "s1", "queued", &intentID, nil, nil, now, now))
	listedTargets, err := store.ListBroadcastTargets(ctx, "c1", "b1", 0)
	if err != nil || len(listedTargets) != 1 || listedTargets[0].OutboundIntentID == nil {
		t.Fatalf("targets=%#v err=%v", listedTargets, err)
	}

	mock.ExpectExec("UPDATE broadcasts").WithArgs("b1", "c1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE broadcast_targets").WithArgs("b1", "c1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE outbound_intents").WithArgs("b1", "c1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CancelBroadcast(ctx, "c1", "b1"); err != nil {
		t.Fatal(err)
	}
	if string(mustJSON(nil)) != "null" {
		t.Fatalf("unexpected mustJSON nil")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDirectBroadcastSuppressesBlockedChannelWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT id, metadata").WithArgs("c1", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "metadata"}).
			AddRow("s1", []byte(`{"channel_type":"whatsapp","recommendation":{"blocked_channels":["whatsapp"]}}`)).
			AddRow("s2", []byte(`{"channel_type":"webchat","recommendation":{"blocked_channels":["whatsapp"]}}`)))
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO broadcasts").
		WithArgs("c1", "ext-1", "title", []byte(`{"body":"hi"}`), "direct", "", []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("b1"))
	mock.ExpectQuery("INSERT INTO broadcast_targets").
		WithArgs("b1", "c1", "u1", "s1", "channel_suppressed").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t1"))
	mock.ExpectQuery("INSERT INTO broadcast_targets").
		WithArgs("b1", "c1", "u2", "s2", "pending").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("t2"))
	mock.ExpectQuery("INSERT INTO outbound_intents").
		WithArgs("c1", "u2", "s2", nil, "broadcast", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("intent-1"))
	mock.ExpectQuery("INSERT INTO async_outbox").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(99)))
	mock.ExpectExec("UPDATE outbound_intents").WithArgs("intent-1", int64(99)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE broadcast_targets").WithArgs("t2", "intent-1").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()

	id, queued, suppressed, runs, err := store.CreateBroadcastFromSpec(ctx, BroadcastSpec{
		CustomerID: "c1", ExternalBroadcastID: "ext-1", Title: "title", Payload: map[string]any{"body": "hi"},
		Targets: []BroadcastTargetSpec{{UserID: "u1", SessionID: "s1"}, {UserID: "u2", SessionID: "s2"}},
	})
	if err != nil || id != "b1" || queued != 1 || suppressed != 1 || runs != 0 {
		t.Fatalf("id=%q queued=%d suppressed=%d runs=%d err=%v", id, queued, suppressed, runs, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBroadcastReferenceArtifact(t *testing.T) {
	payload := broadcastOutboundPayload("b1", "ext-1", "t1", "title", []byte(`{"body":"hi"}`), "hello", "run-1")
	artifacts, ok := payload["artifacts"].([]map[string]any)
	if !ok || len(artifacts) != 1 || artifacts[0]["type"] != "broadcast_reference" || artifacts[0]["id"] != "b1" {
		t.Fatalf("payload=%#v", payload)
	}
	data, ok := artifacts[0]["data"].(map[string]any)
	if !ok || data["broadcast_id"] != "b1" || data["external_broadcast_id"] != "ext-1" {
		t.Fatalf("artifact=%#v", artifacts[0])
	}
}
