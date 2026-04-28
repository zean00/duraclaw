package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type BroadcastTargetSpec struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

type BroadcastTargetSelection struct {
	AllUsers               bool           `json:"all_users,omitempty"`
	UserIDs                []string       `json:"user_ids,omitempty"`
	AgentInstanceID        string         `json:"agent_instance_id,omitempty"`
	ReminderSubscriptionID string         `json:"reminder_subscription_id,omitempty"`
	Segment                map[string]any `json:"segment,omitempty"`
	Limit                  int            `json:"limit,omitempty"`
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
	created := 0
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO broadcasts(customer_id,title,payload,status)
			VALUES($1,$2,$3,'queued')
			RETURNING id::text`, customerID, title, payloadJSON).Scan(&broadcastID); err != nil {
			return err
		}
		for _, target := range targets {
			var targetID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO broadcast_targets(broadcast_id,customer_id,user_id,session_id,status)
				VALUES($1,$2,$3,$4,'pending')
				RETURNING id::text`, broadcastID, customerID, target.UserID, target.SessionID).Scan(&targetID); err != nil {
				return err
			}
			intentID, _, err := createOutboundIntentTx(ctx, tx, OutboundIntent{
				CustomerID: customerID,
				UserID:     target.UserID,
				SessionID:  target.SessionID,
				Type:       "broadcast",
				Payload:    mustJSON(map[string]any{"broadcast_id": broadcastID, "target_id": targetID, "title": title, "payload": json.RawMessage(payloadJSON)}),
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE broadcast_targets SET status='queued', outbound_intent_id=$2, updated_at=now() WHERE id=$1`, targetID, intentID); err != nil {
				return err
			}
			created++
		}
		return nil
	})
	return broadcastID, created, err
}

func (s *Store) ResolveBroadcastTargets(ctx context.Context, customerID string, selection BroadcastTargetSelection) ([]BroadcastTargetSpec, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	limit := selection.Limit
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	seen := map[string]bool{}
	var out []BroadcastTargetSpec
	addRows := func(rows pgx.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var target BroadcastTargetSpec
			if err := rows.Scan(&target.UserID, &target.SessionID); err != nil {
				return err
			}
			key := target.UserID + "\x00" + target.SessionID
			if target.UserID == "" || target.SessionID == "" || seen[key] {
				continue
			}
			out = append(out, target)
			seen[key] = true
		}
		return rows.Err()
	}
	if selection.AllUsers {
		rows, err := s.pool.Query(ctx, `
			SELECT DISTINCT ON (user_id) user_id, id
			FROM sessions
			WHERE customer_id=$1
			ORDER BY user_id, updated_at DESC
			LIMIT $2`, customerID, limit)
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	if selection.AgentInstanceID != "" && len(out) < limit {
		rows, err := s.pool.Query(ctx, `
			SELECT DISTINCT ON (user_id) user_id, id
			FROM sessions
			WHERE customer_id=$1 AND agent_instance_id=$2
			ORDER BY user_id, updated_at DESC
			LIMIT $3`, customerID, selection.AgentInstanceID, limit-len(out))
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	if selection.ReminderSubscriptionID != "" && len(out) < limit {
		rows, err := s.pool.Query(ctx, `
			SELECT user_id, session_id
			FROM reminder_subscriptions
			WHERE customer_id=$1 AND id=$2 AND enabled=true
			LIMIT $3`, customerID, selection.ReminderSubscriptionID, limit-len(out))
		if err != nil {
			return nil, err
		}
		if err := addRows(rows); err != nil {
			return nil, err
		}
	}
	for _, userID := range selection.UserIDs {
		if len(out) >= limit {
			break
		}
		var target BroadcastTargetSpec
		err := s.pool.QueryRow(ctx, `
			SELECT user_id, id
			FROM sessions
			WHERE customer_id=$1 AND user_id=$2
			ORDER BY updated_at DESC
			LIMIT 1`, customerID, userID).Scan(&target.UserID, &target.SessionID)
		if err != nil {
			continue
		}
		key := target.UserID + "\x00" + target.SessionID
		if !seen[key] {
			out = append(out, target)
			seen[key] = true
		}
	}
	if len(selection.Segment) > 0 && len(out) < limit {
		segmentTargets, err := s.resolveBroadcastSegment(ctx, customerID, selection.Segment, limit-len(out))
		if err != nil {
			return nil, err
		}
		for _, target := range segmentTargets {
			key := target.UserID + "\x00" + target.SessionID
			if !seen[key] {
				out = append(out, target)
				seen[key] = true
			}
		}
	}
	return out, nil
}

func (s *Store) resolveBroadcastSegment(ctx context.Context, customerID string, segment map[string]any, limit int) ([]BroadcastTargetSpec, error) {
	if limit <= 0 {
		return nil, nil
	}
	clauses := []string{"customer_id=$1"}
	args := []any{customerID}
	nextArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if prefix, _ := segment["user_id_prefix"].(string); strings.TrimSpace(prefix) != "" {
		clauses = append(clauses, "user_id LIKE "+nextArg(prefix+"%"))
	}
	if prefix, _ := segment["session_id_prefix"].(string); strings.TrimSpace(prefix) != "" {
		clauses = append(clauses, "id LIKE "+nextArg(prefix+"%"))
	}
	if agentInstanceID, _ := segment["agent_instance_id"].(string); strings.TrimSpace(agentInstanceID) != "" {
		clauses = append(clauses, "agent_instance_id="+nextArg(agentInstanceID))
	}
	if updatedSince, _ := segment["updated_since"].(string); strings.TrimSpace(updatedSince) != "" {
		t, err := time.Parse(time.RFC3339, updatedSince)
		if err != nil {
			return nil, fmt.Errorf("segment.updated_since must be RFC3339")
		}
		clauses = append(clauses, "updated_at >= "+nextArg(t))
	}
	if len(clauses) == 1 {
		return nil, fmt.Errorf("segment requires at least one supported condition")
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (user_id) user_id, id
		FROM sessions
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY user_id, updated_at DESC
		LIMIT `+fmt.Sprintf("$%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BroadcastTargetSpec
	for rows.Next() {
		var target BroadcastTargetSpec
		if err := rows.Scan(&target.UserID, &target.SessionID); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
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
