package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type BroadcastTargetSpec struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

type Broadcast struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	Title      string          `json:"title"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type BroadcastTarget struct {
	ID               string    `json:"id"`
	BroadcastID      string    `json:"broadcast_id"`
	CustomerID       string    `json:"customer_id"`
	UserID           string    `json:"user_id"`
	SessionID        string    `json:"session_id"`
	Status           string    `json:"status"`
	OutboundIntentID *string   `json:"outbound_intent_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s *Store) CreateBroadcast(ctx context.Context, customerID, title string, payload any, targets []BroadcastTargetSpec) (string, int, error) {
	if customerID == "" || title == "" || len(targets) == 0 {
		return "", 0, fmt.Errorf("customer_id, title, and targets are required")
	}
	if err := s.ensureCustomer(ctx, customerID); err != nil {
		return "", 0, err
	}
	for _, target := range targets {
		if target.UserID == "" || target.SessionID == "" {
			return "", 0, fmt.Errorf("broadcast targets require user_id and session_id")
		}
	}
	payloadJSON, _ := json.Marshal(payload)
	if len(payloadJSON) == 0 {
		payloadJSON = []byte(`{}`)
	}
	var broadcastID string
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO broadcasts(customer_id,title,payload,status)
		VALUES($1,$2,$3,'queued')
		RETURNING id::text`, customerID, title, payloadJSON).Scan(&broadcastID); err != nil {
		return "", 0, err
	}
	created := 0
	for _, target := range targets {
		var targetID string
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO broadcast_targets(broadcast_id,customer_id,user_id,session_id,status)
			VALUES($1,$2,$3,$4,'pending')
			RETURNING id::text`, broadcastID, customerID, target.UserID, target.SessionID).Scan(&targetID); err != nil {
			return broadcastID, created, err
		}
		intentID, _, err := s.CreateOutboundIntent(ctx, OutboundIntent{
			CustomerID: customerID,
			UserID:     target.UserID,
			SessionID:  target.SessionID,
			Type:       "broadcast",
			Payload:    mustJSON(map[string]any{"broadcast_id": broadcastID, "target_id": targetID, "title": title, "payload": json.RawMessage(payloadJSON)}),
		})
		if err != nil {
			return broadcastID, created, err
		}
		if _, err := s.pool.Exec(ctx, `UPDATE broadcast_targets SET status='queued', outbound_intent_id=$2, updated_at=now() WHERE id=$1`, targetID, intentID); err != nil {
			return broadcastID, created, err
		}
		created++
	}
	return broadcastID, created, nil
}

func (s *Store) ListBroadcasts(ctx context.Context, customerID string, limit int) ([]Broadcast, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, title, payload, status, created_at, updated_at
		FROM broadcasts
		WHERE customer_id=$1
		ORDER BY created_at DESC
		LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var broadcasts []Broadcast
	for rows.Next() {
		var broadcast Broadcast
		if err := rows.Scan(&broadcast.ID, &broadcast.CustomerID, &broadcast.Title, &broadcast.Payload, &broadcast.Status, &broadcast.CreatedAt, &broadcast.UpdatedAt); err != nil {
			return nil, err
		}
		broadcasts = append(broadcasts, broadcast)
	}
	return broadcasts, rows.Err()
}

func (s *Store) ListBroadcastTargets(ctx context.Context, customerID, broadcastID string, limit int) ([]BroadcastTarget, error) {
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, broadcast_id::text, customer_id, user_id, session_id, status, outbound_intent_id::text, created_at, updated_at
		FROM broadcast_targets
		WHERE customer_id=$1 AND broadcast_id=$2
		ORDER BY created_at ASC
		LIMIT $3`, customerID, broadcastID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []BroadcastTarget
	for rows.Next() {
		var target BroadcastTarget
		if err := rows.Scan(&target.ID, &target.BroadcastID, &target.CustomerID, &target.UserID, &target.SessionID, &target.Status, &target.OutboundIntentID, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *Store) CancelBroadcast(ctx context.Context, customerID, broadcastID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE broadcasts
		SET status='cancelled', updated_at=now()
		WHERE id=$1 AND customer_id=$2`, broadcastID, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("broadcast not found")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE broadcast_targets
		SET status='cancelled', updated_at=now()
		WHERE broadcast_id=$1 AND customer_id=$2 AND status IN ('pending','queued')`, broadcastID, customerID); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE outbound_intents
		SET status='cancelled', updated_at=now()
		WHERE id IN (
			SELECT outbound_intent_id FROM broadcast_targets
			WHERE broadcast_id=$1 AND customer_id=$2 AND outbound_intent_id IS NOT NULL
		)
		AND status IN ('pending','queued')`, broadcastID, customerID)
	return err
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}
