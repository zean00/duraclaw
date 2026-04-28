package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SessionTransfer struct {
	ID                  string          `json:"id"`
	CustomerID          string          `json:"customer_id"`
	SessionID           string          `json:"session_id"`
	FromAgentInstanceID string          `json:"from_agent_instance_id"`
	ToAgentInstanceID   string          `json:"to_agent_instance_id"`
	Reason              string          `json:"reason"`
	Metadata            json.RawMessage `json:"metadata"`
	CreatedAt           time.Time       `json:"created_at"`
}

func (s *Store) ReassignSession(ctx context.Context, customerID, sessionID, toAgentInstanceID, reason string, metadata any) (*SessionTransfer, error) {
	if customerID == "" || sessionID == "" || toAgentInstanceID == "" {
		return nil, fmt.Errorf("customer_id, session_id, and to_agent_instance_id are required")
	}
	var transfer SessionTransfer
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `
			SELECT agent_instance_id
			FROM sessions
			WHERE customer_id=$1 AND id=$2
			FOR UPDATE`, customerID, sessionID).Scan(&from); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, customerID, toAgentInstanceID); err != nil {
			return err
		}
		if err := ensureDefaultAgentInstanceVersion(ctx, tx, customerID, toAgentInstanceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET agent_instance_id=$3, updated_at=now() WHERE customer_id=$1 AND id=$2`, customerID, sessionID, toAgentInstanceID); err != nil {
			return err
		}
		b, _ := json.Marshal(metadata)
		if len(b) == 0 || string(b) == "null" {
			b = []byte(`{}`)
		}
		return tx.QueryRow(ctx, `
			INSERT INTO session_agent_instance_transfers(customer_id,session_id,from_agent_instance_id,to_agent_instance_id,reason,metadata)
			VALUES($1,$2,$3,$4,$5,$6)
			RETURNING id::text, customer_id, session_id, from_agent_instance_id, to_agent_instance_id, reason, metadata, created_at`,
			customerID, sessionID, from, toAgentInstanceID, reason, b).
			Scan(&transfer.ID, &transfer.CustomerID, &transfer.SessionID, &transfer.FromAgentInstanceID, &transfer.ToAgentInstanceID, &transfer.Reason, &transfer.Metadata, &transfer.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) sessionAgentInstanceID(ctx context.Context, customerID, sessionID string) (string, error) {
	var agentInstanceID string
	err := s.pool.QueryRow(ctx, `
		SELECT agent_instance_id
		FROM sessions
		WHERE customer_id=$1 AND id=$2`, customerID, sessionID).Scan(&agentInstanceID)
	return agentInstanceID, err
}

func (s *Store) LatestSessionTransfer(ctx context.Context, customerID, sessionID string) (*SessionTransfer, error) {
	var transfer SessionTransfer
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, session_id, from_agent_instance_id, to_agent_instance_id, reason, metadata, created_at
		FROM session_agent_instance_transfers
		WHERE customer_id=$1 AND session_id=$2
		ORDER BY created_at DESC
		LIMIT 1`, customerID, sessionID).
		Scan(&transfer.ID, &transfer.CustomerID, &transfer.SessionID, &transfer.FromAgentInstanceID, &transfer.ToAgentInstanceID, &transfer.Reason, &transfer.Metadata, &transfer.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &transfer, nil
}
