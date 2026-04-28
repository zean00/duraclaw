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
