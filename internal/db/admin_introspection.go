package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type AdminSession struct {
	CustomerID       string          `json:"customer_id"`
	UserID           string          `json:"user_id"`
	AgentInstanceID  string          `json:"agent_instance_id"`
	ID               string          `json:"id"`
	Metadata         json.RawMessage `json:"metadata"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	LastMessageAt    *time.Time      `json:"last_message_at,omitempty"`
	LastRunAt        *time.Time      `json:"last_run_at,omitempty"`
	RunCount         int             `json:"run_count"`
	MessageCount     int             `json:"message_count"`
	DelegationParent bool            `json:"delegation_parent"`
	DelegationChild  bool            `json:"delegation_child"`
}

func (s *Store) AdminSessions(ctx context.Context, customerID, userID string, limit int) ([]AdminSession, error) {
	if customerID == "" || userID == "" {
		return nil, fmt.Errorf("customer_id and user_id are required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.customer_id,s.user_id,s.agent_instance_id,s.id,s.metadata,s.created_at,s.updated_at,
		       (SELECT max(m.created_at) FROM messages m WHERE m.customer_id=s.customer_id AND m.session_id=s.id) AS last_message_at,
		       (SELECT max(r.created_at) FROM runs r WHERE r.customer_id=s.customer_id AND r.session_id=s.id) AS last_run_at,
		       (SELECT count(*) FROM runs r WHERE r.customer_id=s.customer_id AND r.session_id=s.id) AS run_count,
		       (SELECT count(*) FROM messages m WHERE m.customer_id=s.customer_id AND m.session_id=s.id) AS message_count,
		       EXISTS(SELECT 1 FROM agent_delegations d WHERE d.customer_id=s.customer_id AND d.parent_session_id=s.id) AS delegation_parent,
		       EXISTS(SELECT 1 FROM agent_delegations d WHERE d.customer_id=s.customer_id AND d.child_session_id=s.id) AS delegation_child
		FROM sessions s
		WHERE s.customer_id=$1 AND s.user_id=$2
		ORDER BY coalesce((SELECT max(m.created_at) FROM messages m WHERE m.customer_id=s.customer_id AND m.session_id=s.id), s.updated_at) DESC, s.id DESC
		LIMIT $3`, customerID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminSession{}
	for rows.Next() {
		var rec AdminSession
		if err := rows.Scan(&rec.CustomerID, &rec.UserID, &rec.AgentInstanceID, &rec.ID, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt, &rec.LastMessageAt, &rec.LastRunAt, &rec.RunCount, &rec.MessageCount, &rec.DelegationParent, &rec.DelegationChild); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) AdminSessionMessages(ctx context.Context, customerID, userID, sessionID string, limit int) ([]Message, error) {
	if customerID == "" || userID == "" || sessionID == "" {
		return nil, fmt.Errorf("customer_id, user_id, and session_id are required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.id::text, coalesce(m.run_id::text,''), m.role, m.content, m.created_at
		FROM messages m
		JOIN sessions s ON s.customer_id=m.customer_id AND s.id=m.session_id
		WHERE m.customer_id=$1 AND s.user_id=$2 AND m.session_id=$3
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $4`, customerID, userID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var rec Message
		if err := rows.Scan(&rec.ID, &rec.RunID, &rec.Role, &rec.Content, &rec.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) AdminRuns(ctx context.Context, customerID, userID, sessionID string, limit int) ([]Run, error) {
	if customerID == "" || userID == "" {
		return nil, fmt.Errorf("customer_id and user_id are required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args := []any{customerID, userID}
	where := "customer_id=$1 AND user_id=$2"
	if sessionID != "" {
		args = append(args, sessionID)
		where += fmt.Sprintf(" AND session_id=$%d", len(args))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, coalesce(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at, coalesce(refinement_parent_run_id::text,''), refinement_depth, suppress_direct_outbound, interrupt_window_started_at
		FROM runs
		WHERE `+where+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var rec Run
		if err := rows.Scan(&rec.ID, &rec.CustomerID, &rec.UserID, &rec.AgentInstanceID, &rec.AgentInstanceVersionID, &rec.SessionID, &rec.RequestID, &rec.IdempotencyKey, &rec.State, &rec.Input, &rec.Error, &rec.CreatedAt, &rec.UpdatedAt, &rec.CompletedAt, &rec.RefinementParentRunID, &rec.RefinementDepth, &rec.SuppressDirectOutbound, &rec.InterruptWindowStarted); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) AdminRunForUser(ctx context.Context, customerID, userID, runID string) (*Run, error) {
	if customerID == "" || userID == "" || runID == "" {
		return nil, fmt.Errorf("customer_id, user_id, and run_id are required")
	}
	var rec Run
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, coalesce(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at, coalesce(refinement_parent_run_id::text,''), refinement_depth, suppress_direct_outbound, interrupt_window_started_at
		FROM runs
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`, runID, customerID, userID).
		Scan(&rec.ID, &rec.CustomerID, &rec.UserID, &rec.AgentInstanceID, &rec.AgentInstanceVersionID, &rec.SessionID, &rec.RequestID, &rec.IdempotencyKey, &rec.State, &rec.Input, &rec.Error, &rec.CreatedAt, &rec.UpdatedAt, &rec.CompletedAt, &rec.RefinementParentRunID, &rec.RefinementDepth, &rec.SuppressDirectOutbound, &rec.InterruptWindowStarted)
	if err != nil {
		return nil, fmt.Errorf("run not found")
	}
	return &rec, nil
}

func (s *Store) AdminAgentDelegations(ctx context.Context, customerID, userID string, limit int) ([]AgentDelegation, error) {
	if customerID == "" || userID == "" {
		return nil, fmt.Errorf("customer_id and user_id are required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at
		FROM agent_delegations
		WHERE customer_id=$1 AND user_id=$2
		ORDER BY created_at DESC
		LIMIT $3`, customerID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentDelegation{}
	for rows.Next() {
		var rec AgentDelegation
		if err := rows.Scan(&rec.ID, &rec.CustomerID, &rec.UserID, &rec.SourceAgentInstanceID, &rec.TargetAgentInstanceID, &rec.TargetHandle, &rec.ParentSessionID, &rec.ParentRunID, &rec.ChildSessionID, &rec.ChildRunID, &rec.ExactMessage, &rec.ContextSummary, &rec.Status, &rec.ResultText, &rec.Error, &rec.Metadata, &rec.CreatedAt, &rec.UpdatedAt, &rec.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
