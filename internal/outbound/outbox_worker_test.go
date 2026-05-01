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
	extended  []int64
}

func (s *fakeOutboxStore) ClaimOutbox(context.Context, string, int) ([]db.OutboxItem, error) {
	return s.items, nil
}

func (s *fakeOutboxStore) ExtendOutboxClaim(_ context.Context, id int64, _ string, _ time.Duration) (bool, error) {
	s.extended = append(s.extended, id)
	return true, nil
}

func (s *fakeOutboxStore) CompleteOutbox(_ context.Context, id int64, _ string) error {
	s.completed = append(s.completed, id)
	return nil
}

func (s *fakeOutboxStore) ReleaseOutbox(_ context.Context, id int64, _ string, _ time.Duration) error {
	s.released = append(s.released, id)
	return nil
}

type fakeSink struct {
	err   error
	ids   []int64
	delay time.Duration
}

func (s *fakeSink) Handle(ctx context.Context, item db.OutboxItem) error {
	s.ids = append(s.ids, item.ID)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}

type fakeBatchSink struct {
	err     error
	batches [][]int64
	topics  []string
	support map[string]bool
	errID   int64
}

func (s *fakeBatchSink) Handle(_ context.Context, item db.OutboxItem) error {
	s.batches = append(s.batches, []int64{item.ID})
	s.topics = append(s.topics, item.Topic)
	if s.errID != 0 && item.ID == s.errID {
		return s.err
	}
	if s.errID != 0 {
		return nil
	}
	return s.err
}

func (s *fakeBatchSink) HandleBatch(_ context.Context, topic string, items []db.OutboxItem) error {
	var ids []int64
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	s.batches = append(s.batches, ids)
	s.topics = append(s.topics, topic)
	return s.err
}

func (s *fakeBatchSink) SupportsBatch(topic string) bool {
	if s.support == nil {
		return true
	}
	return s.support[topic]
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

func TestOutboxWorkerExtendsClaimDuringSlowSink(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1}}}
	sink := &fakeSink{delay: 35 * time.Millisecond}
	worker := NewOutboxWorker(store, sink, "owner")
	worker.leaseFor = 15 * time.Millisecond
	count, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(store.extended) == 0 {
		t.Fatalf("count=%d extended=%#v", count, store.extended)
	}
}

func TestOutboxWorkerUsesBatchSinkByTopic(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1, Topic: "a"}, {ID: 2, Topic: "a"}, {ID: 3, Topic: "b"}}}
	sink := &fakeBatchSink{}
	count, err := NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || len(store.completed) != 3 || len(sink.batches) != 2 {
		t.Fatalf("count=%d completed=%#v batches=%#v", count, store.completed, sink.batches)
	}
	if sink.topics[0] != "a" || len(sink.batches[0]) != 2 || sink.topics[1] != "b" {
		t.Fatalf("topics=%#v batches=%#v", sink.topics, sink.batches)
	}
}

func TestOutboxWorkerReleasesBatchOnSinkError(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1, Topic: "a"}, {ID: 2, Topic: "a"}}}
	sink := &fakeBatchSink{err: errors.New("boom")}
	count, err := NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err == nil || count != 0 || len(store.completed) != 0 || len(store.released) != 2 {
		t.Fatalf("count=%d err=%v store=%#v", count, err, store)
	}
}

func TestOutboxWorkerCompletesFallbackSinglesBeforeLaterFailure(t *testing.T) {
	store := &fakeOutboxStore{items: []db.OutboxItem{{ID: 1, Topic: "a"}, {ID: 2, Topic: "a"}}}
	sink := &fakeBatchSink{err: errors.New("boom"), support: map[string]bool{"a": false}, errID: 2}
	count, err := NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err == nil || count != 1 || len(store.completed) != 1 || store.completed[0] != 1 || len(store.released) != 1 || store.released[0] != 2 {
		t.Fatalf("count=%d err=%v completed=%#v released=%#v", count, err, store.completed, store.released)
	}

	store = &fakeOutboxStore{items: []db.OutboxItem{{ID: 1, Topic: "a"}, {ID: 2, Topic: "a"}}}
	sink = &fakeBatchSink{support: map[string]bool{"a": false}}
	count, err = NewOutboxWorker(store, sink, "owner").RunOnce(context.Background())
	if err != nil || count != 2 || len(store.completed) != 2 || len(store.released) != 0 || len(sink.batches) != 2 {
		t.Fatalf("count=%d err=%v completed=%#v released=%#v batches=%#v", count, err, store.completed, store.released, sink.batches)
	}
}

func TestOutboxWorkerRequiresStoreAndSink(t *testing.T) {
	if _, err := NewOutboxWorker(nil, &fakeSink{}, "").RunOnce(context.Background()); err == nil {
		t.Fatalf("expected nil store error")
	}
	if _, err := NewOutboxWorker(&fakeOutboxStore{}, nil, "").RunOnce(context.Background()); err == nil {
		t.Fatalf("expected nil sink error")
	}
}

func TestOutboxWorkerGroupsEmptyTopicAsDefault(t *testing.T) {
	groups := groupByTopic([]db.OutboxItem{{ID: 1}, {ID: 2, Topic: "a"}, {ID: 3}})
	if len(groups) != 2 || groups[0].topic != "default" || len(groups[0].items) != 2 || groups[1].topic != "a" {
		t.Fatalf("groups=%#v", groups)
	}
	sink := &fakeBatchSink{support: map[string]bool{"default": true}}
	if !batchSinkSupportsAnyTopic(sink, []db.OutboxItem{{ID: 1}}) {
		t.Fatalf("expected default topic batch support")
	}
	if batchSinkSupportsAnyTopic(sink, []db.OutboxItem{{ID: 1, Topic: "other"}}) {
		t.Fatalf("did not expect other topic batch support")
	}
}
