package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SharedSchedulerJob struct {
	ID               string          `json:"id"`
	CustomerID       string          `json:"customer_id"`
	JobKey           string          `json:"job_key"`
	Title            string          `json:"title"`
	JobType          string          `json:"job_type"`
	Schedule         string          `json:"schedule"`
	Timezone         string          `json:"timezone"`
	Enabled          bool            `json:"enabled"`
	NextRunAt        time.Time       `json:"next_run_at"`
	ExternalService  json.RawMessage `json:"external_service"`
	FanoutAction     string          `json:"fanout_action"`
	MessageTemplate  string          `json:"message_template"`
	RunInputTemplate json.RawMessage `json:"run_input_template"`
	Metadata         json.RawMessage `json:"metadata"`
	LastFiredAt      *time.Time      `json:"last_fired_at,omitempty"`
	LeaseOwner       *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt   *time.Time      `json:"lease_expires_at,omitempty"`
}

type SharedSchedulerJobSpec struct {
	CustomerID       string
	JobKey           string
	Title            string
	JobType          string
	Schedule         string
	Timezone         string
	NextRunAt        time.Time
	ExternalService  any
	FanoutAction     string
	MessageTemplate  string
	RunInputTemplate any
	Metadata         any
}

type SharedSchedulerJobUpdate struct {
	Title            *string
	JobType          *string
	Schedule         *string
	Timezone         *string
	NextRunAt        *time.Time
	ExternalService  any
	FanoutAction     *string
	MessageTemplate  *string
	RunInputTemplate any
	Metadata         any
	Enabled          *bool
}

type SharedSchedulerSubscription struct {
	ID                 string          `json:"id"`
	SharedJobID        string          `json:"shared_job_id"`
	CustomerID         string          `json:"customer_id"`
	UserID             string          `json:"user_id"`
	SessionID          string          `json:"session_id"`
	AgentInstanceID    string          `json:"agent_instance_id"`
	Enabled            bool            `json:"enabled"`
	SubscriberMetadata json.RawMessage `json:"subscriber_metadata"`
}

type SharedSchedulerSubscriptionSpec struct {
	SharedJobID        string
	CustomerID         string
	UserID             string
	SessionID          string
	AgentInstanceID    string
	Enabled            *bool
	SubscriberMetadata any
}

type SharedSchedulerSubscriptionUpdate struct {
	SessionID          *string
	AgentInstanceID    *string
	SubscriberMetadata any
	Enabled            *bool
}

type SharedSchedulerRunDelivery struct {
	ID                 string          `json:"id"`
	SharedJobID        string          `json:"shared_job_id"`
	CustomerID         string          `json:"customer_id"`
	AgentInstanceID    string          `json:"agent_instance_id"`
	RunID              string          `json:"run_id"`
	SubscriberPayloads json.RawMessage `json:"subscriber_payloads"`
	FinalMessage       json.RawMessage `json:"final_message"`
}

func scanSharedSchedulerJob(row pgx.Row) (*SharedSchedulerJob, error) {
	var job SharedSchedulerJob
	err := row.Scan(&job.ID, &job.CustomerID, &job.JobKey, &job.Title, &job.JobType, &job.Schedule, &job.Timezone, &job.Enabled, &job.NextRunAt, &job.ExternalService, &job.FanoutAction, &job.MessageTemplate, &job.RunInputTemplate, &job.Metadata, &job.LastFiredAt, &job.LeaseOwner, &job.LeaseExpiresAt)
	return &job, err
}

func (s *Store) CreateSharedSchedulerJob(ctx context.Context, spec SharedSchedulerJobSpec) (*SharedSchedulerJob, error) {
	if spec.CustomerID == "" || spec.JobKey == "" || spec.Schedule == "" || spec.NextRunAt.IsZero() {
		return nil, fmt.Errorf("customer_id, job_key, schedule, and next_run_at are required")
	}
	if spec.JobType == "" {
		spec.JobType = "eligibility_poll"
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	if spec.FanoutAction == "" {
		spec.FanoutAction = "outbound_intent"
	}
	if spec.FanoutAction != "outbound_intent" && spec.FanoutAction != "durable_run" {
		return nil, fmt.Errorf("fanout_action must be outbound_intent or durable_run")
	}
	ext, _ := json.Marshal(spec.ExternalService)
	runTpl, _ := json.Marshal(spec.RunInputTemplate)
	meta, _ := json.Marshal(spec.Metadata)
	return scanSharedSchedulerJob(s.pool.QueryRow(ctx, `
		INSERT INTO shared_scheduler_jobs(customer_id,job_key,title,job_type,schedule,timezone,next_run_at,external_service,fanout_action,message_template,run_input_template,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,COALESCE(NULLIF($8::jsonb,'null'::jsonb),'{}'::jsonb),$9,$10,COALESCE(NULLIF($11::jsonb,'null'::jsonb),'{}'::jsonb),COALESCE(NULLIF($12::jsonb,'null'::jsonb),'{}'::jsonb))
		RETURNING id::text, customer_id, job_key, title, job_type, schedule, timezone, enabled, next_run_at, external_service, fanout_action, message_template, run_input_template, metadata, last_fired_at, lease_owner, lease_expires_at`,
		spec.CustomerID, spec.JobKey, spec.Title, spec.JobType, spec.Schedule, spec.Timezone, spec.NextRunAt, ext, spec.FanoutAction, spec.MessageTemplate, runTpl, meta))
}

func (s *Store) ClaimDueSharedSchedulerJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]SharedSchedulerJob, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT id FROM shared_scheduler_jobs
			WHERE enabled=true AND next_run_at <= now() AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY next_run_at FOR UPDATE SKIP LOCKED LIMIT $1
		)
		UPDATE shared_scheduler_jobs j
		SET lease_owner=$2, lease_expires_at=now()+$3::interval
		FROM candidate WHERE j.id=candidate.id
		RETURNING j.id::text, j.customer_id, j.job_key, j.title, j.job_type, j.schedule, j.timezone, j.enabled, j.next_run_at, j.external_service, j.fanout_action, j.message_template, j.run_input_template, j.metadata, j.last_fired_at, j.lease_owner, j.lease_expires_at`,
		limit, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedSchedulerJob
	for rows.Next() {
		job, err := scanSharedSchedulerJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

func (s *Store) ListSharedSchedulerJobs(ctx context.Context, customerID string, limit int) ([]SharedSchedulerJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, job_key, title, job_type, schedule, timezone, enabled, next_run_at, external_service, fanout_action, message_template, run_input_template, metadata, last_fired_at, lease_owner, lease_expires_at
		FROM shared_scheduler_jobs WHERE customer_id=$1 ORDER BY job_key LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedSchedulerJob
	for rows.Next() {
		job, err := scanSharedSchedulerJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSharedSchedulerJob(ctx context.Context, id, customerID string, update SharedSchedulerJobUpdate) (*SharedSchedulerJob, error) {
	if update.FanoutAction != nil && *update.FanoutAction != "outbound_intent" && *update.FanoutAction != "durable_run" {
		return nil, fmt.Errorf("fanout_action must be outbound_intent or durable_run")
	}
	ext, _ := json.Marshal(update.ExternalService)
	runTpl, _ := json.Marshal(update.RunInputTemplate)
	meta, _ := json.Marshal(update.Metadata)
	return scanSharedSchedulerJob(s.pool.QueryRow(ctx, `
		UPDATE shared_scheduler_jobs
		SET title=COALESCE($3::text,title), job_type=COALESCE($4::text,job_type), schedule=COALESCE($5::text,schedule), timezone=COALESCE($6::text,timezone),
			next_run_at=COALESCE($7::timestamptz,next_run_at), external_service=CASE WHEN $8::jsonb IS NULL OR $8::jsonb='null'::jsonb THEN external_service ELSE $8::jsonb END,
			fanout_action=COALESCE($9::text,fanout_action), message_template=COALESCE($10::text,message_template),
			run_input_template=CASE WHEN $11::jsonb IS NULL OR $11::jsonb='null'::jsonb THEN run_input_template ELSE $11::jsonb END,
			metadata=CASE WHEN $12::jsonb IS NULL OR $12::jsonb='null'::jsonb THEN metadata ELSE $12::jsonb END,
			enabled=COALESCE($13::boolean,enabled), lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND customer_id=$2
		RETURNING id::text, customer_id, job_key, title, job_type, schedule, timezone, enabled, next_run_at, external_service, fanout_action, message_template, run_input_template, metadata, last_fired_at, lease_owner, lease_expires_at`,
		id, customerID, nullableString(update.Title), nullableString(update.JobType), nullableString(update.Schedule), nullableString(update.Timezone), nullableTime(update.NextRunAt), nullableBytes(ext), nullableString(update.FanoutAction), nullableString(update.MessageTemplate), nullableBytes(runTpl), nullableBytes(meta), nullableBool(update.Enabled)))
}

func (s *Store) DeleteSharedSchedulerJob(ctx context.Context, id, customerID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM shared_scheduler_jobs WHERE id=$1 AND customer_id=$2`, id, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("shared scheduler job not found")
	}
	return nil
}

func (s *Store) CompleteSharedSchedulerJob(ctx context.Context, jobID string, firedAt, nextRunAt time.Time) error {
	if nextRunAt.IsZero() {
		_, err := s.pool.Exec(ctx, `UPDATE shared_scheduler_jobs SET last_fired_at=$2, enabled=false, lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1`, jobID, firedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE shared_scheduler_jobs SET last_fired_at=$2, next_run_at=$3, lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1`, jobID, firedAt, nextRunAt)
	return err
}

func scanSharedSubscription(row pgx.Row) (*SharedSchedulerSubscription, error) {
	var sub SharedSchedulerSubscription
	err := row.Scan(&sub.ID, &sub.SharedJobID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.Enabled, &sub.SubscriberMetadata)
	return &sub, err
}

func (s *Store) CreateSharedSchedulerSubscription(ctx context.Context, spec SharedSchedulerSubscriptionSpec) (*SharedSchedulerSubscription, error) {
	if spec.SharedJobID == "" || spec.CustomerID == "" || spec.UserID == "" || spec.SessionID == "" || spec.AgentInstanceID == "" {
		return nil, fmt.Errorf("shared_job_id, customer_id, user_id, session_id, and agent_instance_id are required")
	}
	if err := s.EnsureSession(ctx, ACPContext{CustomerID: spec.CustomerID, UserID: spec.UserID, AgentInstanceID: spec.AgentInstanceID, SessionID: spec.SessionID}); err != nil {
		return nil, err
	}
	enabled := true
	if spec.Enabled != nil {
		enabled = *spec.Enabled
	}
	meta, _ := json.Marshal(spec.SubscriberMetadata)
	return scanSharedSubscription(s.pool.QueryRow(ctx, `
		INSERT INTO shared_scheduler_subscriptions(shared_job_id,customer_id,user_id,session_id,agent_instance_id,enabled,subscriber_metadata)
		SELECT id, customer_id, $3, $4, $5, $6, COALESCE(NULLIF($7::jsonb,'null'::jsonb),'{}'::jsonb)
		FROM shared_scheduler_jobs
		WHERE id=$1 AND customer_id=$2
		ON CONFLICT (shared_job_id,user_id) DO UPDATE SET session_id=EXCLUDED.session_id, agent_instance_id=EXCLUDED.agent_instance_id, enabled=EXCLUDED.enabled, subscriber_metadata=EXCLUDED.subscriber_metadata, updated_at=now()
		RETURNING id::text, shared_job_id::text, customer_id, user_id, session_id, agent_instance_id, enabled, subscriber_metadata`,
		spec.SharedJobID, spec.CustomerID, spec.UserID, spec.SessionID, spec.AgentInstanceID, enabled, meta))
}

func (s *Store) ListSharedSchedulerSubscriptions(ctx context.Context, customerID, userID, sharedJobID string, limit int) ([]SharedSchedulerSubscription, error) {
	limitSQL := ""
	args := []any{customerID, userID, sharedJobID}
	if limit > 0 {
		args = append(args, limit)
		limitSQL = " LIMIT $4"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, shared_job_id::text, customer_id, user_id, session_id, agent_instance_id, enabled, subscriber_metadata
		FROM shared_scheduler_subscriptions
		WHERE customer_id=$1 AND ($2='' OR user_id=$2) AND ($3='' OR shared_job_id::text=$3)
		ORDER BY created_at DESC`+limitSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedSchedulerSubscription
	for rows.Next() {
		sub, err := scanSharedSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSharedSchedulerSubscription(ctx context.Context, id, customerID, userID string, update SharedSchedulerSubscriptionUpdate) (*SharedSchedulerSubscription, error) {
	meta, _ := json.Marshal(update.SubscriberMetadata)
	return scanSharedSubscription(s.pool.QueryRow(ctx, `
		UPDATE shared_scheduler_subscriptions
		SET session_id=COALESCE($4::text,session_id), agent_instance_id=COALESCE($5::text,agent_instance_id),
			subscriber_metadata=CASE WHEN $6::jsonb IS NULL OR $6::jsonb='null'::jsonb THEN subscriber_metadata ELSE $6::jsonb END,
			enabled=COALESCE($7::boolean,enabled), updated_at=now()
		WHERE id=$1 AND customer_id=$2 AND user_id=$3
		RETURNING id::text, shared_job_id::text, customer_id, user_id, session_id, agent_instance_id, enabled, subscriber_metadata`,
		id, customerID, userID, nullableString(update.SessionID), nullableString(update.AgentInstanceID), nullableBytes(meta), nullableBool(update.Enabled)))
}

func (s *Store) DeleteSharedSchedulerSubscription(ctx context.Context, id, customerID, userID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM shared_scheduler_subscriptions WHERE id=$1 AND customer_id=$2 AND user_id=$3`, id, customerID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("shared scheduler subscription not found")
	}
	return nil
}

func (s *Store) RecordSharedSchedulerFire(ctx context.Context, jobID, customerID string, fireAt time.Time, status string, selected, created int, errText string, req, resp any) error {
	reqJSON, _ := json.Marshal(req)
	respJSON, _ := json.Marshal(resp)
	var errArg any
	if errText != "" {
		errArg = errText
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO shared_scheduler_fires(shared_job_id,customer_id,scheduled_fire_at,status,selected_count,created_count,error,request_summary,response_summary)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (shared_job_id,scheduled_fire_at) DO UPDATE SET status=$4, selected_count=$5, created_count=$6, error=$7, request_summary=$8, response_summary=$9`,
		jobID, customerID, fireAt, status, selected, created, errArg, reqJSON, respJSON)
	return err
}

func (s *Store) CreateSharedSchedulerOutbound(ctx context.Context, job SharedSchedulerJob, sub SharedSchedulerSubscription, fireAt time.Time, payload map[string]any) (bool, error) {
	returning := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var deliveryID string
		err := tx.QueryRow(ctx, `
			INSERT INTO shared_scheduler_deliveries(shared_job_id,customer_id,user_id,scheduled_fire_at,action)
			VALUES($1,$2,$3,$4,'outbound_intent')
			ON CONFLICT DO NOTHING
			RETURNING id::text`, job.ID, job.CustomerID, sub.UserID, fireAt).Scan(&deliveryID)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		payloadJSON, _ := json.Marshal(payload)
		intentID, _, err := createOutboundIntentTx(ctx, tx, OutboundIntent{CustomerID: job.CustomerID, UserID: sub.UserID, SessionID: sub.SessionID, Type: "message", Payload: payloadJSON})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE shared_scheduler_deliveries SET outbound_intent_id=$2 WHERE id=$1`, deliveryID, intentID)
		returning = err == nil
		return err
	})
	return returning, err
}

func (s *Store) CreateSharedSchedulerRunDelivery(ctx context.Context, job SharedSchedulerJob, fireAt time.Time, agentInstanceID string, runID string, subscribers any) (bool, error) {
	if agentInstanceID == "" || runID == "" {
		return false, fmt.Errorf("agent_instance_id and run_id are required")
	}
	subscriberPayloads, _ := json.Marshal(subscribers)
	var deliveryID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO shared_scheduler_deliveries(shared_job_id,customer_id,user_id,scheduled_fire_at,action,run_id,subscriber_payloads,status)
		VALUES($1,$2,$3,$4,'durable_run',$5,$6,'pending')
		ON CONFLICT DO NOTHING
		RETURNING id::text`, job.ID, job.CustomerID, agentInstanceID, fireAt, runID, subscriberPayloads).Scan(&deliveryID)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) ClaimCompletedSharedSchedulerRunDeliveries(ctx context.Context, limit int) ([]SharedSchedulerRunDelivery, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT d.id
			FROM shared_scheduler_deliveries d
			JOIN runs r ON r.id=d.run_id
			WHERE d.action='durable_run'
			AND d.status='pending'
			AND r.state='completed'
			ORDER BY d.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE shared_scheduler_deliveries d
		SET status='processing', updated_at=now()
		FROM candidate
		WHERE d.id=candidate.id
		RETURNING d.id::text, d.shared_job_id::text, d.customer_id, d.user_id, d.run_id::text, d.subscriber_payloads,
			COALESCE((SELECT m.content FROM runs r JOIN messages m ON m.id=r.final_message_id WHERE r.id=d.run_id), '{}'::jsonb)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SharedSchedulerRunDelivery
	for rows.Next() {
		var delivery SharedSchedulerRunDelivery
		if err := rows.Scan(&delivery.ID, &delivery.SharedJobID, &delivery.CustomerID, &delivery.AgentInstanceID, &delivery.RunID, &delivery.SubscriberPayloads, &delivery.FinalMessage); err != nil {
			return nil, err
		}
		out = append(out, delivery)
	}
	return out, rows.Err()
}

func (s *Store) CreateSharedSchedulerDeliveryOutbound(ctx context.Context, delivery SharedSchedulerRunDelivery, userID, sessionID string, payload map[string]any) (bool, error) {
	if userID == "" || sessionID == "" {
		return false, fmt.Errorf("user_id and session_id are required")
	}
	created := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var rowID string
		err := tx.QueryRow(ctx, `
			INSERT INTO shared_scheduler_deliveries(shared_job_id,customer_id,user_id,scheduled_fire_at,action)
			SELECT shared_job_id, customer_id, $2, scheduled_fire_at, 'durable_run_outbound'
			FROM shared_scheduler_deliveries
			WHERE id=$1
			ON CONFLICT DO NOTHING
			RETURNING id::text`, delivery.ID, userID).Scan(&rowID)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		payloadJSON, _ := json.Marshal(payload)
		intentID, _, err := createOutboundIntentTx(ctx, tx, OutboundIntent{CustomerID: delivery.CustomerID, UserID: userID, SessionID: sessionID, RunID: &delivery.RunID, Type: "message", Payload: payloadJSON})
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE shared_scheduler_deliveries SET outbound_intent_id=$2, status='queued', updated_at=now() WHERE id=$1`, rowID, intentID)
		created = err == nil
		return err
	})
	return created, err
}

func (s *Store) CompleteSharedSchedulerRunDelivery(ctx context.Context, deliveryID string, status string, errText string) error {
	if status == "" {
		status = "completed"
	}
	_, err := s.pool.Exec(ctx, `UPDATE shared_scheduler_deliveries SET status=$2, updated_at=now() WHERE id=$1`, deliveryID, status)
	return err
}
