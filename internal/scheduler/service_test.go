package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
)

type fakeSchedulerStore struct {
	jobs                    []db.SchedulerJob
	sharedJobs              []db.SharedSchedulerJob
	sharedSubs              []db.SharedSchedulerSubscription
	subs                    []db.ReminderSubscription
	runCtx                  db.ACPContext
	completed               bool
	sharedCompleted         bool
	runCreated              bool
	runCreatedCount         int
	outboundCreated         int
	runDeliveryCount        int
	completedDeliveries     []db.SharedSchedulerRunDelivery
	deliveryOutboundCreated int
	sharedListLimit         int
}

func (s *fakeSchedulerStore) ClaimDueSchedulerJobs(context.Context, string, int, time.Duration) ([]db.SchedulerJob, error) {
	return s.jobs, nil
}

func (s *fakeSchedulerStore) ClaimDueReminderSubscriptions(context.Context, string, int, time.Duration) ([]db.ReminderSubscription, error) {
	return s.subs, nil
}

func (s *fakeSchedulerStore) ClaimDueSharedSchedulerJobs(context.Context, string, int, time.Duration) ([]db.SharedSchedulerJob, error) {
	return s.sharedJobs, nil
}

func (s *fakeSchedulerStore) CompleteSchedulerJob(_ context.Context, _ string, _, _ time.Time) error {
	s.completed = true
	return nil
}

func (s *fakeSchedulerStore) CompleteReminderSubscription(_ context.Context, _ string, _, _ time.Time) error {
	s.completed = true
	return nil
}

func (s *fakeSchedulerStore) CompleteSharedSchedulerJob(_ context.Context, _ string, _, _ time.Time) error {
	s.sharedCompleted = true
	return nil
}

func (s *fakeSchedulerStore) ListSharedSchedulerSubscriptions(_ context.Context, _, _, _ string, limit int) ([]db.SharedSchedulerSubscription, error) {
	s.sharedListLimit = limit
	return s.sharedSubs, nil
}

func (s *fakeSchedulerStore) RecordSharedSchedulerFire(context.Context, string, string, time.Time, string, int, int, string, any, any) error {
	return nil
}

func (s *fakeSchedulerStore) CreateSharedSchedulerOutbound(context.Context, db.SharedSchedulerJob, db.SharedSchedulerSubscription, time.Time, map[string]any) (bool, error) {
	s.outboundCreated++
	return true, nil
}

func (s *fakeSchedulerStore) CreateSharedSchedulerRunDelivery(context.Context, db.SharedSchedulerJob, time.Time, string, string, any) (bool, error) {
	s.runDeliveryCount++
	return true, nil
}

func (s *fakeSchedulerStore) ClaimCompletedSharedSchedulerRunDeliveries(context.Context, int) ([]db.SharedSchedulerRunDelivery, error) {
	out := s.completedDeliveries
	s.completedDeliveries = nil
	return out, nil
}

func (s *fakeSchedulerStore) CreateSharedSchedulerDeliveryOutbound(context.Context, db.SharedSchedulerRunDelivery, string, string, map[string]any) (bool, error) {
	s.deliveryOutboundCreated++
	return true, nil
}

func (s *fakeSchedulerStore) CompleteSharedSchedulerRunDelivery(context.Context, string, string, string) error {
	return nil
}

func (s *fakeSchedulerStore) CreateRun(_ context.Context, c db.ACPContext, _ any) (*db.Run, error) {
	s.runCtx = c
	s.runCreated = true
	s.runCreatedCount++
	return &db.Run{ID: "run-1"}, nil
}

func (s *fakeSchedulerStore) ResumeWorkflowTimer(context.Context, string, string, string, map[string]any) error {
	s.completed = true
	return nil
}

func TestServiceCreatesRunWithDeterministicIdempotencyKey(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"user_id": "u", "agent_instance_id": "a", "session_id": "s",
		"input": map[string]any{"text": "tick"},
	})
	fireAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeSchedulerStore{jobs: []db.SchedulerJob{{
		ID: "job-1", CustomerID: "c", Schedule: "*/5 * * * *", NextRunAt: fireAt, Payload: payload,
	}}}
	count, err := NewService(store, "owner").RunOnce(context.Background(), fireAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !store.completed {
		t.Fatalf("count=%d completed=%v", count, store.completed)
	}
	wantKey := IdempotencyKey("job-1", fireAt)
	if store.runCtx.IdempotencyKey != wantKey || store.runCtx.CustomerID != "c" || store.runCtx.UserID != "u" {
		t.Fatalf("run ctx=%#v want key %s", store.runCtx, wantKey)
	}
}

func TestServiceResumesWorkflowTimer(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"user_id": "u", "agent_instance_id": "a", "session_id": "s",
		"input":         map[string]any{"text": "wake"},
		"workflow_wake": map[string]any{"run_id": "run-parent", "workflow_run_id": "wf-run", "node_key": "wait"},
	})
	fireAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeSchedulerStore{jobs: []db.SchedulerJob{{
		ID: "job-1", CustomerID: "c", JobType: "workflow_timer", Schedule: "@once", NextRunAt: fireAt, Payload: payload,
	}}}
	count, err := NewService(store, "owner").RunOnce(context.Background(), fireAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !store.completed {
		t.Fatalf("count=%d completed=%v", count, store.completed)
	}
	if store.runCreated {
		t.Fatalf("workflow timer wake should not create a fresh run")
	}
}

func TestServiceFansOutReminderSubscriptions(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"text": "drink water"})
	fireAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeSchedulerStore{subs: []db.ReminderSubscription{{
		ID: "sub-1", CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", Schedule: "*/5 * * * *", NextRunAt: fireAt, Payload: payload,
	}}}
	count, err := NewService(store, "owner").RunOnce(context.Background(), fireAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !store.runCreated || !store.completed {
		t.Fatalf("count=%d runCreated=%v completed=%v", count, store.runCreated, store.completed)
	}
	if store.runCtx.IdempotencyKey != IdempotencyKey("sub-1", fireAt) || store.runCtx.RequestID != "reminder-sub-1" {
		t.Fatalf("run ctx=%#v", store.runCtx)
	}
}

func TestServiceFansOutSharedSchedulerWithoutExternalService(t *testing.T) {
	fireAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeSchedulerStore{
		sharedJobs: []db.SharedSchedulerJob{{
			ID: "shared-1", CustomerID: "c", JobKey: "prayer", Schedule: "@once", NextRunAt: fireAt, FanoutAction: "outbound_intent", MessageTemplate: "Prayer time",
		}},
		sharedSubs: []db.SharedSchedulerSubscription{
			{ID: "sub-1", SharedJobID: "shared-1", CustomerID: "c", UserID: "u1", SessionID: "s1", AgentInstanceID: "a", Enabled: true},
			{ID: "sub-2", SharedJobID: "shared-1", CustomerID: "c", UserID: "u2", SessionID: "s2", AgentInstanceID: "a", Enabled: true},
		},
	}
	count, err := NewService(store, "owner").RunOnce(context.Background(), fireAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || store.outboundCreated != 2 || !store.sharedCompleted {
		t.Fatalf("count=%d outbound=%d completed=%v", count, store.outboundCreated, store.sharedCompleted)
	}
	if store.sharedListLimit != 0 {
		t.Fatalf("shared subscriber list limit=%d", store.sharedListLimit)
	}
}

func TestServiceCreatesOneSharedDurableRunPerAgentInstance(t *testing.T) {
	fireAt := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	store := &fakeSchedulerStore{
		sharedJobs: []db.SharedSchedulerJob{{
			ID: "shared-1", CustomerID: "c", JobKey: "prayer", Schedule: "@once", NextRunAt: fireAt, FanoutAction: "durable_run", MessageTemplate: "Prayer time",
		}},
		sharedSubs: []db.SharedSchedulerSubscription{
			{ID: "sub-1", SharedJobID: "shared-1", CustomerID: "c", UserID: "u1", SessionID: "s1", AgentInstanceID: "a1", Enabled: true},
			{ID: "sub-2", SharedJobID: "shared-1", CustomerID: "c", UserID: "u2", SessionID: "s2", AgentInstanceID: "a1", Enabled: true},
			{ID: "sub-3", SharedJobID: "shared-1", CustomerID: "c", UserID: "u3", SessionID: "s3", AgentInstanceID: "a2", Enabled: true},
		},
	}
	count, err := NewService(store, "owner").RunOnce(context.Background(), fireAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || store.runCreatedCount != 2 || store.runDeliveryCount != 2 {
		t.Fatalf("count=%d runs=%d deliveries=%d", count, store.runCreatedCount, store.runDeliveryCount)
	}
	if store.runCtx.UserID != "shared-scheduler" {
		t.Fatalf("run ctx=%#v", store.runCtx)
	}
}

func TestServicePushesCompletedSharedDurableRunToSubscribers(t *testing.T) {
	subs, _ := json.Marshal([]map[string]any{
		{"user_id": "u1", "session_id": "s1"},
		{"user_id": "u2", "session_id": "s2"},
	})
	final, _ := json.Marshal(map[string]any{"parts": []map[string]any{{"type": "text", "text": "Prayer time"}}})
	store := &fakeSchedulerStore{completedDeliveries: []db.SharedSchedulerRunDelivery{{
		ID: "delivery-1", CustomerID: "c", RunID: "run-1", SubscriberPayloads: subs, FinalMessage: final,
	}}}
	count, err := NewService(store, "owner").RunOnce(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || store.deliveryOutboundCreated != 2 {
		t.Fatalf("count=%d outbound=%d", count, store.deliveryOutboundCreated)
	}
}

func TestMapSharedTargetsNestedList(t *testing.T) {
	decoded := map[string]any{"result": map[string]any{"users": []any{
		map[string]any{"user_id": "u1", "prayer_name": "Maghrib"},
		map[string]any{"user_id": "missing"},
	}}}
	subs := []db.SharedSchedulerSubscription{{UserID: "u1", Enabled: true}, {UserID: "u2", Enabled: true}}
	got, summary, err := mapSharedTargets(decoded, responseMappingConfig{TargetPath: "result.users", IDPath: "user_id"}, subs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].sub.UserID != "u1" || summary["selected_count"] != 1 {
		t.Fatalf("got=%#v summary=%#v", got, summary)
	}
}

func TestMapSharedTargetsObjectKeys(t *testing.T) {
	decoded := map[string]any{"accounts": map[string]any{"u1": map[string]any{"prayer_name": "Isya"}}}
	subs := []db.SharedSchedulerSubscription{{UserID: "u1", Enabled: true}, {UserID: "u2", Enabled: true}}
	got, _, err := mapSharedTargets(decoded, responseMappingConfig{TargetPath: "accounts", TargetShape: "object", ObjectIDSource: "key"}, subs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].sub.UserID != "u1" {
		t.Fatalf("got=%#v", got)
	}
}

func TestMapSharedTargetsMissingPathFallsBackToAllSubscribers(t *testing.T) {
	subs := []db.SharedSchedulerSubscription{{UserID: "u1", Enabled: true}, {UserID: "u2", Enabled: true}}
	got, summary, err := mapSharedTargets(map[string]any{"ok": true}, responseMappingConfig{TargetPath: "result.users", IDPath: "user_id"}, subs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || summary["fallback"] != "all_subscribers" {
		t.Fatalf("got=%#v summary=%#v", got, summary)
	}
}

func TestMapSharedTargetsExplicitEmptyListSelectsNoRecipients(t *testing.T) {
	subs := []db.SharedSchedulerSubscription{{UserID: "u1", Enabled: true}}
	got, summary, err := mapSharedTargets(map[string]any{"users": []any{}}, responseMappingConfig{TargetPath: "users", IDPath: "user_id"}, subs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || summary["selected_count"] != 0 {
		t.Fatalf("got=%#v summary=%#v", got, summary)
	}
}
