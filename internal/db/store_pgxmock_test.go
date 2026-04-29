package db

import (
	"context"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreAcceptsPgxMockPool(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectQuery("SELECT").
		WillReturnRows(pgxmock.NewRows([]string{
			"runs_queued",
			"runs_active",
			"outbox_pending",
			"async_write_queued",
			"scheduler_due",
		}).AddRow(1, 2, 3, 4, 5))

	stats, err := NewStore(mock).QueueStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats != (QueueStats{RunsQueued: 1, RunsActive: 2, OutboxPending: 3, AsyncWriteQueued: 4, SchedulerDue: 5}) {
		t.Fatalf("stats=%#v", stats)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreAsyncWriteMethodsWithPgxMock(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	store := NewStore(mock)
	ctx := context.Background()

	mock.ExpectQuery("INSERT INTO async_write_jobs").
		WithArgs("c1", nil, "profile_refresh", "profiles", []byte(`{"user":"u1"}`), "queued", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(int64(42)))
	id, err := store.EnqueueAsyncWrite(ctx, AsyncWriteSpec{
		CustomerID:  "c1",
		JobType:     "profile_refresh",
		TargetTable: "profiles",
		Payload:     map[string]string{"user": "u1"},
	})
	if err != nil || id != 42 {
		t.Fatalf("id=%d err=%v", id, err)
	}

	now := time.Now().UTC()
	runID := "run-1"
	leaseOwner := "worker-1"
	mock.ExpectQuery("UPDATE async_write_jobs").
		WithArgs(1, "worker-1", "30.000000 seconds").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "customer_id", "run_id", "job_type", "target_table", "payload", "state", "available_at",
			"lease_owner", "lease_expires_at", "error", "attempts", "created_at", "completed_at",
		}).AddRow(int64(7), "c1", &runID, "job", "table", []byte(`{"ok":true}`), "leased", now, &leaseOwner, &now, nil, 1, now, nil))
	jobs, err := store.ClaimAsyncWriteJobs(ctx, "worker-1", 1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != 7 || jobs[0].RunID == nil || *jobs[0].RunID != runID {
		t.Fatalf("jobs=%#v", jobs)
	}

	mock.ExpectExec("UPDATE async_write_jobs").WithArgs(int64(7), "completed", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteAsyncWriteJob(ctx, 7, "", nil); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("UPDATE async_write_jobs").WithArgs(int64(7), pgxmock.AnyArg(), "retry later").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.ReleaseAsyncWriteJob(ctx, 7, time.Minute, "retry later"); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
