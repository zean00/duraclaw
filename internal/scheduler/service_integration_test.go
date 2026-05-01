package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"duraclaw/internal/db"
)

func integrationStore(t *testing.T) (*db.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return db.NewStore(pool), pool.Close
}

func TestServiceReminderFanoutPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-scheduler-" + suffix
	fireAt := time.Unix(1, 0).UTC()
	_, err := store.CreateReminderSubscription(ctx, db.ReminderSubscriptionSpec{
		CustomerID: customerID, UserID: "u", SessionID: "s", AgentInstanceID: "a", Title: "wake", Schedule: "@once", NextRunAt: fireAt,
		Payload: map[string]any{"text": "scheduled wake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, "test-scheduler")
	var run *db.Run
	for i := 0; i < 5; i++ {
		if _, err := service.RunOnce(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		run, err = store.LatestRun(ctx, customerID, "s")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run == nil {
		t.Fatal(err)
	}
	if run.AgentInstanceID != "a" || run.State != "queued" {
		t.Fatalf("run=%#v", run)
	}
	subs, err := store.ListReminderSubscriptions(ctx, customerID, "u", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Enabled {
		t.Fatalf("subscription should be disabled after fanout: %#v", subs)
	}
}
