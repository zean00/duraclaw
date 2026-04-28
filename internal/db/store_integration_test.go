package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func integrationStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return NewStore(pool), pool.Close
}

func TestStoreDurableRunHappyPathPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	acpCtx := ACPContext{
		CustomerID: "c-" + suffix, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "i",
	}
	run, err := store.CreateRun(ctx, acpCtx, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimRun(ctx, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claimed=%#v run=%#v", claimed, run)
	}
	stepID, err := store.StartRunStep(ctx, run.ID, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunStep(ctx, run.ID, stepID, "succeeded", map[string]any{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	msgID, err := store.InsertMessage(ctx, run.CustomerID, run.SessionID, run.ID, "assistant", map[string]any{"text": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunWithMessage(ctx, run.ID, msgID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "completed" {
		t.Fatalf("state=%s", got.State)
	}
}

func TestStoreSessionReassignmentAndVersionSnapshotPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-reassign-" + suffix
	base := ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "agent-a", SessionID: "s", RequestID: "r1", IdempotencyKey: "i1"}
	first, err := store.CreateRun(ctx, base, map[string]any{"text": "first"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentInstanceID != "agent-a" || first.AgentInstanceVersionID == "" {
		t.Fatalf("first run=%#v", first)
	}
	v2, err := store.CreateAgentInstanceVersion(ctx, AgentInstanceVersionSpec{
		CustomerID: customerID, AgentInstanceID: "agent-b", Name: "b", SystemInstructions: "version b", ActivateImmediately: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutTransfer := base
	withoutTransfer.AgentInstanceID = "agent-b"
	withoutTransfer.RequestID = "r2"
	withoutTransfer.IdempotencyKey = "i2"
	second, err := store.CreateRun(ctx, withoutTransfer, map[string]any{"text": "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentInstanceID != "agent-a" {
		t.Fatalf("run used request agent without transfer: %#v", second)
	}
	if _, err := store.ReassignSession(ctx, customerID, "s", "agent-b", "upgrade", nil); err != nil {
		t.Fatal(err)
	}
	afterTransfer := withoutTransfer
	afterTransfer.RequestID = "r3"
	afterTransfer.IdempotencyKey = "i3"
	third, err := store.CreateRun(ctx, afterTransfer, map[string]any{"text": "third"})
	if err != nil {
		t.Fatal(err)
	}
	if third.AgentInstanceID != "agent-b" || third.AgentInstanceVersionID != v2.ID {
		t.Fatalf("run did not snapshot reassigned agent version: run=%#v version=%#v", third, v2)
	}
}

func TestStoreReminderSubscriptionLeaseCompletionPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-reminder-" + suffix
	nextRunAt := time.Now().UTC().Add(-time.Minute)
	sub, err := store.CreateReminderSubscription(ctx, ReminderSubscriptionSpec{
		CustomerID: customerID, UserID: "u", SessionID: "s", AgentInstanceID: "a", Title: "once", Schedule: "@once", NextRunAt: nextRunAt,
		Payload: map[string]any{"text": "wake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueReminderSubscriptions(ctx, "test", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != sub.ID || claimed[0].LeaseOwner == nil || *claimed[0].LeaseOwner != "test" {
		t.Fatalf("claimed=%#v sub=%#v", claimed, sub)
	}
	if err := store.CompleteReminderSubscription(ctx, sub.ID, nextRunAt, time.Time{}); err != nil {
		t.Fatal(err)
	}
	subs, err := store.ListReminderSubscriptions(ctx, customerID, "u", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Enabled {
		t.Fatalf("subscription should be disabled after one-shot completion: %#v", subs)
	}
}
