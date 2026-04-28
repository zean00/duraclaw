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
	var id string
	var outboxID int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, outboxID, err = createOutboundIntentTx(ctx, tx, intent)
		return err
	})
	return id, outboxID, err
}

func createOutboundIntentTx(ctx context.Context, tx pgx.Tx, intent OutboundIntent) (string, int64, error) {
	payload := []byte(intent.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	var runID any
	if intent.RunID != nil && *intent.RunID != "" {
		runID = *intent.RunID
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_intents(customer_id,user_id,session_id,run_id,intent_type,payload,status)
		VALUES($1,$2,$3,$4,$5,$6,'pending')
		RETURNING id::text`, intent.CustomerID, intent.UserID, intent.SessionID, runID, intent.Type, payload).Scan(&id); err != nil {
		return "", 0, err
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
	var outboxID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO async_outbox(topic,payload)
		VALUES('nexus.outbound_intent',$1)
		RETURNING id`, outboxJSON).Scan(&outboxID); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_intents SET status='queued', outbox_id=$2, updated_at=now() WHERE id=$1`, id, outboxID); err != nil {
		return "", 0, err
	}
	return id, outboxID, nil
}

func NormalizeOutboundIntentStatus(status string) (string, error) {
	switch status {
	case "sent":
		return "sent_to_nexus", nil
	case "sent_to_nexus", "delivered", "failed", "cancelled":
		return status, nil
	default:
		return "", fmt.Errorf("invalid outbound intent status %q", status)
	}
}

func (s *Store) SetOutboundIntentStatus(ctx context.Context, id, customerID, status string) error {
	status, err := NormalizeOutboundIntentStatus(status)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var runID *string
		err := tx.QueryRow(ctx, `
			UPDATE outbound_intents
			SET status=$3, updated_at=now()
			WHERE id=$1 AND customer_id=$2
			RETURNING run_id::text`, id, customerID, status).Scan(&runID)
		if err == pgx.ErrNoRows {
			return fmt.Errorf("outbound intent not found")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE broadcast_targets
			SET status=$2, updated_at=now()
			WHERE outbound_intent_id=$1`, id, status); err != nil {
			return err
		}
		switch status {
		case "delivered":
			if _, err := tx.Exec(ctx, `
				UPDATE broadcasts b
				SET status='delivered', updated_at=now()
				WHERE b.id IN (
					SELECT broadcast_id FROM broadcast_targets WHERE outbound_intent_id=$1
				)
				AND NOT EXISTS (
					SELECT 1 FROM broadcast_targets t
					WHERE t.broadcast_id=b.id
					AND t.status NOT IN ('delivered','cancelled')
				)`, id); err != nil {
				return err
			}
		case "sent_to_nexus":
			if _, err := tx.Exec(ctx, `
				UPDATE broadcasts b
				SET status='sent_to_nexus', updated_at=now()
				WHERE b.id IN (
					SELECT broadcast_id FROM broadcast_targets WHERE outbound_intent_id=$1
				)
				AND b.status NOT IN ('delivered','failed','cancelled')
				AND NOT EXISTS (
					SELECT 1 FROM broadcast_targets t
					WHERE t.broadcast_id=b.id
					AND t.status NOT IN ('sent_to_nexus','delivered','cancelled')
				)`, id); err != nil {
				return err
			}
		case "failed":
			if _, err := tx.Exec(ctx, `
				UPDATE broadcasts b
				SET status='failed', updated_at=now()
				WHERE b.id IN (
					SELECT broadcast_id FROM broadcast_targets WHERE outbound_intent_id=$1
				)
				AND b.status NOT IN ('delivered','cancelled')`, id); err != nil {
				return err
			}
		}
		payload := mustJSON(map[string]any{"outbound_intent_id": id, "status": status})
		_, err = tx.Exec(ctx, `
			INSERT INTO observability_events(customer_id,run_id,event_type,payload)
			VALUES($1,$2,$3,$4)`, customerID, runID, "outbound."+status, payload)
		return err
	})
}

func (s *Store) ListOutboundIntents(ctx context.Context, customerID, status string, limit int) ([]OutboundIntent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	statusFilter := ""
	if status != "" {
		if status == "sent" {
			status = "sent_to_nexus"
		}
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
