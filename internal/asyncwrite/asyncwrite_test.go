package asyncwrite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/observability"
)

type fakeStore struct {
	limits     db.EffectiveRuntimeLimits
	enqueueErr error
	claimErr   error
	agentIDs   []string
	enqueued   []db.AsyncWriteSpec
	claimed    []db.AsyncWriteJob
	completed  []string
	released   []int64
	events     []string
}

func (s *fakeStore) EnqueueAsyncWrite(_ context.Context, spec db.AsyncWriteSpec) (int64, error) {
	if s.enqueueErr != nil {
		return 0, s.enqueueErr
	}
	s.enqueued = append(s.enqueued, spec)
	return int64(len(s.enqueued)), nil
}

func (s *fakeStore) ClaimAsyncWriteJobs(context.Context, string, int, time.Duration) ([]db.AsyncWriteJob, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	jobs := s.claimed
	s.claimed = nil
	return jobs, nil
}

func (s *fakeStore) CompleteAsyncWriteJob(_ context.Context, _ int64, state string, _ *string) error {
	s.completed = append(s.completed, state)
	return nil
}

func (s *fakeStore) ReleaseAsyncWriteJob(_ context.Context, id int64, _ time.Duration, _ string) error {
	s.released = append(s.released, id)
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

func TestWriterDefaultsNilAndOverflowPaths(t *testing.T) {
	NewWriter(&fakeStore{}, "", 0).Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "observability_event"})
	(*Writer)(nil).Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "observability_event"})
	if _, err := (*Writer)(nil).RunOnce(context.Background()); err == nil {
		t.Fatal("expected nil writer error")
	}

	store := &fakeStore{}
	writer := NewWriter(store, "test", 1)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "first"})
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "second"})
	if len(store.enqueued) != 1 || store.enqueued[0].JobType != "second" {
		t.Fatalf("enqueued=%#v", store.enqueued)
	}
}

func TestWriterDropsWhenOverflowPersistFails(t *testing.T) {
	counters := observability.NewCounters()
	store := &fakeStore{enqueueErr: errors.New("db down")}
	writer := NewWriter(store, "test", 1).WithCounters(counters)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "first"})
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "second"})
	if counters.Snapshot()["async_write_dropped_total"] != 1 {
		t.Fatalf("counters=%#v", counters.Snapshot())
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

func TestWriterDropsTooLargePayloads(t *testing.T) {
	counters := observability.NewCounters()
	store := &fakeStore{limits: db.EffectiveRuntimeLimits{AsyncBufferSize: 1, AsyncDegradeThresholdBytes: 8, MaxAsyncPayloadBytes: 10}}
	writer := NewWriter(store, "test", 1).WithCounters(counters)
	writer.Enqueue(context.Background(), db.AsyncWriteSpec{CustomerID: "c", JobType: "observability_event", Payload: map[string]any{"big": "this is large"}})
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.enqueued) != 1 || store.enqueued[0].State != "dropped" {
		t.Fatalf("enqueued=%#v", store.enqueued)
	}
	if counters.Snapshot()["async_write_dropped_total"] != 1 {
		t.Fatalf("counters=%#v", counters.Snapshot())
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

func TestWriterPropagatesClaimErrors(t *testing.T) {
	store := &fakeStore{claimErr: errors.New("claim failed")}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err == nil {
		t.Fatal("expected claim error")
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

func TestWriterUsesDefaultsForClaimedJobs(t *testing.T) {
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "observability_event", State: "dropped", Payload: json.RawMessage(`{}`)}}}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || store.events[0] != "async_write" || len(store.completed) != 1 || store.completed[0] != "dropped" {
		t.Fatalf("events=%#v completed=%#v", store.events, store.completed)
	}
}

func TestWriterAppliesClaimedOTLPJob(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	payload, _ := json.Marshal(map[string]any{"name": "async.trace", "attributes": map[string]any{"trace_id": "abc"}})
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "otlp_event", Payload: payload}}}
	writer := NewWriter(store, "test", 1).WithOTLPExporter(observability.OTLPExporter{Endpoint: server.URL})
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/v1/traces" || len(store.completed) != 1 || store.completed[0] != "completed" {
		t.Fatalf("paths=%#v completed=%#v", paths, store.completed)
	}
}

func TestWriterOTLPJobNoopsWhenExporterDisabled(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"name": "async.trace"})
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "otlp_event", Payload: payload}}}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || store.completed[0] != "completed" {
		t.Fatalf("completed=%#v", store.completed)
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

func TestWriterStopsRetryingAfterMaxAttempts(t *testing.T) {
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "observability_event", Payload: json.RawMessage(`{bad`), Attempts: 5}}}
	counters := observability.NewCounters()
	writer := NewWriter(store, "test", 1).WithCounters(counters)
	if _, err := writer.RunOnce(context.Background()); err == nil {
		t.Fatal("expected apply error")
	}
	if len(store.completed) != 1 || store.completed[0] != "failed" || len(store.released) != 0 {
		t.Fatalf("completed=%#v released=%#v", store.completed, store.released)
	}
	if counters.Snapshot()["async_write_failed_terminal_total"] != 1 {
		t.Fatalf("counters=%#v", counters.Snapshot())
	}
}

func TestWriterReleasesBeforeMaxAttempts(t *testing.T) {
	store := &fakeStore{claimed: []db.AsyncWriteJob{{ID: 1, CustomerID: "c", JobType: "observability_event", Payload: json.RawMessage(`{bad`), Attempts: 1}}}
	writer := NewWriter(store, "test", 1)
	if _, err := writer.RunOnce(context.Background()); err == nil {
		t.Fatal("expected apply error")
	}
	if len(store.released) != 1 || len(store.completed) != 0 {
		t.Fatalf("completed=%#v released=%#v", store.completed, store.released)
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
