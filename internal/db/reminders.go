package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ReminderSubscription struct {
	ID                    string          `json:"id"`
	CustomerID            string          `json:"customer_id"`
	UserID                string          `json:"user_id"`
	SessionID             string          `json:"session_id"`
	AgentInstanceID       string          `json:"agent_instance_id"`
	ChannelType           string          `json:"channel_type,omitempty"`
	Title                 string          `json:"title"`
	Schedule              string          `json:"schedule"`
	Timezone              string          `json:"timezone"`
	Payload               json.RawMessage `json:"payload"`
	Enabled               bool            `json:"enabled"`
	NextRunAt             time.Time       `json:"next_run_at"`
	LastFiredAt           *time.Time      `json:"last_fired_at,omitempty"`
	RepeatIntervalSeconds int             `json:"repeat_interval_seconds,omitempty"`
	RepeatUntil           *time.Time      `json:"repeat_until,omitempty"`
	RepeatCount           int             `json:"repeat_count,omitempty"`
	FiredCount            int             `json:"fired_count,omitempty"`
	Metadata              json.RawMessage `json:"metadata"`
	LeaseOwner            *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt        *time.Time      `json:"lease_expires_at,omitempty"`
}

type ReminderSubscriptionSpec struct {
	CustomerID            string
	UserID                string
	SessionID             string
	AgentInstanceID       string
	ChannelType           string
	Title                 string
	Schedule              string
	Timezone              string
	Payload               any
	NextRunAt             time.Time
	RepeatIntervalSeconds int
	RepeatUntil           *time.Time
	RepeatCount           int
	Metadata              any
}

type ReminderSubscriptionUpdate struct {
	Title                 *string
	Schedule              *string
	Timezone              *string
	ChannelType           *string
	Payload               any
	NextRunAt             *time.Time
	RepeatIntervalSeconds *int
	RepeatUntil           *time.Time
	RepeatUntilSet        bool
	RepeatCount           *int
	FiredCount            *int
	Metadata              any
	Enabled               *bool
}

func (s *Store) CreateReminderSubscription(ctx context.Context, spec ReminderSubscriptionSpec) (*ReminderSubscription, error) {
	if spec.CustomerID == "" || spec.UserID == "" || spec.SessionID == "" || spec.AgentInstanceID == "" || spec.Schedule == "" || spec.NextRunAt.IsZero() {
		return nil, fmt.Errorf("customer_id, user_id, session_id, agent_instance_id, schedule, and next_run_at are required")
	}
	if spec.RepeatIntervalSeconds < 0 || spec.RepeatCount < 0 {
		return nil, fmt.Errorf("repeat_interval_seconds and repeat_count must be non-negative")
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
	spec.ChannelType = strings.ToLower(strings.TrimSpace(spec.ChannelType))
	var sub ReminderSubscription
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reminder_subscriptions(customer_id,user_id,session_id,agent_instance_id,channel_type,title,schedule,timezone,payload,next_run_at,repeat_interval_seconds,repeat_until,repeat_count,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id::text, customer_id, user_id, session_id, agent_instance_id, coalesce(channel_type,''), title, schedule, timezone, payload, enabled, next_run_at, last_fired_at, repeat_interval_seconds, repeat_until, repeat_count, fired_count, metadata`,
		spec.CustomerID, spec.UserID, spec.SessionID, spec.AgentInstanceID, nullableString(emptyStringNil(spec.ChannelType)), spec.Title, spec.Schedule, spec.Timezone, payload, spec.NextRunAt, spec.RepeatIntervalSeconds, spec.RepeatUntil, spec.RepeatCount, metadata).
		Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.ChannelType, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.RepeatIntervalSeconds, &sub.RepeatUntil, &sub.RepeatCount, &sub.FiredCount, &sub.Metadata)
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
		SELECT id::text, customer_id, user_id, session_id, agent_instance_id, coalesce(channel_type,''), title, schedule, timezone, payload, enabled, next_run_at, last_fired_at, repeat_interval_seconds, repeat_until, repeat_count, fired_count, metadata
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
		if err := rows.Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.ChannelType, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.RepeatIntervalSeconds, &sub.RepeatUntil, &sub.RepeatCount, &sub.FiredCount, &sub.Metadata); err != nil {
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
		RETURNING r.id::text, r.customer_id, r.user_id, r.session_id, r.agent_instance_id, coalesce(r.channel_type,''), r.title, r.schedule, r.timezone, r.payload, r.enabled, r.next_run_at, r.last_fired_at, r.repeat_interval_seconds, r.repeat_until, r.repeat_count, r.fired_count, r.metadata, r.lease_owner, r.lease_expires_at`,
		limit, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReminderSubscription
	for rows.Next() {
		var sub ReminderSubscription
		if err := rows.Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.ChannelType, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.RepeatIntervalSeconds, &sub.RepeatUntil, &sub.RepeatCount, &sub.FiredCount, &sub.Metadata, &sub.LeaseOwner, &sub.LeaseExpiresAt); err != nil {
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
			SET last_fired_at=$2, fired_count=fired_count+1, enabled=false, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
			WHERE id=$1`, id, firedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE reminder_subscriptions
		SET last_fired_at=$2, fired_count=fired_count+1, next_run_at=$3, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
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

func (s *Store) UpdateUserReminderSubscription(ctx context.Context, id, customerID, userID string, update ReminderSubscriptionUpdate) (*ReminderSubscription, error) {
	if id == "" || customerID == "" || userID == "" {
		return nil, fmt.Errorf("subscription_id, customer_id, and user_id are required")
	}
	if update.RepeatUntil != nil {
		update.RepeatUntilSet = true
	}
	if update.RepeatIntervalSeconds != nil && *update.RepeatIntervalSeconds < 0 {
		return nil, fmt.Errorf("repeat_interval_seconds must be non-negative")
	}
	if update.RepeatCount != nil && *update.RepeatCount < 0 {
		return nil, fmt.Errorf("repeat_count must be non-negative")
	}
	if update.FiredCount != nil && *update.FiredCount < 0 {
		return nil, fmt.Errorf("fired_count must be non-negative")
	}
	if update.Title == nil && update.Schedule == nil && update.Timezone == nil && update.ChannelType == nil && update.Payload == nil && update.NextRunAt == nil && update.RepeatIntervalSeconds == nil && !update.RepeatUntilSet && update.RepeatCount == nil && update.FiredCount == nil && update.Metadata == nil && update.Enabled == nil {
		return nil, fmt.Errorf("at least one reminder field is required")
	}
	if err := s.validateReminderUpdateBounds(ctx, id, customerID, userID, update); err != nil {
		return nil, err
	}
	var payload []byte
	if update.Payload != nil {
		payload, _ = json.Marshal(update.Payload)
		if len(payload) == 0 || string(payload) == "null" {
			payload = []byte(`{}`)
		}
	}
	var metadata []byte
	if update.Metadata != nil {
		metadata, _ = json.Marshal(update.Metadata)
		if len(metadata) == 0 || string(metadata) == "null" {
			metadata = []byte(`{}`)
		}
	}
	title := nullableString(update.Title)
	schedule := nullableString(update.Schedule)
	timezone := nullableString(update.Timezone)
	channelTypeSet := update.ChannelType != nil
	channelType := nullableString(normalizeOptionalChannelType(update.ChannelType))
	nextRunAt := nullableTime(update.NextRunAt)
	repeatIntervalSeconds := nullableInt(update.RepeatIntervalSeconds)
	repeatUntil := nullableTime(update.RepeatUntil)
	repeatCount := nullableInt(update.RepeatCount)
	firedCount := nullableInt(update.FiredCount)
	enabled := nullableBool(update.Enabled)
	var sub ReminderSubscription
	err := s.pool.QueryRow(ctx, `
		UPDATE reminder_subscriptions
		SET title=COALESCE($4::text, title),
			schedule=COALESCE($5::text, schedule),
			timezone=COALESCE($6::text, timezone),
			channel_type=CASE WHEN $7::boolean THEN $8::text ELSE channel_type END,
			payload=COALESCE($9::jsonb, payload),
			next_run_at=COALESCE($10::timestamptz, next_run_at),
			repeat_interval_seconds=COALESCE($11::integer, repeat_interval_seconds),
			repeat_until=CASE WHEN $17::boolean THEN $12::timestamptz ELSE repeat_until END,
			repeat_count=COALESCE($13::integer, repeat_count),
			fired_count=COALESCE($14::integer, fired_count),
			metadata=COALESCE($15::jsonb, metadata),
			enabled=COALESCE($16::boolean, enabled),
			lease_owner=NULL,
			lease_expires_at=NULL,
			updated_at=now()
		WHERE id=$1 AND customer_id=$2 AND user_id=$3
		RETURNING id::text, customer_id, user_id, session_id, agent_instance_id, coalesce(channel_type,''), title, schedule, timezone, payload, enabled, next_run_at, last_fired_at, repeat_interval_seconds, repeat_until, repeat_count, fired_count, metadata`,
		id, customerID, userID, title, schedule, timezone, channelTypeSet, channelType, nullableBytes(payload), nextRunAt, repeatIntervalSeconds, repeatUntil, repeatCount, firedCount, nullableBytes(metadata), enabled, update.RepeatUntilSet).
		Scan(&sub.ID, &sub.CustomerID, &sub.UserID, &sub.SessionID, &sub.AgentInstanceID, &sub.ChannelType, &sub.Title, &sub.Schedule, &sub.Timezone, &sub.Payload, &sub.Enabled, &sub.NextRunAt, &sub.LastFiredAt, &sub.RepeatIntervalSeconds, &sub.RepeatUntil, &sub.RepeatCount, &sub.FiredCount, &sub.Metadata)
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Store) validateReminderUpdateBounds(ctx context.Context, id, customerID, userID string, update ReminderSubscriptionUpdate) error {
	if update.Schedule == nil && update.NextRunAt == nil && update.RepeatIntervalSeconds == nil && !update.RepeatUntilSet {
		return nil
	}
	var current ReminderSubscription
	err := s.pool.QueryRow(ctx, `
		SELECT schedule, next_run_at, repeat_interval_seconds, repeat_until
		FROM reminder_subscriptions
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`, id, customerID, userID).
		Scan(&current.Schedule, &current.NextRunAt, &current.RepeatIntervalSeconds, &current.RepeatUntil)
	if err != nil {
		return err
	}
	schedule := current.Schedule
	if update.Schedule != nil {
		schedule = *update.Schedule
	}
	repeatIntervalSeconds := current.RepeatIntervalSeconds
	if update.RepeatIntervalSeconds != nil {
		repeatIntervalSeconds = *update.RepeatIntervalSeconds
	}
	if schedule == "@interval" && repeatIntervalSeconds <= 0 {
		return fmt.Errorf("repeat_interval_seconds must be positive for @interval reminders")
	}
	nextRunAt := current.NextRunAt
	if update.NextRunAt != nil {
		nextRunAt = *update.NextRunAt
	}
	repeatUntil := current.RepeatUntil
	if update.RepeatUntilSet {
		repeatUntil = update.RepeatUntil
	}
	if repeatUntil != nil && !nextRunAt.IsZero() && !repeatUntil.After(nextRunAt) {
		return fmt.Errorf("repeat_until must be after next_run_at")
	}
	return nil
}

func (s *Store) DeleteUserReminderSubscription(ctx context.Context, id, customerID, userID string) error {
	if id == "" || customerID == "" || userID == "" {
		return fmt.Errorf("subscription_id, customer_id, and user_id are required")
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM reminder_subscriptions
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`, id, customerID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reminder subscription not found")
	}
	return nil
}

func nullableBytes(b []byte) any {
	if b == nil {
		return nil
	}
	return b
}

func nullableString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func emptyStringNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func normalizeOptionalChannelType(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(*value))
	if normalized == "" {
		return nil
	}
	return &normalized
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func nullableInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func nullableBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
