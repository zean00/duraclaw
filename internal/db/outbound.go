package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type OutboundIntent struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	UserID     string          `json:"user_id"`
	SessionID  string          `json:"session_id"`
	RunID      *string         `json:"run_id,omitempty"`
	Type       string          `json:"intent_type"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	OutboxID   *int64          `json:"outbox_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func (s *Store) CreateOutboundIntent(ctx context.Context, intent OutboundIntent) (string, int64, error) {
	payload := []byte(intent.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	var id string
	var outboxID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var runID any
		if intent.RunID != nil && *intent.RunID != "" {
			runID = *intent.RunID
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO outbound_intents(customer_id,user_id,session_id,run_id,intent_type,payload,status)
			VALUES($1,$2,$3,$4,$5,$6,'pending')
			RETURNING id::text`, intent.CustomerID, intent.UserID, intent.SessionID, runID, intent.Type, payload).Scan(&id); err != nil {
			return err
		}
		outboxPayload := map[string]any{
			"outbound_intent_id": id,
			"customer_id":        intent.CustomerID,
			"user_id":            intent.UserID,
			"session_id":         intent.SessionID,
			"run_id":             runID,
			"intent_type":        intent.Type,
			"payload":            json.RawMessage(payload),
		}
		outboxJSON, _ := json.Marshal(outboxPayload)
		if err := tx.QueryRow(ctx, `
			INSERT INTO async_outbox(topic,payload)
			VALUES('nexus.outbound_intent',$1)
			RETURNING id`, outboxJSON).Scan(&outboxID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE outbound_intents SET status='queued', outbox_id=$2, updated_at=now() WHERE id=$1`, id, outboxID)
		return err
	})
	return id, outboxID, err
}

func (s *Store) SetOutboundIntentStatus(ctx context.Context, id, customerID, status string) error {
	switch status {
	case "sent", "failed", "cancelled":
	default:
		return fmt.Errorf("invalid outbound intent status %q", status)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE outbound_intents SET status=$3, updated_at=now() WHERE id=$1 AND customer_id=$2`, id, customerID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("outbound intent not found")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE broadcast_targets
		SET status=$2, updated_at=now()
		WHERE outbound_intent_id=$1`, id, status); err != nil {
		return err
	}
	if status == "sent" {
		_, err = s.pool.Exec(ctx, `
			UPDATE broadcasts b
			SET status='sent', updated_at=now()
			WHERE b.id IN (
				SELECT broadcast_id FROM broadcast_targets WHERE outbound_intent_id=$1
			)
			AND NOT EXISTS (
				SELECT 1 FROM broadcast_targets t
				WHERE t.broadcast_id=b.id
				AND t.status NOT IN ('sent','cancelled')
			)`, id)
		return err
	}
	return nil
}

func (s *Store) ListOutboundIntents(ctx context.Context, customerID, status string, limit int) ([]OutboundIntent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	statusFilter := ""
	if status != "" {
		args = append(args, status)
		statusFilter = " AND status=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, session_id, run_id::text, intent_type, payload, status, outbox_id, created_at, updated_at
		FROM outbound_intents
		WHERE customer_id=$1`+statusFilter+`
		ORDER BY created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []OutboundIntent
	for rows.Next() {
		var intent OutboundIntent
		if err := rows.Scan(&intent.ID, &intent.CustomerID, &intent.UserID, &intent.SessionID, &intent.RunID, &intent.Type, &intent.Payload, &intent.Status, &intent.OutboxID, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}
