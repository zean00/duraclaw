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
