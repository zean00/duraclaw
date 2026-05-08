package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ToolEvaluation struct {
	ID                string          `json:"id"`
	RunID             string          `json:"run_id"`
	CustomerID        string          `json:"customer_id"`
	UserID            string          `json:"user_id"`
	AgentInstanceID   string          `json:"agent_instance_id"`
	SessionID         string          `json:"session_id"`
	Status            string          `json:"status"`
	Category          string          `json:"category,omitempty"`
	Confidence        float64         `json:"confidence,omitempty"`
	ExpectedTools     json.RawMessage `json:"expected_tools"`
	ActualTools       json.RawMessage `json:"actual_tools"`
	Reason            string          `json:"reason,omitempty"`
	RepairAction      string          `json:"repair_action,omitempty"`
	RepairStatus      string          `json:"repair_status,omitempty"`
	Finding           json.RawMessage `json:"finding"`
	SuspiciousSignals json.RawMessage `json:"suspicious_signals"`
	LeaseOwner        *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt    *time.Time      `json:"lease_expires_at,omitempty"`
	CompletedAt       *time.Time      `json:"completed_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type ToolEvaluationUpdate struct {
	Status        string
	Category      string
	Confidence    float64
	ExpectedTools any
	ActualTools   any
	Reason        string
	RepairAction  string
	RepairStatus  string
	Finding       any
	Error         error
}

func (s *Store) QueueSuspiciousToolEvaluations(ctx context.Context, limit int, threshold float64, globalEnabled bool) (int, error) {
	if limit <= 0 || limit > 500 {
		limit = 25
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.75
	}
	tag, err := s.pool.Exec(ctx, `
		WITH candidates AS (
			SELECT r.id, r.customer_id, r.user_id, r.agent_instance_id, r.session_id,
			       jsonb_agg(DISTINCT signal) AS signals
			FROM runs r
			LEFT JOIN agent_instance_versions v ON v.id=r.agent_instance_version_id
			LEFT JOIN tool_evaluations te ON te.run_id=r.id
			CROSS JOIN LATERAL (
				SELECT 'tool_event'::text AS signal
				WHERE EXISTS (
					SELECT 1 FROM run_events e
					WHERE e.run_id=r.id
					AND e.event_type IN ('tool.required_missing','tool.suppressed','tool.failed','tool_selection.failed')
				)
				UNION ALL
				SELECT 'tool_failed'::text
				WHERE EXISTS (SELECT 1 FROM tool_calls tc WHERE tc.run_id=r.id AND tc.state='failed')
				   OR EXISTS (SELECT 1 FROM mcp_calls mc WHERE mc.run_id=r.id AND mc.state='failed')
				UNION ALL
				SELECT 'selected_tool_missing'::text
				WHERE EXISTS (
					SELECT 1 FROM run_events e
					WHERE e.run_id=r.id
					AND e.event_type='tool_selection.completed'
					AND COALESCE((e.payload->>'confidence')::double precision, 0) >= $2
					AND jsonb_array_length(COALESCE(e.payload->'selected_tools', '[]'::jsonb)) > 0
				)
				AND NOT EXISTS (SELECT 1 FROM tool_calls tc WHERE tc.run_id=r.id)
				AND NOT EXISTS (SELECT 1 FROM mcp_calls mc WHERE mc.run_id=r.id)
			) signals
			WHERE r.state='completed'
			AND te.run_id IS NULL
			AND ($3::boolean OR lower(COALESCE(v.profile_config->'tool_evaluator'->>'enabled', 'false')) IN ('true','1','yes','on'))
			GROUP BY r.id, r.customer_id, r.user_id, r.agent_instance_id, r.session_id
			ORDER BY r.completed_at NULLS LAST, r.created_at
			LIMIT $1
		)
		INSERT INTO tool_evaluations(run_id,customer_id,user_id,agent_instance_id,session_id,status,suspicious_signals)
		SELECT id, customer_id, user_id, agent_instance_id, session_id, 'queued', COALESCE(signals, '[]'::jsonb)
		FROM candidates
		ON CONFLICT (run_id) DO NOTHING`, limit, threshold, globalEnabled)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) QueueToolEvaluation(ctx context.Context, runID string, signals any) (*ToolEvaluation, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	signalsJSON, _ := json.Marshal(signals)
	if len(signalsJSON) == 0 || string(signalsJSON) == "null" {
		signalsJSON = []byte(`["manual"]`)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO tool_evaluations(run_id,customer_id,user_id,agent_instance_id,session_id,status,suspicious_signals)
		VALUES($1,$2,$3,$4,$5,'queued',$6)
		ON CONFLICT (run_id) DO UPDATE
		SET status='queued', suspicious_signals=EXCLUDED.suspicious_signals, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()`,
		run.ID, run.CustomerID, run.UserID, run.AgentInstanceID, run.SessionID, signalsJSON)
	if err != nil {
		return nil, err
	}
	return s.ToolEvaluationForRun(ctx, runID)
}

func (s *Store) ClaimToolEvaluations(ctx context.Context, owner string, leaseFor time.Duration, limit int) ([]ToolEvaluation, error) {
	if owner == "" {
		owner = "duraclaw-tool-evaluator"
	}
	if leaseFor <= 0 {
		leaseFor = 5 * time.Minute
	}
	if limit <= 0 || limit > 500 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM tool_evaluations
			WHERE status='queued'
			AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE tool_evaluations te
		SET status='running', lease_owner=$2, lease_expires_at=now()+$3::interval, updated_at=now()
		FROM candidates c
		WHERE te.id=c.id
		RETURNING te.id::text, te.run_id::text, te.customer_id, te.user_id, te.agent_instance_id, te.session_id,
		          te.status, te.category, te.confidence, te.expected_tools, te.actual_tools, te.reason,
		          te.repair_action, te.repair_status, te.finding, te.suspicious_signals, te.lease_owner,
		          te.lease_expires_at, te.completed_at, te.created_at, te.updated_at`,
		limit, owner, pgInterval(leaseFor))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToolEvaluations(rows)
}

func (s *Store) ClaimToolEvaluation(ctx context.Context, id, owner string, leaseFor time.Duration) (*ToolEvaluation, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ValidationError{Message: "evaluation_id is required"}
	}
	if owner == "" {
		owner = "duraclaw-tool-evaluator"
	}
	if leaseFor <= 0 {
		leaseFor = 5 * time.Minute
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE tool_evaluations
		SET status='running', lease_owner=$2, lease_expires_at=now()+$3::interval, updated_at=now()
		WHERE id=$1
		AND (status='queued' OR lease_expires_at IS NULL OR lease_expires_at < now())
		RETURNING id::text, run_id::text, customer_id, user_id, agent_instance_id, session_id,
		          status, category, confidence, expected_tools, actual_tools, reason,
		          repair_action, repair_status, finding, suspicious_signals, lease_owner,
		          lease_expires_at, completed_at, created_at, updated_at`,
		id, owner, pgInterval(leaseFor))
	return scanToolEvaluation(row)
}

func (s *Store) CompleteToolEvaluation(ctx context.Context, id string, update ToolEvaluationUpdate) error {
	status := strings.TrimSpace(update.Status)
	if status == "" {
		status = "completed"
	}
	if status != "completed" && status != "failed" {
		return fmt.Errorf("invalid tool evaluation status %q", status)
	}
	expectedJSON, _ := json.Marshal(update.ExpectedTools)
	actualJSON, _ := json.Marshal(update.ActualTools)
	findingJSON, _ := json.Marshal(update.Finding)
	if len(expectedJSON) == 0 || string(expectedJSON) == "null" {
		expectedJSON = []byte(`[]`)
	}
	if len(actualJSON) == 0 || string(actualJSON) == "null" {
		actualJSON = []byte(`[]`)
	}
	if len(findingJSON) == 0 || string(findingJSON) == "null" {
		findingJSON = []byte(`{}`)
	}
	reason := update.Reason
	if update.Error != nil && strings.TrimSpace(reason) == "" {
		reason = update.Error.Error()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE tool_evaluations
		SET status=$2, category=$3, confidence=$4, expected_tools=$5, actual_tools=$6, reason=$7,
		    repair_action=$8, repair_status=$9, finding=$10, lease_owner=NULL, lease_expires_at=NULL,
		    completed_at=now(), updated_at=now()
		WHERE id=$1`,
		id, status, update.Category, update.Confidence, expectedJSON, actualJSON, reason, update.RepairAction, update.RepairStatus, findingJSON)
	return err
}

func (s *Store) ToolEvaluation(ctx context.Context, customerID, id string) (*ToolEvaluation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, run_id::text, customer_id, user_id, agent_instance_id, session_id,
		       status, category, confidence, expected_tools, actual_tools, reason,
		       repair_action, repair_status, finding, suspicious_signals, lease_owner,
		       lease_expires_at, completed_at, created_at, updated_at
		FROM tool_evaluations
		WHERE customer_id=$1 AND id=$2`, customerID, id)
	return scanToolEvaluation(row)
}

func (s *Store) ToolEvaluationForRun(ctx context.Context, runID string) (*ToolEvaluation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, run_id::text, customer_id, user_id, agent_instance_id, session_id,
		       status, category, confidence, expected_tools, actual_tools, reason,
		       repair_action, repair_status, finding, suspicious_signals, lease_owner,
		       lease_expires_at, completed_at, created_at, updated_at
		FROM tool_evaluations
		WHERE run_id=$1`, runID)
	return scanToolEvaluation(row)
}

func (s *Store) ListToolEvaluations(ctx context.Context, customerID, status, category string, limit int) ([]ToolEvaluation, error) {
	if strings.TrimSpace(customerID) == "" {
		return nil, ValidationError{Message: "customer_id is required"}
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID}
	where := "customer_id=$1"
	if strings.TrimSpace(status) != "" {
		args = append(args, status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if strings.TrimSpace(category) != "" {
		args = append(args, category)
		where += fmt.Sprintf(" AND category=$%d", len(args))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, run_id::text, customer_id, user_id, agent_instance_id, session_id,
		       status, category, confidence, expected_tools, actual_tools, reason,
		       repair_action, repair_status, finding, suspicious_signals, lease_owner,
		       lease_expires_at, completed_at, created_at, updated_at
		FROM tool_evaluations
		WHERE `+where+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToolEvaluations(rows)
}

func (s *Store) toolEvaluationsForTrace(ctx context.Context, runID string) ([]ToolEvaluation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, run_id::text, customer_id, user_id, agent_instance_id, session_id,
		       status, category, confidence, expected_tools, actual_tools, reason,
		       repair_action, repair_status, finding, suspicious_signals, lease_owner,
		       lease_expires_at, completed_at, created_at, updated_at
		FROM tool_evaluations
		WHERE run_id=$1
		ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToolEvaluations(rows)
}

type toolEvaluationScanner interface {
	Scan(...any) error
}

func scanToolEvaluation(row toolEvaluationScanner) (*ToolEvaluation, error) {
	var ev ToolEvaluation
	if err := row.Scan(&ev.ID, &ev.RunID, &ev.CustomerID, &ev.UserID, &ev.AgentInstanceID, &ev.SessionID,
		&ev.Status, &ev.Category, &ev.Confidence, &ev.ExpectedTools, &ev.ActualTools, &ev.Reason,
		&ev.RepairAction, &ev.RepairStatus, &ev.Finding, &ev.SuspiciousSignals, &ev.LeaseOwner,
		&ev.LeaseExpiresAt, &ev.CompletedAt, &ev.CreatedAt, &ev.UpdatedAt); err != nil {
		return nil, err
	}
	return &ev, nil
}

func scanToolEvaluations(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]ToolEvaluation, error) {
	var out []ToolEvaluation
	for rows.Next() {
		ev, err := scanToolEvaluation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ev)
	}
	return out, rows.Err()
}
