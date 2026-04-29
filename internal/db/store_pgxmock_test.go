package db

import (
	"context"
	"testing"

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
