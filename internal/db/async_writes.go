package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type AsyncWriteJob struct {
	ID             int64           `json:"id"`
	CustomerID     string          `json:"customer_id"`
	RunID          *string         `json:"run_id,omitempty"`
	JobType        string          `json:"job_type"`
	TargetTable    string          `json:"target_table"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	AvailableAt    time.Time       `json:"available_at"`
	LeaseOwner     *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	Error          *string         `json:"error,omitempty"`
	Attempts       int             `json:"attempts"`
	CreatedAt      time.Time       `json:"created_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

type AsyncWriteSpec struct {
	CustomerID      string
	AgentInstanceID string
	RunID           string
	JobType         string
	TargetTable     string
	Payload         any
	State           string
	AvailableAt     time.Time
}

func (s *Store) EnqueueAsyncWrite(ctx context.Context, spec AsyncWriteSpec) (int64, error) {
	if spec.CustomerID == "" || spec.JobType == "" {
		return 0, fmt.Errorf("customer_id and job_type are required")
	}
	if spec.State == "" {
		spec.State = "queued"
	}
	if spec.AvailableAt.IsZero() {
		spec.AvailableAt = time.Now().UTC()
	}
	b, _ := json.Marshal(spec.Payload)
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO async_write_jobs(customer_id,run_id,job_type,target_table,payload,state,available_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING id`, spec.CustomerID, nullableRunID(spec.RunID), spec.JobType, spec.TargetTable, b, spec.State, spec.AvailableAt).Scan(&id)
	return id, err
}

func (s *Store) ClaimAsyncWriteJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]AsyncWriteJob, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT id
			FROM async_write_jobs
			WHERE state='queued'
			AND available_at <= now()
			AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE async_write_jobs j
		SET state='leased', lease_owner=$2, lease_expires_at=now()+$3::interval, attempts=attempts+1
		FROM candidate
		WHERE j.id=candidate.id
		RETURNING j.id, j.customer_id, j.run_id::text, j.job_type, j.target_table, j.payload, j.state, j.available_at, j.lease_owner, j.lease_expires_at, j.error, j.attempts, j.created_at, j.completed_at`,
		limit, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []AsyncWriteJob
	for rows.Next() {
		var job AsyncWriteJob
		if err := rows.Scan(&job.ID, &job.CustomerID, &job.RunID, &job.JobType, &job.TargetTable, &job.Payload, &job.State, &job.AvailableAt, &job.LeaseOwner, &job.LeaseExpiresAt, &job.Error, &job.Attempts, &job.CreatedAt, &job.CompletedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) CompleteAsyncWriteJob(ctx context.Context, id int64, state string, errText *string) error {
	if state == "" {
		state = "completed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE async_write_jobs
		SET state=$2, error=$3, lease_owner=NULL, lease_expires_at=NULL, completed_at=now()
		WHERE id=$1`, id, state, errText)
	return err
}

func (s *Store) ReleaseAsyncWriteJob(ctx context.Context, id int64, delay time.Duration, errText string) error {
	availableAt := time.Now().UTC().Add(delay)
	_, err := s.pool.Exec(ctx, `
		UPDATE async_write_jobs
		SET state='queued', error=$3, lease_owner=NULL, lease_expires_at=NULL, available_at=$2
		WHERE id=$1`, id, availableAt, errText)
	return err
}
