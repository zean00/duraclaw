package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
)

type fakeSchedulerStore struct {
	jobs       []db.SchedulerJob
	subs       []db.ReminderSubscription
	runCtx     db.ACPContext
	completed  bool
	runCreated bool
}

func (s *fakeSchedulerStore) ClaimDueSchedulerJobs(context.Context, string, int, time.Duration) ([]db.SchedulerJob, error) {
	return s.jobs, nil
}

func (s *fakeSchedulerStore) ClaimDueReminderSubscriptions(context.Context, string, int, time.Duration) ([]db.ReminderSubscription, error) {
	return s.subs, nil
}

func (s *fakeSchedulerStore) CompleteSchedulerJob(_ context.Context, _ string, _, _ time.Time) error {
	s.completed = true
	return nil
}

func (s *fakeSchedulerStore) CompleteReminderSubscription(_ context.Context, _ string, _, _ time.Time) error {
	s.completed = true
	return nil
}

func (s *fakeSchedulerStore) CreateRun(_ context.Context, c db.ACPContext, _ any) (*db.Run, error) {
	s.runCtx = c
	s.runCreated = true
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
