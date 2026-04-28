package outbound

import (
	"context"
	"fmt"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/observability"
)

type OutboxStore interface {
	ClaimOutbox(ctx context.Context, owner string, limit int) ([]db.OutboxItem, error)
	ReleaseOutbox(ctx context.Context, id int64, delay time.Duration) error
	CompleteOutbox(ctx context.Context, id int64) error
}

type Sink interface {
	Handle(ctx context.Context, item db.OutboxItem) error
}

type OutboxWorker struct {
	store      OutboxStore
	sink       Sink
	owner      string
	limit      int
	retryDelay time.Duration
	counters   *observability.Counters
}

func NewOutboxWorker(store OutboxStore, sink Sink, owner string) *OutboxWorker {
	if owner == "" {
		owner = "duraclaw-outbox"
	}
	return &OutboxWorker{store: store, sink: sink, owner: owner, limit: 50, retryDelay: 30 * time.Second}
}

func (w *OutboxWorker) WithCounters(counters *observability.Counters) *OutboxWorker {
	w.counters = counters
	return w
}

func (w *OutboxWorker) RunOnce(ctx context.Context) (int, error) {
	if w.store == nil || w.sink == nil {
		return 0, fmt.Errorf("outbox worker requires store and sink")
	}
	items, err := w.store.ClaimOutbox(ctx, w.owner, w.limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, item := range items {
		if err := w.sink.Handle(ctx, item); err != nil {
			_ = w.store.ReleaseOutbox(context.Background(), item.ID, w.retryDelay)
			w.inc("outbox_failed")
			return completed, err
		}
		if err := w.store.CompleteOutbox(ctx, item.ID); err != nil {
			return completed, err
		}
		completed++
		w.inc("outbox_completed")
	}
	return completed, nil
}

func (w *OutboxWorker) inc(name string) {
	if w.counters != nil {
		w.counters.Inc(name)
	}
}

func (w *OutboxWorker) Loop(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type LogSink struct{}

func (LogSink) Handle(ctx context.Context, _ db.OutboxItem) error {
	return ctx.Err()
}
