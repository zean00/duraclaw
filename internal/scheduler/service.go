package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"duraclaw/internal/db"
)

type Store interface {
	ClaimDueSchedulerJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]db.SchedulerJob, error)
	CompleteSchedulerJob(ctx context.Context, jobID string, firedAt, nextRunAt time.Time) error
	CreateRun(ctx context.Context, c db.ACPContext, input any) (*db.Run, error)
	ResumeWorkflowTimer(ctx context.Context, runID, workflowRunID, nodeKey string, response map[string]any) error
}

type Service struct {
	store    Store
	owner    string
	limit    int
	leaseFor time.Duration
}

func NewService(store Store, owner string) *Service {
	if owner == "" {
		owner = "duraclaw-scheduler"
	}
	return &Service{store: store, owner: owner, limit: 10, leaseFor: time.Minute}
}

func (s *Service) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	jobs, err := s.store.ClaimDueSchedulerJobs(ctx, s.owner, s.limit, s.leaseFor)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, job := range jobs {
		spec, err := parsePayload(job.Payload)
		if err != nil {
			return created, err
		}
		if spec.WorkflowWake == nil && len(job.Metadata) > 0 {
			var metadata struct {
				WorkflowWake *workflowWake `json:"workflow_wake"`
			}
			_ = json.Unmarshal(job.Metadata, &metadata)
			spec.WorkflowWake = metadata.WorkflowWake
		}
		fireAt := job.NextRunAt
		if spec.WorkflowWake != nil {
			if err := s.store.ResumeWorkflowTimer(ctx, spec.WorkflowWake.RunID, spec.WorkflowWake.WorkflowRunID, spec.WorkflowWake.NodeKey, map[string]any{"scheduler_job_id": job.ID, "fired_at": fireAt}); err != nil {
				return created, err
			}
		} else {
			key := IdempotencyKey(job.ID, fireAt)
			_, err = s.store.CreateRun(ctx, db.ACPContext{
				CustomerID:      job.CustomerID,
				UserID:          spec.UserID,
				AgentInstanceID: spec.AgentInstanceID,
				SessionID:       spec.SessionID,
				RequestID:       "scheduler-" + job.ID,
				IdempotencyKey:  key,
			}, spec.Input)
			if err != nil {
				return created, err
			}
		}
		next, err := Next(job.Schedule, maxTime(now, fireAt))
		if err != nil {
			return created, err
		}
		if err := s.store.CompleteSchedulerJob(ctx, job.ID, fireAt, next); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

type jobPayload struct {
	UserID          string         `json:"user_id"`
	AgentInstanceID string         `json:"agent_instance_id"`
	SessionID       string         `json:"session_id"`
	Input           map[string]any `json:"input"`
	WorkflowWake    *workflowWake  `json:"workflow_wake,omitempty"`
}

type workflowWake struct {
	RunID         string `json:"run_id"`
	WorkflowRunID string `json:"workflow_run_id"`
	NodeKey       string `json:"node_key"`
}

func parsePayload(raw json.RawMessage) (jobPayload, error) {
	var p jobPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.UserID == "" || p.AgentInstanceID == "" || p.SessionID == "" {
		return p, fmt.Errorf("scheduler payload requires user_id, agent_instance_id, and session_id")
	}
	if p.Input == nil {
		p.Input = map[string]any{"text": "Scheduled job fired."}
	}
	return p, nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
