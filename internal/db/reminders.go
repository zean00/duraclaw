package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ReminderSubscription struct {
	ID              string          `json:"id"`
	CustomerID      string          `json:"customer_id"`
	UserID          string          `json:"user_id"`
	SessionID       string          `json:"session_id"`
	AgentInstanceID string          `json:"agent_instance_id"`
	Title           string          `json:"title"`
	Schedule        string          `json:"schedule"`
	Timezone        string          `json:"timezone"`
	Payload         json.RawMessage `json:"payload"`
	Enabled         bool            `json:"enabled"`
	NextRunAt       time.Time       `json:"next_run_at"`
	LastFiredAt     *time.Time      `json:"last_fired_at,omitempty"`
	Metadata        json.RawMessage `json:"metadata"`
	LeaseOwner      *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty"`
}

type ReminderSubscriptionSpec struct {
	CustomerID      string
	UserID          string
	SessionID       string
	AgentInstanceID string
	Title           string
	Schedule        string
	Timezone        string
	Payload         any
	NextRunAt       time.Time
	Metadata        any
}

func (s *Store) CreateReminderSubscription(ctx context.Context, spec ReminderSubscriptionSpec) (*ReminderSubscription, error) {
	if spec.CustomerID == "" || spec.UserID == "" || spec.SessionID == "" || spec.AgentInstanceID == "" || spec.Schedule == "" || spec.NextRunAt.IsZero() {
		return nil, fmt.Errorf("customer_id, user_id, session_id, agent_instance_id, schedule, and next_run_at are required")
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	if err := s.EnsureSession(ctx, ACPContext{CustomerID: spec.CustomerID, UserID: spec.UserID, AgentInstanceID: spec.AgentInstanceID, SessionID: spec.SessionID}); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(spec.Payload)
	if len(payload) == 0 || string(payload) == "null" {
		payload = []byte(`{}`)
	}
	metadata, _ := json.Marshal(spec.Metadata)
	if len(metadata) == 0 || string(metadata) == "null" {
		metadata = []byte(`{}`)
	}
	var sub ReminderSubscription
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reminder_subscriptions(customer_id,user_id,session_id,agent_instance_id,title,schedule,timezone,payload,next_run_at,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id::text, customer_id, user_id, session_id, agent_instance_id, title, schedule, timezone, payload, enabled, next_run_at, last_fired_at, metadata`,
		spec.CustomerID, spec.UserID, spec.SessionID, spec.AgentInstanceID, spec.Title, spec.Schedule, spec.Timezone, payload, spec.NextRunAt, metadata).
		Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.Metadata)
	return &sub, err
}

func (s *Store) ListReminderSubscriptions(ctx context.Context, customerID, userID string, limit int) ([]ReminderSubscription, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	filter := ""
	if userID != "" {
		args = append(args, userID)
		filter = " AND user_id=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, session_id, agent_instance_id, title, schedule, timezone, payload, enabled, next_run_at, last_fired_at, metadata
		FROM reminder_subscriptions
		WHERE customer_id=$1`+filter+`
		ORDER BY next_run_at ASC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderSubscription
	for rows.Next() {
		var sub ReminderSubscription
		if err := rows.Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.Metadata); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) ClaimDueReminderSubscriptions(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]ReminderSubscription, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT id
			FROM reminder_subscriptions
			WHERE enabled=true
			AND next_run_at <= now()
			AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE reminder_subscriptions r
		SET lease_owner=$2, lease_expires_at=now()+$3::interval
		FROM candidate
		WHERE r.id=candidate.id
		RETURNING r.id::text, r.customer_id, r.user_id, r.session_id, r.agent_instance_id, r.title, r.schedule, r.timezone, r.payload, r.enabled, r.next_run_at, r.last_fired_at, r.metadata, r.lease_owner, r.lease_expires_at`,
		limit, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderSubscription
	for rows.Next() {
		var sub ReminderSubscription
		if err := rows.Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.Metadata, &sub.LeaseOwner, &sub.LeaseExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) CompleteReminderSubscription(ctx context.Context, id string, firedAt, nextRunAt time.Time) error {
	if nextRunAt.IsZero() {
		_, err := s.pool.Exec(ctx, `
			UPDATE reminder_subscriptions
			SET last_fired_at=$2, enabled=false, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
			WHERE id=$1`, id, firedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE reminder_subscriptions
		SET last_fired_at=$2, next_run_at=$3, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1`, id, firedAt, nextRunAt)
	return err
}

func (s *Store) SetReminderSubscriptionEnabled(ctx context.Context, id, customerID string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE reminder_subscriptions
		SET enabled=$3, updated_at=now()
		WHERE id=$1 AND customer_id=$2`, id, customerID, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reminder subscription not found")
	}
	return nil
}
