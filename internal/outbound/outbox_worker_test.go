package outbound

import (
	"context"
	"errors"
	"testing"
	"time"

	"duraclaw/internal/db"
)

type fakeOutboxStore struct {
	items     []db.OutboxItem
	completed []int64
	released  []int64
}

func (s *fakeOutboxStore) ClaimOutbox(context.Context, string, int) ([]db.OutboxItem, error) {
	return s.items, nil
}

func (s *fakeOutboxStore) CompleteOutbox(_ context.Context, id int64) error {
	s.completed = append(s.completed, id)
	return nil
}

func (s *fakeOutboxStore) ReleaseOutbox(_ context.Context, id int64, _ time.Duration) error {
	s.released = append(s.released, id)
	return nil
}

type fakeSink struct {
	err error
	ids []int64
}

func (s *fakeSink) Handle(_ context.Context, item db.OutboxItem) error {
	s.ids = append(s.ids, item.ID)
	return s.err
}

func TestOutboxWorkerCompletesAfterSink(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1}, {ID: 2, AvailableAt: time.Now()}}}
	sink := &fakeSink{}
	count, err := NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(store.completed) != 2 || len(sink.ids) != 2 {
		t.Fatalf("count=%d store=%#v sink=%#v", count, store, sink)
	}
}

func TestOutboxWorkerStopsOnSinkError(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1}}}
	sink := &fakeSink{err: errors.New("boom")}
	count, err := NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err == nil || count != 0 || len(store.completed) != 0 || len(store.released) != 1 {
		t.Fatalf("count=%d err=%v store=%#v", count, err, store)
	}
}
