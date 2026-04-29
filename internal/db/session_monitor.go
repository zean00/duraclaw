package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type MonitoredSession struct {
	CustomerID      string          `json:"customer_id"`
	UserID          string          `json:"user_id"`
	AgentInstanceID string          `json:"agent_instance_id"`
	SessionID       string          `json:"session_id"`
	Metadata        json.RawMessage `json:"metadata"`
	ActivePattern   json.RawMessage `json:"active_pattern"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastMonitoredAt *time.Time      `json:"last_monitored_at,omitempty"`
	LastMessageAt   *time.Time      `json:"last_message_at,omitempty"`
}

func (s *Store) ClaimIdleSessions(ctx context.Context, owner string, idleFor, leaseFor time.Duration, limit int) ([]MonitoredSession, error) {
	if owner == "" {
		owner = "duraclaw-session-monitor"
	}
	if idleFor <= 0 {
		idleFor = 30 * time.Minute
	}
	if leaseFor <= 0 {
		leaseFor = 5 * time.Minute
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT s.customer_id, s.id
			FROM sessions s
			LEFT JOIN LATERAL (
				SELECT max(created_at) AS last_message_at
				FROM messages m
				WHERE m.customer_id=s.customer_id AND m.session_id=s.id
			) lm ON true
			WHERE COALESCE(lm.last_message_at, s.updated_at) <= now() - $1::interval
			AND (s.last_monitored_at IS NULL OR s.last_monitored_at < COALESCE(lm.last_message_at, s.updated_at))
			AND (s.monitor_lease_expires_at IS NULL OR s.monitor_lease_expires_at < now())
			ORDER BY COALESCE(lm.last_message_at, s.updated_at) ASC
			LIMIT $2
			FOR UPDATE OF s SKIP LOCKED
		)
		UPDATE sessions s
		SET monitor_lease_owner=$3, monitor_lease_expires_at=now()+$4::interval
		FROM candidates c
		WHERE s.customer_id=c.customer_id AND s.id=c.id
		RETURNING s.customer_id, s.user_id, s.agent_instance_id, s.id, s.metadata, s.active_pattern, s.updated_at, s.last_monitored_at,
			(SELECT max(created_at) FROM messages m WHERE m.customer_id=s.customer_id AND m.session_id=s.id)`,
		pgInterval(idleFor), limit, owner, pgInterval(leaseFor))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonitoredSession
	for rows.Next() {
		var session MonitoredSession
		if err := rows.Scan(&session.CustomerID, &session.UserID, &session.AgentInstanceID, &session.SessionID, &session.Metadata, &session.ActivePattern, &session.UpdatedAt, &session.LastMonitoredAt, &session.LastMessageAt); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *Store) CompleteSessionMonitor(ctx context.Context, customerID, sessionID string, monitoredThrough time.Time, activePattern any) error {
	if monitoredThrough.IsZero() {
		monitoredThrough = time.Now().UTC()
	}
	b, _ := json.Marshal(activePattern)
	if len(b) == 0 || string(b) == "null" {
		b = []byte(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET active_pattern=$4, last_monitored_at=$3, monitor_lease_owner=NULL, monitor_lease_expires_at=NULL, updated_at=updated_at
		WHERE customer_id=$1 AND id=$2`, customerID, sessionID, monitoredThrough, b)
	return err
}

func (s *Store) ReleaseSessionMonitor(ctx context.Context, customerID, sessionID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET monitor_lease_owner=NULL, monitor_lease_expires_at=NULL
		WHERE customer_id=$1 AND id=$2`, customerID, sessionID)
	return err
}

func (s *Store) RecentUserMessages(ctx context.Context, customerID, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, role, content, created_at
		FROM (
			SELECT id, role, content, created_at
			FROM messages
			WHERE customer_id=$1 AND session_id=$2 AND role='user'
			ORDER BY created_at DESC
			LIMIT $3
		) recent
		ORDER BY created_at ASC`, customerID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func pgInterval(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	return fmt.Sprintf("%f seconds", d.Seconds())
}
