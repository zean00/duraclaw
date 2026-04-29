package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SessionSummary struct {
	CustomerID  string          `json:"customer_id"`
	SessionID   string          `json:"session_id"`
	Summary     string          `json:"summary"`
	SourceRunID *string         `json:"source_run_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (s *Store) UpsertSessionSummary(ctx context.Context, customerID, sessionID, sourceRunID, summary string, metadata any) error {
	if customerID == "" || sessionID == "" {
		return fmt.Errorf("customer_id and session_id are required")
	}
	b, _ := json.Marshal(metadata)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO session_summaries(customer_id,session_id,source_run_id,summary,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (customer_id, session_id) DO UPDATE
		SET source_run_id=EXCLUDED.source_run_id, summary=EXCLUDED.summary, metadata=EXCLUDED.metadata, updated_at=now()`,
		customerID, sessionID, nullableRunID(sourceRunID), summary, b)
	return err
}

func (s *Store) SessionSummary(ctx context.Context, customerID, sessionID string) (*SessionSummary, error) {
	var out SessionSummary
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, session_id, source_run_id::text, summary, metadata, updated_at
		FROM session_summaries
		WHERE customer_id=$1 AND session_id=$2`, customerID, sessionID).
		Scan(&out.CustomerID, &out.SessionID, &out.SourceRunID, &out.Summary, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) SetRunProgress(ctx context.Context, runID string, progress any) error {
	b, _ := json.Marshal(progress)
	_, err := s.pool.Exec(ctx, `UPDATE runs SET progress=$2, updated_at=now() WHERE id=$1`, runID, b)
	return err
}

func (s *Store) RunProgress(ctx context.Context, runID string) (json.RawMessage, error) {
	var progress json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT progress FROM runs WHERE id=$1`, runID).Scan(&progress)
	if len(progress) == 0 {
		progress = json.RawMessage(`{}`)
	}
	return progress, err
}

func (s *Store) MarkRunBackground(ctx context.Context, runID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET run_mode='background', updated_at=now() WHERE id=$1`, runID)
	return err
}

func (s *Store) CreateBackgroundRun(ctx context.Context, c ACPContext, input any, progress any) (*Run, error) {
	var out *Run
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO customers(id) VALUES($1) ON CONFLICT DO NOTHING`, c.CustomerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.CustomerID, c.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.CustomerID, c.AgentInstanceID); err != nil {
			return err
		}
		if err := ensureDefaultAgentInstanceVersion(ctx, tx, c.CustomerID, c.AgentInstanceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sessions(customer_id,user_id,agent_instance_id,id)
			VALUES($1,$2,$3,$4)
			ON CONFLICT (customer_id,id) DO UPDATE
			SET user_id=EXCLUDED.user_id, updated_at=now()`,
			c.CustomerID, c.UserID, c.AgentInstanceID, c.SessionID); err != nil {
			return err
		}
		var effectiveAgentInstanceID string
		if err := tx.QueryRow(ctx, `SELECT agent_instance_id FROM sessions WHERE customer_id=$1 AND id=$2`, c.CustomerID, c.SessionID).Scan(&effectiveAgentInstanceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, backgroundQuotaLockKey(c.CustomerID, effectiveAgentInstanceID)); err != nil {
			return err
		}
		var existing Run
		err := tx.QueryRow(ctx, `
			SELECT id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at
			FROM runs
			WHERE customer_id=$1 AND session_id=$2 AND idempotency_key=$3`,
			c.CustomerID, c.SessionID, c.IdempotencyKey).
			Scan(&existing.ID, &existing.CustomerID, &existing.UserID, &existing.AgentInstanceID, &existing.AgentInstanceVersionID, &existing.SessionID, &existing.RequestID, &existing.IdempotencyKey, &existing.State, &existing.Input, &existing.Error, &existing.CreatedAt, &existing.UpdatedAt, &existing.CompletedAt)
		if err == nil {
			out = &existing
			return nil
		}
		if err != pgx.ErrNoRows {
			return err
		}
		if err := s.enforceRunQuotaTx(ctx, tx, c.CustomerID, effectiveAgentInstanceID); err != nil {
			return err
		}
		if err := s.enforceBackgroundQuotaTx(ctx, tx, c.CustomerID, effectiveAgentInstanceID); err != nil {
			return err
		}
		var versionID string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(current_version_id::text,'')
			FROM agent_instances
			WHERE customer_id=$1 AND id=$2`, c.CustomerID, effectiveAgentInstanceID).Scan(&versionID); err != nil {
			return err
		}
		var versionArg any
		if versionID != "" {
			versionArg = versionID
		}
		inputJSON, _ := json.Marshal(input)
		progressJSON, _ := json.Marshal(progress)
		if len(progressJSON) == 0 || string(progressJSON) == "null" {
			progressJSON = []byte(`{}`)
		}
		channelJSON, _ := json.Marshal(map[string]string{
			"channel_type": c.ChannelType, "channel_user_id": c.ChannelUserID, "channel_conversation_id": c.ChannelConvID, "trace_id": c.TraceID, "traceparent": c.TraceParent,
		})
		var run Run
		if err := tx.QueryRow(ctx, `
			INSERT INTO runs(customer_id,user_id,agent_instance_id,agent_instance_version_id,session_id,request_id,idempotency_key,state,input,channel_context,run_mode,progress)
			VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9,'background',$10)
			RETURNING id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at`,
			c.CustomerID, c.UserID, effectiveAgentInstanceID, versionArg, c.SessionID, c.RequestID, c.IdempotencyKey, inputJSON, channelJSON, progressJSON).
			Scan(&run.ID, &run.CustomerID, &run.UserID, &run.AgentInstanceID, &run.AgentInstanceVersionID, &run.SessionID, &run.RequestID, &run.IdempotencyKey, &run.State, &run.Input, &run.Error, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
			return err
		}
		if _, err := insertMessageTx(ctx, tx, run.CustomerID, run.SessionID, run.ID, "user", input); err != nil {
			return err
		}
		if err := addEventTx(ctx, tx, run.ID, "run.queued", map[string]any{"state": run.State, "run_mode": "background"}); err != nil {
			return err
		}
		out = &run
		return nil
	})
	if err != nil {
		if IsQuotaExceeded(err) {
			_ = s.AddObservabilityEvent(ctx, c.CustomerID, "", "quota_exceeded", map[string]any{"error": err.Error(), "agent_instance_id": c.AgentInstanceID, "run_mode": "background"})
		}
		return nil, err
	}
	return out, nil
}

func backgroundQuotaLockKey(customerID, agentInstanceID string) int64 {
	sum := sha256.Sum256([]byte(customerID + "\x00" + agentInstanceID + "\x00background_runs"))
	return int64(sum[0])<<56 | int64(sum[1])<<48 | int64(sum[2])<<40 | int64(sum[3])<<32 | int64(sum[4])<<24 | int64(sum[5])<<16 | int64(sum[6])<<8 | int64(sum[7])
}

func (s *Store) enforceRunQuotaTx(ctx context.Context, tx pgx.Tx, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxQueuedRuns > 0 {
		count, err := runCountTx(ctx, tx, customerID, agentInstanceID, []string{"queued"})
		if err != nil {
			return err
		}
		if count >= limits.MaxQueuedRuns {
			return QuotaExceededError{Kind: "queued_runs", Limit: int64(limits.MaxQueuedRuns), Count: int64(count + 1)}
		}
	}
	if limits.MaxActiveRuns > 0 {
		count, err := runCountTx(ctx, tx, customerID, agentInstanceID, []string{"leased", "running", "running_workflow", "awaiting_user"})
		if err != nil {
			return err
		}
		if count >= limits.MaxActiveRuns {
			return QuotaExceededError{Kind: "active_runs", Limit: int64(limits.MaxActiveRuns), Count: int64(count + 1)}
		}
	}
	return nil
}

func (s *Store) enforceBackgroundQuotaTx(ctx context.Context, tx pgx.Tx, customerID, agentInstanceID string) error {
	limits, err := s.EffectiveRuntimeLimits(ctx, customerID, agentInstanceID)
	if err != nil {
		return err
	}
	if limits.MaxBackgroundRuns <= 0 {
		return nil
	}
	var count int
	err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM runs
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND run_mode='background'
		AND state IN ('queued','leased','running','running_workflow','awaiting_user')`, customerID, agentInstanceID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= limits.MaxBackgroundRuns {
		return QuotaExceededError{Kind: "background_runs", Limit: int64(limits.MaxBackgroundRuns), Count: int64(count + 1)}
	}
	return nil
}

func runCountTx(ctx context.Context, tx pgx.Tx, customerID, agentInstanceID string, states []string) (int, error) {
	var count int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM runs
		WHERE customer_id=$1
		AND agent_instance_id=$2
		AND state = ANY($3)`, customerID, agentInstanceID, states).Scan(&count)
	return count, err
}

func insertMessageTx(ctx context.Context, tx pgx.Tx, customerID, sessionID, runID, role string, content any) (string, error) {
	b, _ := json.Marshal(content)
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO messages(customer_id,session_id,run_id,role,content) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, customerID, sessionID, runID, role, b).Scan(&id)
	return id, err
}

func addEventTx(ctx context.Context, tx pgx.Tx, runID, typ string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO run_events(run_id,event_type,payload) VALUES($1,$2,$3)`, runID, typ, b)
	return err
}

func (s *Store) BackgroundRuns(ctx context.Context, customerID, agentInstanceID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at
		FROM runs
		WHERE customer_id=$1
		AND ($2='' OR agent_instance_id=$2)
		AND run_mode='background'
		ORDER BY created_at DESC
		LIMIT $3`, customerID, agentInstanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UserBackgroundRuns(ctx context.Context, customerID, userID, agentInstanceID string, limit int) ([]Run, error) {
	if customerID == "" || userID == "" {
		return nil, fmt.Errorf("customer_id and user_id are required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at
		FROM runs
		WHERE customer_id=$1
		AND user_id=$2
		AND ($3='' OR agent_instance_id=$3)
		AND run_mode='background'
		ORDER BY created_at DESC
		LIMIT $4`, customerID, userID, agentInstanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CancelUserBackgroundRun(ctx context.Context, runID, customerID, userID string) error {
	if runID == "" || customerID == "" || userID == "" {
		return fmt.Errorf("run_id, customer_id, and user_id are required")
	}
	var id string
	if err := s.pool.QueryRow(ctx, `
		SELECT id::text
		FROM runs
		WHERE id=$1 AND customer_id=$2 AND user_id=$3 AND run_mode='background'`,
		runID, customerID, userID).Scan(&id); err != nil {
		return err
	}
	return s.SetRunState(ctx, id, "cancelled", nil)
}
