package db

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

type ACPContext struct {
	CustomerID      string
	UserID          string
	AgentInstanceID string
	SessionID       string
	RequestID       string
	IdempotencyKey  string
	RunID           string
	ChannelType     string
	ChannelUserID   string
	ChannelConvID   string
	TraceID         string
}

type ContentPart struct {
	Type string         `json:"type"`
	Text string         `json:"text,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

type Run struct {
	ID                     string          `json:"id"`
	CustomerID             string          `json:"customer_id"`
	UserID                 string          `json:"user_id"`
	AgentInstanceID        string          `json:"agent_instance_id"`
	AgentInstanceVersionID string          `json:"agent_instance_version_id,omitempty"`
	SessionID              string          `json:"session_id"`
	RequestID              string          `json:"request_id"`
	IdempotencyKey         string          `json:"idempotency_key"`
	State                  string          `json:"state"`
	Input                  json.RawMessage `json:"input"`
	Error                  *string         `json:"error,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	CompletedAt            *time.Time      `json:"completed_at,omitempty"`
}

type RunStep struct {
	ID          string          `json:"id"`
	RunID       string          `json:"run_id"`
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	Input       json.RawMessage `json:"input"`
	Output      json.RawMessage `json:"output"`
	Error       *string         `json:"error,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Message struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
}

type Event struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"run_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type ObservabilityEvent struct {
	ID         int64           `json:"id"`
	CustomerID string          `json:"customer_id"`
	RunID      *string         `json:"run_id,omitempty"`
	EventType  string          `json:"event_type"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type SchedulerJob struct {
	ID             string          `json:"id"`
	CustomerID     string          `json:"customer_id"`
	JobType        string          `json:"job_type"`
	Schedule       string          `json:"schedule"`
	NextRunAt      time.Time       `json:"next_run_at"`
	Payload        json.RawMessage `json:"payload"`
	Enabled        bool            `json:"enabled"`
	LeaseOwner     *string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	LastFiredAt    *time.Time      `json:"last_fired_at,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
}

type SchedulerJobSpec struct {
	CustomerID      string
	UserID          string
	AgentInstanceID string
	SessionID       string
	JobType         string
	Schedule        string
	NextRunAt       time.Time
	Input           any
	Metadata        any
}

type OutboxItem struct {
	ID          int64           `json:"id"`
	Topic       string          `json:"topic"`
	Payload     json.RawMessage `json:"payload"`
	AvailableAt time.Time       `json:"available_at"`
	ClaimedAt   *time.Time      `json:"claimed_at,omitempty"`
	ClaimOwner  *string         `json:"claim_owner,omitempty"`
}

type ToolCallRecord struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	ToolName  string          `json:"tool_name"`
	State     string          `json:"state"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Retryable bool            `json:"retryable"`
	ArgsHash  string          `json:"args_hash"`
	Error     *string         `json:"error,omitempty"`
}

type Store struct{ pool *Pool }

func NewStore(pool *Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error {
	return Ping(ctx, s.pool)
}

func (s *Store) EnsureSession(ctx context.Context, c ACPContext) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
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
		_, err := tx.Exec(ctx, `
			INSERT INTO sessions(customer_id,user_id,agent_instance_id,id)
			VALUES($1,$2,$3,$4)
			ON CONFLICT (customer_id,id) DO UPDATE
			SET user_id=EXCLUDED.user_id, updated_at=now()`,
			c.CustomerID, c.UserID, c.AgentInstanceID, c.SessionID)
		return err
	})
}

func (s *Store) CreateRun(ctx context.Context, c ACPContext, input any) (*Run, error) {
	if err := s.EnsureSession(ctx, c); err != nil {
		return nil, err
	}
	effectiveAgentInstanceID, err := s.sessionAgentInstanceID(ctx, c.CustomerID, c.SessionID)
	if err != nil {
		return nil, err
	}
	versionID, err := s.currentAgentInstanceVersionID(ctx, c.CustomerID, effectiveAgentInstanceID)
	if err != nil {
		return nil, err
	}
	var versionArg any
	if versionID != "" {
		versionArg = versionID
	}
	inputJSON, _ := json.Marshal(input)
	channelJSON, _ := json.Marshal(map[string]string{
		"channel_type": c.ChannelType, "channel_user_id": c.ChannelUserID, "channel_conversation_id": c.ChannelConvID, "trace_id": c.TraceID,
	})
	var r Run
	var inserted bool
	err = s.pool.QueryRow(ctx, `
		INSERT INTO runs(customer_id,user_id,agent_instance_id,agent_instance_version_id,session_id,request_id,idempotency_key,state,input,channel_context)
		VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9)
		ON CONFLICT (customer_id,session_id,idempotency_key) DO UPDATE SET updated_at=runs.updated_at
		RETURNING id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at, xmax = 0`,
		c.CustomerID, c.UserID, effectiveAgentInstanceID, versionArg, c.SessionID, c.RequestID, c.IdempotencyKey, inputJSON, channelJSON).
		Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt, &inserted)
	if err != nil {
		return nil, err
	}
	if inserted {
		_, _ = s.InsertMessage(ctx, r.CustomerID, r.SessionID, r.ID, "user", input)
		_ = s.AddEvent(ctx, r.ID, "run.queued", map[string]any{"state": r.State})
	}
	return &r, nil
}

func (s *Store) GetRun(ctx context.Context, runID string) (*Run, error) {
	var r Run
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at
		FROM runs WHERE id=$1`, runID).
		Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) LatestRun(ctx context.Context, customerID, sessionID string) (*Run, error) {
	return s.oneRun(ctx, `WHERE customer_id=$1 AND session_id=$2 ORDER BY created_at DESC LIMIT 1`, customerID, sessionID)
}

func (s *Store) RunByIdempotencyKey(ctx context.Context, customerID, sessionID, key string) (*Run, error) {
	return s.oneRun(ctx, `WHERE customer_id=$1 AND session_id=$2 AND idempotency_key=$3 LIMIT 1`, customerID, sessionID, key)
}

func (s *Store) oneRun(ctx context.Context, where string, args ...any) (*Run, error) {
	var r Run
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, user_id, agent_instance_id, COALESCE(agent_instance_version_id::text,''), session_id, request_id, idempotency_key, state, input, error, created_at, updated_at, completed_at
		FROM runs `+where, args...).
		Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) AddEvent(ctx context.Context, runID, typ string, payload any) error {
	b, _ := json.Marshal(payload)
	_, err := s.pool.Exec(ctx, `INSERT INTO run_events(run_id,event_type,payload) VALUES($1,$2,$3)`, runID, typ, b)
	return err
}

func (s *Store) Events(ctx context.Context, runID string, after int64) ([]Event, error) {
	return s.EventsPage(ctx, runID, after, 500)
}

func (s *Store) EventsPage(ctx context.Context, runID string, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `SELECT id, run_id::text, event_type, payload, created_at FROM run_events WHERE run_id=$1 AND id>$2 ORDER BY id LIMIT $3`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.RunID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ClaimRun(ctx context.Context, owner string, leaseFor time.Duration) (*Run, error) {
	var r Run
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			WITH candidate AS (
				SELECT r.id
				FROM runs r
				WHERE r.state='queued'
				AND NOT EXISTS (
					SELECT 1 FROM runs active
					WHERE active.customer_id=r.customer_id
					AND active.session_id=r.session_id
					AND active.state IN ('leased','running','running_workflow','awaiting_user')
				)
				ORDER BY r.created_at
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			UPDATE runs r
			SET state='leased', lease_owner=$1, leased_at=now(), lease_expires_at=now()+$2::interval, updated_at=now()
			FROM candidate
			WHERE r.id=candidate.id
			RETURNING r.id::text, r.customer_id, r.user_id, r.agent_instance_id, COALESCE(r.agent_instance_version_id::text,''), r.session_id, r.request_id, r.idempotency_key, r.state, r.input, r.error, r.created_at, r.updated_at, r.completed_at`,
			owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
		return row.Scan(&r.ID, &r.CustomerID, &r.UserID, &r.AgentInstanceID, &r.AgentInstanceVersionID, &r.SessionID, &r.RequestID, &r.IdempotencyKey, &r.State, &r.Input, &r.Error, &r.CreatedAt, &r.UpdatedAt, &r.CompletedAt)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = s.AddEvent(ctx, r.ID, "run.leased", map[string]any{"owner": owner})
	return &r, nil
}

func (s *Store) RecoverExpiredLeases(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs
		SET state='queued', lease_owner=NULL, leased_at=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE state IN ('leased','running','running_workflow')
		AND lease_expires_at IS NOT NULL
		AND lease_expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ExtendRunLease(ctx context.Context, runID, owner string, leaseFor time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE runs
		SET lease_expires_at=now()+$3::interval, updated_at=now()
		WHERE id=$1
		AND lease_owner=$2
		AND state IN ('leased','running','running_workflow')`,
		runID, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) RunState(ctx context.Context, runID string) (string, error) {
	var state string
	err := s.pool.QueryRow(ctx, `SELECT state FROM runs WHERE id=$1`, runID).Scan(&state)
	return state, err
}

func (s *Store) SetRunState(ctx context.Context, runID, state string, errText *string) error {
	if !ValidRunState(state) {
		return fmt.Errorf("invalid run state %q", state)
	}
	completed := ""
	if state == "completed" || state == "failed" || state == "cancelled" || state == "expired" {
		completed = ", completed_at=now(), lease_owner=NULL, lease_expires_at=NULL"
	}
	_, err := s.pool.Exec(ctx, `UPDATE runs SET state=$2, error=$3, updated_at=now()`+completed+` WHERE id=$1`, runID, state, errText)
	if err == nil {
		_ = s.AddEvent(ctx, runID, "run."+state, map[string]any{"state": state, "error": errText})
		_ = s.AddObservabilityEvent(ctx, "", runID, "run_state_changed", map[string]any{"state": state})
	}
	return err
}

func (s *Store) InsertMessage(ctx context.Context, customerID, sessionID, runID, role string, content any) (string, error) {
	b, _ := json.Marshal(content)
	var id string
	err := s.pool.QueryRow(ctx, `INSERT INTO messages(customer_id,session_id,run_id,role,content) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, customerID, sessionID, runID, role, b).Scan(&id)
	return id, err
}

func (s *Store) RecentMessages(ctx context.Context, customerID, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 12
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, role, content, created_at
		FROM (
			SELECT id, role, content, created_at
			FROM messages
			WHERE customer_id=$1 AND session_id=$2
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

func (s *Store) CompleteRunWithMessage(ctx context.Context, runID, messageID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runs SET state='completed', final_message_id=$2, completed_at=now(), lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1`, runID, messageID)
	if err == nil {
		_ = s.AddEvent(ctx, runID, "run.completed", map[string]any{"message_id": messageID})
		_ = s.AddObservabilityEvent(ctx, "", runID, "run_state_changed", map[string]any{"state": "completed"})
	}
	return err
}

func (s *Store) Checkpoint(ctx context.Context, runID, key string, state any) error {
	b, _ := json.Marshal(state)
	_, err := s.pool.Exec(ctx, `INSERT INTO checkpoints(run_id,checkpoint_key,state) VALUES($1,$2,$3)`, runID, key, b)
	return err
}

func (s *Store) StartRunStep(ctx context.Context, runID, kind string, input any) (string, error) {
	b, _ := json.Marshal(input)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO run_steps(run_id,kind,state,input,started_at)
		VALUES($1,$2,'running',$3,now())
		RETURNING id::text`, runID, kind, b).Scan(&id)
	if err == nil {
		_ = s.AddEvent(ctx, runID, "step.started", map[string]any{"step_id": id, "kind": kind})
	}
	return id, err
}

func (s *Store) CompleteRunStep(ctx context.Context, runID, stepID, state string, output any, errText *string) error {
	if !ValidStepState(state) {
		return fmt.Errorf("invalid run step state %q", state)
	}
	b, _ := json.Marshal(output)
	_, err := s.pool.Exec(ctx, `
		UPDATE run_steps
		SET state=$2, output=$3, error=$4, completed_at=now()
		WHERE id=$1`, stepID, state, b, errText)
	if err == nil {
		_ = s.AddEvent(ctx, runID, "step."+state, map[string]any{"step_id": stepID})
	}
	return err
}

func (s *Store) AttachArtifact(ctx context.Context, runID string, a Artifact) error {
	if !ValidArtifactState(a.State) {
		return fmt.Errorf("invalid artifact state %q", a.State)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var customerID string
		if err := tx.QueryRow(ctx, `SELECT customer_id FROM runs WHERE id=$1`, runID).Scan(&customerID); err != nil {
			return err
		}
		metadata, _ := json.Marshal(a.Metadata)
		_, err := tx.Exec(ctx, `
			INSERT INTO artifacts(id,customer_id,run_id,modality,media_type,filename,size_bytes,checksum,storage_ref,source_channel,source_message_id,state,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (id) DO UPDATE SET run_id=EXCLUDED.run_id, state=EXCLUDED.state, updated_at=now()`,
			a.ID, customerID, runID, a.Modality, a.MediaType, a.Filename, a.SizeBytes, a.Checksum, a.StorageRef, a.SourceChannel, a.SourceMessageID, a.State, metadata)
		return err
	})
}

type Artifact struct {
	ID              string         `json:"artifact_id"`
	Modality        string         `json:"modality"`
	MediaType       string         `json:"media_type"`
	Filename        string         `json:"filename"`
	SizeBytes       int64          `json:"size_bytes"`
	Checksum        string         `json:"checksum"`
	StorageRef      string         `json:"storage_ref"`
	SourceChannel   string         `json:"source_channel"`
	SourceMessageID string         `json:"source_message_id"`
	State           string         `json:"state"`
	Metadata        map[string]any `json:"metadata"`
}

type ArtifactRepresentation struct {
	ID         int64           `json:"id"`
	ArtifactID string          `json:"artifact_id"`
	Type       string          `json:"representation_type"`
	Summary    string          `json:"summary"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (s *Store) ArtifactsForRun(ctx context.Context, runID string) ([]Artifact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, modality, media_type, filename, size_bytes, checksum, storage_ref, source_channel, source_message_id, state, metadata
		FROM artifacts WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var metadata []byte
		if err := rows.Scan(&a.ID, &a.Modality, &a.MediaType, &a.Filename, &a.SizeBytes, &a.Checksum, &a.StorageRef, &a.SourceChannel, &a.SourceMessageID, &a.State, &metadata); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &a.Metadata)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ArtifactRepresentations(ctx context.Context, customerID, artifactID string) ([]ArtifactRepresentation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.artifact_id, r.representation_type, r.summary, r.metadata, r.created_at
		FROM artifact_representations r
		JOIN artifacts a ON a.id=r.artifact_id
		WHERE r.artifact_id=$1 AND a.customer_id=$2
		ORDER BY r.created_at ASC, r.id ASC`, artifactID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArtifactRepresentation
	for rows.Next() {
		var rep ArtifactRepresentation
		if err := rows.Scan(&rep.ID, &rep.ArtifactID, &rep.Type, &rep.Summary, &rep.Metadata, &rep.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func (s *Store) StartProcessorCall(ctx context.Context, runID, artifactID, processor string, request any) (string, error) {
	b, _ := json.Marshal(request)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO processor_calls(run_id,artifact_id,processor,state,request_summary)
		VALUES($1,$2,$3,'running',$4)
		RETURNING id::text`, runID, artifactID, processor, b).Scan(&id)
	if err == nil {
		payload := map[string]any{"processor_call_id": id, "artifact_id": artifactID, "processor": processor}
		_ = s.AddEvent(ctx, runID, "processor.started", payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "processor.started", payload)
	}
	return id, err
}

func (s *Store) CompleteProcessorCall(ctx context.Context, callID, runID string, response any, errText *string) error {
	state := "succeeded"
	if errText != nil {
		state = "failed"
	}
	b, _ := json.Marshal(response)
	_, err := s.pool.Exec(ctx, `UPDATE processor_calls SET state=$2, response_summary=$3, error=$4, completed_at=now() WHERE id=$1`, callID, state, b, errText)
	if err == nil {
		payload := map[string]any{"processor_call_id": callID, "state": state}
		_ = s.AddEvent(ctx, runID, "processor."+state, payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "processor."+state, payload)
	}
	return err
}

func (s *Store) InsertArtifactRepresentation(ctx context.Context, artifactID, typ, summary string, metadata any) error {
	b, _ := json.Marshal(metadata)
	_, err := s.pool.Exec(ctx, `INSERT INTO artifact_representations(artifact_id,representation_type,summary,metadata) VALUES($1,$2,$3,$4)`, artifactID, typ, summary, b)
	return err
}

func (s *Store) SetArtifactState(ctx context.Context, artifactID, state string) error {
	if !ValidArtifactState(state) {
		return fmt.Errorf("invalid artifact state %q", state)
	}
	_, err := s.pool.Exec(ctx, `UPDATE artifacts SET state=$2, updated_at=now() WHERE id=$1`, artifactID, state)
	return err
}

func (s *Store) StartModelCall(ctx context.Context, runID, provider, model string, request any) (string, error) {
	b, _ := json.Marshal(request)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO model_calls(run_id,provider,model,state,request_summary)
		VALUES($1,$2,$3,'running',$4)
		RETURNING id::text`, runID, provider, model, b).Scan(&id)
	if err == nil {
		payload := map[string]any{"model_call_id": id, "provider": provider, "model": model}
		_ = s.AddEvent(ctx, runID, "model.started", payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "model.started", payload)
	}
	return id, err
}

func (s *Store) CompleteModelCall(ctx context.Context, callID, runID string, response any, errText *string) error {
	state := "succeeded"
	if errText != nil {
		state = "failed"
	}
	b, _ := json.Marshal(response)
	_, err := s.pool.Exec(ctx, `UPDATE model_calls SET state=$2, response_summary=$3, error=$4, completed_at=now() WHERE id=$1`, callID, state, b, errText)
	if err == nil {
		payload := map[string]any{"model_call_id": callID, "state": state}
		_ = s.AddEvent(ctx, runID, "model."+state, payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "model."+state, payload)
	}
	return err
}

func (s *Store) StartToolCall(ctx context.Context, runID, toolName string, args any, retryable bool) (string, error) {
	b, _ := json.Marshal(args)
	argsHash := StableArgsHash(toolName, args)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tool_calls(run_id,tool_name,state,arguments,retryable,args_hash)
		VALUES($1,$2,'running',$3,$4,$5)
		RETURNING id::text`, runID, toolName, b, retryable, argsHash).Scan(&id)
	if err == nil {
		payload := map[string]any{"tool_call_id": id, "tool_name": toolName}
		_ = s.AddEvent(ctx, runID, "tool.started", payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "tool.started", payload)
	}
	return id, err
}

func (s *Store) CompleteToolCall(ctx context.Context, callID, runID string, result any, errText *string) error {
	state := "succeeded"
	if errText != nil {
		state = "failed"
	}
	b, _ := json.Marshal(result)
	_, err := s.pool.Exec(ctx, `UPDATE tool_calls SET state=$2, result=$3, error=$4, completed_at=now() WHERE id=$1`, callID, state, b, errText)
	if err == nil {
		payload := map[string]any{"tool_call_id": callID, "state": state}
		_ = s.AddEvent(ctx, runID, "tool."+state, payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "tool."+state, payload)
	}
	return err
}

func (s *Store) CompletedNonRetryableToolCalls(ctx context.Context, runID string) ([]ToolCallRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, run_id::text, tool_name, state, arguments, result, retryable, args_hash, error
		FROM tool_calls
		WHERE run_id=$1 AND retryable=false AND completed_at IS NOT NULL
		ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCallRecord
	for rows.Next() {
		var rec ToolCallRecord
		if err := rows.Scan(&rec.ID, &rec.RunID, &rec.ToolName, &rec.State, &rec.Arguments, &rec.Result, &rec.Retryable, &rec.ArgsHash, &rec.Error); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) ToolCallCount(ctx context.Context, runID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tool_calls WHERE run_id=$1`, runID).Scan(&count)
	return count, err
}

func StableArgsHash(toolName string, args any) string {
	normalized := normalizeJSON(args)
	b, _ := json.Marshal(normalized)
	sum := sha256.Sum256([]byte(toolName + ":" + string(b)))
	return fmt.Sprintf("%x", sum[:])
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = normalizeJSON(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeJSON(x[i])
		}
		return out
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(x, &decoded); err == nil {
			return normalizeJSON(decoded)
		}
		return string(x)
	default:
		return x
	}
}

func (s *Store) StartMCPCall(ctx context.Context, runID, serverName, toolName string, request any) (string, error) {
	b, _ := json.Marshal(request)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mcp_calls(run_id,server_name,tool_name,state,request_summary)
		VALUES($1,$2,$3,'running',$4)
		RETURNING id::text`, runID, serverName, toolName, b).Scan(&id)
	if err == nil {
		payload := map[string]any{"mcp_call_id": id, "server_name": serverName, "tool_name": toolName}
		_ = s.AddEvent(ctx, runID, "mcp.started", payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "mcp.started", payload)
	}
	return id, err
}

func (s *Store) CompleteMCPCall(ctx context.Context, callID, runID string, response any, errText *string) error {
	state := "succeeded"
	if errText != nil {
		state = "failed"
	}
	b, _ := json.Marshal(response)
	_, err := s.pool.Exec(ctx, `UPDATE mcp_calls SET state=$2, response_summary=$3, error=$4, completed_at=now() WHERE id=$1`, callID, state, b, errText)
	if err == nil {
		payload := map[string]any{"mcp_call_id": callID, "state": state}
		_ = s.AddEvent(ctx, runID, "mcp."+state, payload)
		_ = s.AddObservabilityEvent(ctx, "", runID, "mcp."+state, payload)
	}
	return err
}

func (s *Store) ClaimDueSchedulerJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]SchedulerJob, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT id
			FROM scheduler_jobs
			WHERE enabled=true
			AND next_run_at <= now()
			AND (lease_expires_at IS NULL OR lease_expires_at < now())
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE scheduler_jobs j
		SET lease_owner=$2, lease_expires_at=now()+$3::interval
		FROM candidate
		WHERE j.id=candidate.id
		RETURNING j.id::text, j.customer_id, j.job_type, j.schedule, j.next_run_at, j.payload, j.enabled, j.lease_owner, j.lease_expires_at, j.last_fired_at, j.metadata`,
		limit, owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []SchedulerJob
	for rows.Next() {
		var job SchedulerJob
		if err := rows.Scan(&job.ID, &job.CustomerID, &job.JobType, &job.Schedule, &job.NextRunAt, &job.Payload, &job.Enabled, &job.LeaseOwner, &job.LeaseExpiresAt, &job.LastFiredAt, &job.Metadata); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) CreateSchedulerJob(ctx context.Context, spec SchedulerJobSpec) (*SchedulerJob, error) {
	if err := s.EnsureSession(ctx, ACPContext{
		CustomerID:      spec.CustomerID,
		UserID:          spec.UserID,
		AgentInstanceID: spec.AgentInstanceID,
		SessionID:       spec.SessionID,
	}); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{
		"user_id":           spec.UserID,
		"agent_instance_id": spec.AgentInstanceID,
		"session_id":        spec.SessionID,
		"input":             spec.Input,
		"workflow_wake":     workflowWakeMetadata(spec.Metadata),
	})
	metadata, _ := json.Marshal(spec.Metadata)
	jobType := spec.JobType
	if jobType == "" {
		jobType = "cron"
	}
	var job SchedulerJob
	err := s.pool.QueryRow(ctx, `
		INSERT INTO scheduler_jobs(customer_id,job_type,schedule,next_run_at,payload,metadata)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id::text, customer_id, job_type, schedule, next_run_at, payload, enabled, lease_owner, lease_expires_at, last_fired_at, metadata`,
		spec.CustomerID, jobType, spec.Schedule, spec.NextRunAt, payload, metadata).
		Scan(&job.ID, &job.CustomerID, &job.JobType, &job.Schedule, &job.NextRunAt, &job.Payload, &job.Enabled, &job.LeaseOwner, &job.LeaseExpiresAt, &job.LastFiredAt, &job.Metadata)
	return &job, err
}

func (s *Store) ListSchedulerJobs(ctx context.Context, customerID string, limit int) ([]SchedulerJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, job_type, schedule, next_run_at, payload, enabled, lease_owner, lease_expires_at, last_fired_at, metadata
		FROM scheduler_jobs
		WHERE customer_id=$1
		ORDER BY next_run_at ASC
		LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []SchedulerJob
	for rows.Next() {
		var job SchedulerJob
		if err := rows.Scan(&job.ID, &job.CustomerID, &job.JobType, &job.Schedule, &job.NextRunAt, &job.Payload, &job.Enabled, &job.LeaseOwner, &job.LeaseExpiresAt, &job.LastFiredAt, &job.Metadata); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) SetSchedulerJobEnabled(ctx context.Context, jobID, customerID string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE scheduler_jobs
		SET enabled=$3, lease_owner=NULL, lease_expires_at=NULL
		WHERE id=$1 AND customer_id=$2`, jobID, customerID, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("scheduler job not found")
	}
	return nil
}

func (s *Store) CompleteSchedulerJob(ctx context.Context, jobID string, firedAt, nextRunAt time.Time) error {
	if nextRunAt.IsZero() {
		_, err := s.pool.Exec(ctx, `
			UPDATE scheduler_jobs
			SET last_fired_at=$2, enabled=false, lease_owner=NULL, lease_expires_at=NULL
			WHERE id=$1`, jobID, firedAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE scheduler_jobs
		SET last_fired_at=$2, next_run_at=$3, lease_owner=NULL, lease_expires_at=NULL
		WHERE id=$1`, jobID, firedAt, nextRunAt)
	return err
}

func (s *Store) EnqueueOutbox(ctx context.Context, topic string, payload any, availableAt time.Time) (int64, error) {
	if availableAt.IsZero() {
		availableAt = time.Now().UTC()
	}
	b, _ := json.Marshal(payload)
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO async_outbox(topic,payload,available_at) VALUES($1,$2,$3) RETURNING id`, topic, b, availableAt).Scan(&id)
	return id, err
}

func (s *Store) ClaimOutbox(ctx context.Context, owner string, limit int) ([]OutboxItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidate AS (
			SELECT id
			FROM async_outbox
			WHERE completed_at IS NULL
			AND available_at <= now()
			AND claimed_at IS NULL
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE async_outbox o
		SET claimed_at=now(), claim_owner=$2
		FROM candidate
		WHERE o.id=candidate.id
		RETURNING o.id, o.topic, o.payload, o.available_at, o.claimed_at, o.claim_owner`, limit, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []OutboxItem
	for rows.Next() {
		var item OutboxItem
		if err := rows.Scan(&item.ID, &item.Topic, &item.Payload, &item.AvailableAt, &item.ClaimedAt, &item.ClaimOwner); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ReleaseOutbox(ctx context.Context, id int64, delay time.Duration) error {
	availableAt := time.Now().UTC().Add(delay)
	_, err := s.pool.Exec(ctx, `
		UPDATE async_outbox
		SET claimed_at=NULL, claim_owner=NULL, available_at=$2
		WHERE id=$1 AND completed_at IS NULL`, id, availableAt)
	return err
}

func (s *Store) CompleteOutbox(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE async_outbox SET completed_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) AddObservabilityEvent(ctx context.Context, customerID, runID, eventType string, payload any) error {
	if customerID == "" && runID != "" {
		_ = s.pool.QueryRow(ctx, `SELECT customer_id FROM runs WHERE id=$1`, runID).Scan(&customerID)
	}
	b, _ := json.Marshal(payload)
	_, err := s.pool.Exec(ctx, `INSERT INTO observability_events(customer_id,run_id,event_type,payload) VALUES($1,$2,$3,$4)`, customerID, nullableRunID(runID), eventType, b)
	return err
}

func (s *Store) ListObservabilityEvents(ctx context.Context, customerID, runID string, limit int) ([]ObservabilityEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	runFilter := ""
	if runID != "" {
		args = append(args, runID)
		runFilter = " AND run_id=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, customer_id, run_id::text, event_type, payload, created_at
		FROM observability_events
		WHERE customer_id=$1`+runFilter+`
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ObservabilityEvent
	for rows.Next() {
		var event ObservabilityEvent
		if err := rows.Scan(&event.ID, &event.CustomerID, &event.RunID, &event.EventType, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func nullableRunID(runID string) any {
	if runID == "" {
		return nil
	}
	return runID
}

func workflowWakeMetadata(metadata any) any {
	m, ok := metadata.(map[string]any)
	if !ok {
		return nil
	}
	return m["workflow_wake"]
}
