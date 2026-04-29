package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func reminderRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "agent_instance_id", "title", "schedule", "timezone", "payload", "enabled", "next_run_at", "last_fired_at", "metadata"})
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
		WillReturnRows(reminderRows().AddRow("sub-1", "c1", "u1", "s1", "a1", "title", "* * * * *", "UTC", []byte(`{}`), true, later, nil, []byte(`{}`)))
	subs, err := store.ListReminderSubscriptions(ctx, "c1", "u1", 0)
	if err != nil || len(subs) != 1 || subs[0].ID != "sub-1" {
		t.Fatalf("subs=%#v err=%v", subs, err)
	}

	mock.ExpectQuery("UPDATE reminder_subscriptions").
		WithArgs(1, owner, "60.000000 seconds").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "agent_instance_id", "title", "schedule", "timezone", "payload", "enabled", "next_run_at", "last_fired_at", "metadata", "lease_owner", "lease_expires_at"}).
			AddRow("sub-1", "c1", "u1", "s1", "a1", "title", "* * * * *", "UTC", []byte(`{}`), true, later, nil, []byte(`{}`), &owner, &later))
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "title", "payload", "status", "created_at", "updated_at"}).
			AddRow("b1", "c1", "title", []byte(`{}`), "queued", now, now))
	broadcasts, err := store.ListBroadcasts(ctx, "c1", 0)
	if err != nil || len(broadcasts) != 1 {
		t.Fatalf("broadcasts=%#v err=%v", broadcasts, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "b1", 250).
		WillReturnRows(pgxmock.NewRows([]string{"id", "broadcast_id", "customer_id", "user_id", "session_id", "status", "outbound_intent_id", "created_at", "updated_at"}).
			AddRow("target-1", "b1", "c1", "u1", "s1", "queued", &intentID, now, now))
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
