package asyncwrite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/observability"
)

type fakeStore struct {
	limits    db.EffectiveRuntimeLimits
	agentIDs  []string
	enqueued  []db.AsyncWriteSpec
	claimed   []db.AsyncWriteJob
	completed []string
	events    []string
}

func (s *fakeStore) EnqueueAsyncWrite(_ context.Context, spec db.AsyncWriteSpec) (int64, error) {
	s.enqueued = append(s.enqueued, spec)
	return int64(len(s.enqueued)), nil
}

func (s *fakeStore) ClaimAsyncWriteJobs(context.Context, string, int, time.Duration) ([]db.AsyncWriteJob, error) {
	jobs := s.claimed
	s.claimed = nil
	return jobs, nil
}

func (s *fakeStore) CompleteAsyncWriteJob(_ context.Context, _ int64, state string, _ *string) error {
	s.completed = append(s.completed, state)
	return nil
}

func (s *fakeStore) ReleaseAsyncWriteJob(context.Context, int64, time.Duration, string) error {
	return nil
}

func (s *fakeStore) AddObservabilityEvent(_ context.Context, _ string, _ string, eventType string, _ any) error {
	s.events = append(s.events, eventType)
	return nil
}

func (s *fakeStore) EffectiveRuntimeLimits(_ context.Context, _ string, agentInstanceID string) (db.EffectiveRuntimeLimits, error) {
	s.agentIDs = append(s.agentIDs, agentInstanceID)
	if s.limits == (db.EffectiveRuntimeLimits{}) {
		return db.DefaultRuntimeLimits(), nil
	}
	return s.limits, nil
}

func TestWriterEnqueuesAndFlushesBufferedJobs(t *testing.T) {
	store := &fakeStore{}
	writer := NewWriter(store, "test", 1)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "observability_event", Payload: map[string]any{"event_type": "x"}})
	if len(store.enqueued) != 0 {
		t.Fatalf("expected buffered enqueue first")
	}
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("enqueued=%#v", store.enqueued)
	}
}

func TestWriterDegradesLargePayloads(t *testing.T) {
	counters := observability.NewCounters()
	store := &fakeStore{limits: db.EffectiveRuntimeLimits{AsyncBufferSize: 1, AsyncDegradeThresholdBytes: 8, MaxAsyncPayloadBytes: 1000}}
	writer := NewWriter(store, "test", 1).WithCounters(counters)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "observability_event", Payload: map[string]any{"big": "this is large"}})
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("enqueued=%#v", store.enqueued)
	}
	var payload map[string]any
	b, _ := json.Marshal(store.enqueued[0].Payload)
	_ = json.Unmarshal(b, &payload)
	if payload["event_type"] != "async_write_degraded" || store.enqueued[0].TargetTable != "degraded" || counters.Snapshot()["async_write_degraded_total"] != 1 {
		t.Fatalf("payload=%#v counters=%#v", payload, counters.Snapshot())
	}
}

func TestWriterUsesAgentScopedLimits(t *testing.T) {
	store := &fakeStore{limits: db.EffectiveRuntimeLimits{AsyncBufferSize: 1, AsyncDegradeThresholdBytes: 8, MaxAsyncPayloadBytes: 1000}}
	writer := NewWriter(store, "test", 1)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", AgentInstanceID: "agent-1", JobType: "observability_event", Payload: map[string]any{"big": "this is large"}})
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.agentIDs) == 0 || store.agentIDs[0] != "agent-1" {
		t.Fatalf("agentIDs=%#v", store.agentIDs)
	}
	if len(store.enqueued) != 1 || store.enqueued[0].TargetTable != "degraded" {
		t.Fatalf("enqueued=%#v", store.enqueued)
	}
}

func TestWriterAppliesClaimedObservabilityJob(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"event_type": "async.event", "payload": map[string]any{"ok": true}})
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "observability_event", Payload: payload}}}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || store.events[0] != "async.event" || len(store.completed) != 1 || store.completed[0] != "completed" {
		t.Fatalf("events=%#v completed=%#v", store.events, store.completed)
	}
}

func TestWriterCompletesDegradedJobsAsDegraded(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"event_type": "async_write_degraded", "payload": map[string]any{"degraded": true}})
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "observability_event", TargetTable: "degraded", Payload: payload}}}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || store.completed[0] != "degraded" {
		t.Fatalf("completed=%#v", store.completed)
	}
}

func TestStatusForErrorRecognizesQuotaExceeded(t *testing.T) {
	if !db.IsQuotaExceeded(db.QuotaExceededError{Kind: "active_runs", Limit: 1, Count: 2}) {
		t.Fatal("expected quota error")
	}
	if db.IsQuotaExceeded(errors.New("other")) {
		t.Fatal("unexpected quota error")
	}
}
