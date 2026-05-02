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
	ExtendOutboxClaim(ctx context.Context, id int64, owner string, leaseFor time.Duration) (bool, error)
	ReleaseOutbox(ctx context.Context, id int64, owner string, delay time.Duration) error
	CompleteOutbox(ctx context.Context, id int64, owner string) error
}

type Sink interface {
	Handle(ctx context.Context, item db.OutboxItem) error
}

type BatchSink interface {
	Sink
	HandleBatch(ctx context.Context, topic string, items []db.OutboxItem) error
	SupportsBatch(topic string) bool
}

type OutboxWorker struct {
	store      OutboxStore
	sink       Sink
	owner      string
	limit      int
	retryDelay time.Duration
	leaseFor   time.Duration
	counters   *observability.Counters
	onError    func(error)
}

func NewOutboxWorker(store OutboxStore, sink Sink, owner string) *OutboxWorker {
	if owner == "" {
		owner = "duraclaw-outbox"
	}
	return &OutboxWorker{store: store, sink: sink, owner: owner, limit: 50, retryDelay: 30 * time.Second, leaseFor: 5 * time.Minute}
}

func (w *OutboxWorker) WithCounters(counters *observability.Counters) *OutboxWorker {
	w.counters = counters
	return w
}

func (w *OutboxWorker) WithErrorHandler(fn func(error)) *OutboxWorker {
	w.onError = fn
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
	if batchSink, ok := w.sink.(BatchSink); ok && batchSinkSupportsAnyTopic(batchSink, items) {
		return w.runBatch(ctx, batchSink, items)
	}
	completed := 0
	for _, item := range items {
		n, err := w.runSingleItems(ctx, []db.OutboxItem{item})
		completed += n
		if err != nil {
			return completed, err
		}
	}
	return completed, nil
}

func (w *OutboxWorker) runBatch(ctx context.Context, sink BatchSink, items []db.OutboxItem) (int, error) {
	completed := 0
	for _, group := range groupByTopic(items) {
		if len(group.items) == 0 {
			continue
		}
		if !sink.SupportsBatch(group.topic) {
			n, err := w.runSingleItems(ctx, group.items)
			completed += n
			if err != nil {
				return completed, err
			}
			continue
		}
		stopHeartbeat := w.startHeartbeat(ctx, group.items)
		err := sink.HandleBatch(ctx, group.topic, group.items)
		stopHeartbeat()
		if err != nil {
			for _, item := range group.items {
				_ = w.store.ReleaseOutbox(context.Background(), item.ID, w.owner, w.retryDelay)
			}
			w.inc("outbox_failed")
			return completed, err
		}
		for _, item := range group.items {
			if err := w.store.CompleteOutbox(ctx, item.ID, w.owner); err != nil {
				return completed, err
			}
			completed++
			w.inc("outbox_completed")
		}
	}
	return completed, nil
}

func (w *OutboxWorker) runSingleItems(ctx context.Context, items []db.OutboxItem) (int, error) {
	completed := 0
	for _, item := range items {
		stopHeartbeat := w.startHeartbeat(ctx, []db.OutboxItem{item})
		err := w.sink.Handle(ctx, item)
		stopHeartbeat()
		if err != nil {
			_ = w.store.ReleaseOutbox(context.Background(), item.ID, w.owner, w.retryDelay)
			w.inc("outbox_failed")
			return completed, err
		}
		if err := w.store.CompleteOutbox(ctx, item.ID, w.owner); err != nil {
			return completed, err
		}
		completed++
		w.inc("outbox_completed")
	}
	return completed, nil
}

func (w *OutboxWorker) startHeartbeat(ctx context.Context, items []db.OutboxItem) func() {
	if len(items) == 0 {
		return func() {}
	}
	interval := w.leaseFor / 3
	if interval <= 0 {
		interval = time.Minute
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, item := range items {
					_, _ = w.store.ExtendOutboxClaim(ctx, item.ID, w.owner, w.leaseFor)
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() { close(done) }
}

func batchSinkSupportsAnyTopic(sink BatchSink, items []db.OutboxItem) bool {
	for _, group := range groupByTopic(items) {
		if sink.SupportsBatch(group.topic) {
			return true
		}
	}
	return false
}

type topicGroup struct {
	topic string
	items []db.OutboxItem
}

func groupByTopic(items []db.OutboxItem) []topicGroup {
	positions := map[string]int{}
	var groups []topicGroup
	for _, item := range items {
		topic := item.Topic
		if topic == "" {
			topic = "default"
		}
		pos, ok := positions[topic]
		if !ok {
			positions[topic] = len(groups)
			groups = append(groups, topicGroup{topic: topic})
			pos = len(groups) - 1
		}
		groups[pos].items = append(groups[pos].items, item)
	}
	return groups
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
		if _, err := w.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if w.onError != nil {
				w.onError(err)
			}
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
