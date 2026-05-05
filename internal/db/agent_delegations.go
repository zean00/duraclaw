package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const agentDelegationCacheTTL = 30 * time.Second

type AgentDelegationHandle struct {
	CustomerID      string          `json:"customer_id"`
	Handle          string          `json:"handle"`
	AgentInstanceID string          `json:"agent_instance_id"`
	Enabled         bool            `json:"enabled"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AgentDelegationAccessRule struct {
	CustomerID      string          `json:"customer_id"`
	AgentInstanceID string          `json:"agent_instance_id"`
	UserID          string          `json:"user_id,omitempty"`
	AllowedAgents   []string        `json:"allowed_agents"`
	DeniedAgents    []string        `json:"denied_agents"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type EffectiveAgentDelegationAccess struct {
	CustomerID      string   `json:"customer_id"`
	AgentInstanceID string   `json:"agent_instance_id"`
	UserID          string   `json:"user_id,omitempty"`
	AllowedAgents   []string `json:"allowed_agents"`
	DeniedAgents    []string `json:"denied_agents"`
	HasRule         bool     `json:"has_rule"`
	Source          string   `json:"source,omitempty"`
}

type AgentDelegation struct {
	ID                    string          `json:"id"`
	CustomerID            string          `json:"customer_id"`
	UserID                string          `json:"user_id"`
	SourceAgentInstanceID string          `json:"source_agent_instance_id"`
	TargetAgentInstanceID string          `json:"target_agent_instance_id"`
	TargetHandle          string          `json:"target_handle"`
	ParentSessionID       string          `json:"parent_session_id"`
	ParentRunID           string          `json:"parent_run_id"`
	ChildSessionID        string          `json:"child_session_id"`
	ChildRunID            string          `json:"child_run_id"`
	ExactMessage          string          `json:"exact_message"`
	ContextSummary        string          `json:"context_summary"`
	Status                string          `json:"status"`
	ResultText            string          `json:"result_text,omitempty"`
	Error                 *string         `json:"error,omitempty"`
	Metadata              json.RawMessage `json:"metadata,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
	CompletedAt           *time.Time      `json:"completed_at,omitempty"`
}

type AgentDelegationSpec struct {
	CustomerID            string
	UserID                string
	SourceAgentInstanceID string
	TargetAgentInstanceID string
	TargetHandle          string
	ParentSessionID       string
	ParentRunID           string
	ChildSessionID        string
	ChildRunID            string
	ExactMessage          string
	ContextSummary        string
	Metadata              any
}

type agentDelegationAccessCacheEntry struct {
	access    EffectiveAgentDelegationAccess
	expiresAt time.Time
}

func NormalizeAgentHandle(handle string) string {
	handle = strings.TrimSpace(strings.ToLower(handle))
	handle = strings.TrimPrefix(handle, "@")
	handle = strings.ReplaceAll(handle, "_", "-")
	return handle
}

func (s *Store) UpsertAgentDelegationHandle(ctx context.Context, handle AgentDelegationHandle) (*AgentDelegationHandle, error) {
	handle.CustomerID = strings.TrimSpace(handle.CustomerID)
	handle.Handle = NormalizeAgentHandle(handle.Handle)
	handle.AgentInstanceID = strings.TrimSpace(handle.AgentInstanceID)
	if handle.CustomerID == "" || handle.Handle == "" || handle.AgentInstanceID == "" {
		return nil, ValidationError{Message: "customer_id, handle, and agent_instance_id are required"}
	}
	if err := s.ensureCustomer(ctx, handle.CustomerID); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, handle.CustomerID, handle.AgentInstanceID); err != nil {
		return nil, err
	}
	metadata := jsonObject(handle.Metadata)
	var out AgentDelegationHandle
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_delegation_handles(customer_id,handle,agent_instance_id,enabled,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (customer_id,handle) DO UPDATE SET
			agent_instance_id=EXCLUDED.agent_instance_id,
			enabled=EXCLUDED.enabled,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, handle, agent_instance_id, enabled, metadata, updated_at`,
		handle.CustomerID, handle.Handle, handle.AgentInstanceID, handle.Enabled, metadata).
		Scan(&out.CustomerID, &out.Handle, &out.AgentInstanceID, &out.Enabled, &out.Metadata, &out.UpdatedAt)
	return &out, err
}

func (s *Store) AgentDelegationHandle(ctx context.Context, customerID, handle string) (*AgentDelegationHandle, error) {
	customerID = strings.TrimSpace(customerID)
	handle = NormalizeAgentHandle(handle)
	var out AgentDelegationHandle
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, handle, agent_instance_id, enabled, metadata, updated_at
		FROM agent_delegation_handles
		WHERE customer_id=$1 AND handle=$2`, customerID, handle).
		Scan(&out.CustomerID, &out.Handle, &out.AgentInstanceID, &out.Enabled, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListAgentDelegationHandles(ctx context.Context, customerID string, limit int) ([]AgentDelegationHandle, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return nil, ValidationError{Message: "customer_id is required"}
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT customer_id, handle, agent_instance_id, enabled, metadata, updated_at
		FROM agent_delegation_handles
		WHERE customer_id=$1
		ORDER BY handle
		LIMIT $2`, customerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentDelegationHandle
	for rows.Next() {
		var h AgentDelegationHandle
		if err := rows.Scan(&h.CustomerID, &h.Handle, &h.AgentInstanceID, &h.Enabled, &h.Metadata, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgentDelegationHandle(ctx context.Context, customerID, handle string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_delegation_handles WHERE customer_id=$1 AND handle=$2`, strings.TrimSpace(customerID), NormalizeAgentHandle(handle))
	return err
}

func (s *Store) UpsertAgentDelegationAccessRule(ctx context.Context, rule AgentDelegationAccessRule) (*AgentDelegationAccessRule, error) {
	rule.CustomerID = strings.TrimSpace(rule.CustomerID)
	rule.AgentInstanceID = strings.TrimSpace(rule.AgentInstanceID)
	rule.UserID = strings.TrimSpace(rule.UserID)
	if rule.CustomerID == "" || rule.AgentInstanceID == "" {
		return nil, ValidationError{Message: "customer_id and agent_instance_id are required"}
	}
	allowed := normalizedStrings(rule.AllowedAgents)
	denied := normalizedStrings(rule.DeniedAgents)
	allowedJSON, _ := json.Marshal(allowed)
	deniedJSON, _ := json.Marshal(denied)
	metadata := jsonObject(rule.Metadata)
	if err := s.ensureCustomer(ctx, rule.CustomerID); err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, rule.CustomerID, rule.AgentInstanceID); err != nil {
		return nil, err
	}
	if rule.UserID != "" {
		if _, err := s.pool.Exec(ctx, `INSERT INTO users(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, rule.CustomerID, rule.UserID); err != nil {
			return nil, err
		}
	}
	var out AgentDelegationAccessRule
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_delegation_access_rules(customer_id,agent_instance_id,user_id,allowed_agents,denied_agents,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (customer_id,agent_instance_id,user_id) DO UPDATE SET
			allowed_agents=EXCLUDED.allowed_agents,
			denied_agents=EXCLUDED.denied_agents,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, agent_instance_id, user_id, allowed_agents, denied_agents, metadata, updated_at`,
		rule.CustomerID, rule.AgentInstanceID, rule.UserID, allowedJSON, deniedJSON, metadata).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := decodeAgentDelegationAccessLists(allowedJSON, deniedJSON, &out); err != nil {
		return nil, err
	}
	s.invalidateAgentDelegationAccess()
	return &out, nil
}

func (s *Store) AgentDelegationAccessRule(ctx context.Context, customerID, agentInstanceID, userID string) (*AgentDelegationAccessRule, error) {
	var out AgentDelegationAccessRule
	var allowedJSON, deniedJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, user_id, allowed_agents, denied_agents, metadata, updated_at
		FROM agent_delegation_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3`,
		strings.TrimSpace(customerID), strings.TrimSpace(agentInstanceID), strings.TrimSpace(userID)).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := decodeAgentDelegationAccessLists(allowedJSON, deniedJSON, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) DeleteAgentDelegationAccessRule(ctx context.Context, customerID, agentInstanceID, userID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM agent_delegation_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3`,
		strings.TrimSpace(customerID), strings.TrimSpace(agentInstanceID), strings.TrimSpace(userID))
	if err == nil {
		s.invalidateAgentDelegationAccess()
	}
	return err
}

func (s *Store) EffectiveAgentDelegationAccess(ctx context.Context, customerID, agentInstanceID, userID string) (EffectiveAgentDelegationAccess, error) {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	userID = strings.TrimSpace(userID)
	key := strings.Join([]string{customerID, agentInstanceID, userID}, "\x00")
	if cached, ok := s.agentDelegationAccessCache.Load(key); ok {
		entry := cached.(agentDelegationAccessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.access, nil
		}
		s.agentDelegationAccessCache.Delete(key)
	}
	access, err := s.effectiveAgentDelegationAccess(ctx, customerID, agentInstanceID, userID)
	if err != nil {
		return access, err
	}
	s.agentDelegationAccessCache.Store(key, agentDelegationAccessCacheEntry{access: access, expiresAt: time.Now().Add(agentDelegationCacheTTL)})
	return access, nil
}

func (s *Store) AgentDelegationAllowed(ctx context.Context, customerID, sourceAgentInstanceID, userID, targetAgentInstanceID, targetHandle string) error {
	access, err := s.EffectiveAgentDelegationAccess(ctx, customerID, sourceAgentInstanceID, userID)
	if err != nil {
		return err
	}
	if !agentDelegationAllowed(access, targetAgentInstanceID, targetHandle) {
		return fmt.Errorf("agent delegation to %q is not allowed", targetHandle)
	}
	return nil
}

func (s *Store) effectiveAgentDelegationAccess(ctx context.Context, customerID, agentInstanceID, userID string) (EffectiveAgentDelegationAccess, error) {
	base := EffectiveAgentDelegationAccess{CustomerID: customerID, AgentInstanceID: agentInstanceID, UserID: userID}
	if userID != "" {
		rule, err := s.AgentDelegationAccessRule(ctx, customerID, agentInstanceID, userID)
		if err == nil {
			return rule.effective("user"), nil
		}
		if err != nil && err != pgx.ErrNoRows {
			return base, err
		}
	}
	rule, err := s.AgentDelegationAccessRule(ctx, customerID, agentInstanceID, "")
	if err == nil {
		return rule.effective("agent_instance"), nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return base, err
	}
	return base, nil
}

func agentDelegationAllowed(access EffectiveAgentDelegationAccess, targetAgentInstanceID, targetHandle string) bool {
	targetAgentInstanceID = strings.TrimSpace(targetAgentInstanceID)
	targetHandle = NormalizeAgentHandle(targetHandle)
	denied := normalizedAgentDelegationSet(access.DeniedAgents)
	if denied[targetAgentInstanceID] || denied[targetHandle] || denied["@"+targetHandle] {
		return false
	}
	allowed := normalizedAgentDelegationSet(access.AllowedAgents)
	return len(allowed) == 0 || allowed[targetAgentInstanceID] || allowed[targetHandle] || allowed["@"+targetHandle]
}

func (r AgentDelegationAccessRule) effective(source string) EffectiveAgentDelegationAccess {
	return EffectiveAgentDelegationAccess{
		CustomerID: r.CustomerID, AgentInstanceID: r.AgentInstanceID, UserID: r.UserID,
		AllowedAgents: normalizedStrings(r.AllowedAgents), DeniedAgents: normalizedStrings(r.DeniedAgents), HasRule: true, Source: source,
	}
}

func (s *Store) invalidateAgentDelegationAccess() {
	s.agentDelegationAccessCache.Range(func(key, _ any) bool {
		s.agentDelegationAccessCache.Delete(key)
		return true
	})
}

func decodeAgentDelegationAccessLists(allowedJSON, deniedJSON []byte, out *AgentDelegationAccessRule) error {
	if err := json.Unmarshal(allowedJSON, &out.AllowedAgents); err != nil {
		return fmt.Errorf("decode allowed agents: %w", err)
	}
	if err := json.Unmarshal(deniedJSON, &out.DeniedAgents); err != nil {
		return fmt.Errorf("decode denied agents: %w", err)
	}
	out.AllowedAgents = normalizedStrings(out.AllowedAgents)
	out.DeniedAgents = normalizedStrings(out.DeniedAgents)
	return nil
}

func normalizedAgentDelegationSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = true
		handle := NormalizeAgentHandle(value)
		if handle != "" {
			out[handle] = true
			out["@"+handle] = true
		}
	}
	return out
}

func (s *Store) CreateAgentDelegation(ctx context.Context, spec AgentDelegationSpec) (*AgentDelegation, error) {
	if spec.CustomerID == "" || spec.UserID == "" || spec.SourceAgentInstanceID == "" || spec.TargetAgentInstanceID == "" ||
		spec.TargetHandle == "" || spec.ParentSessionID == "" || spec.ParentRunID == "" || spec.ChildSessionID == "" || spec.ChildRunID == "" {
		return nil, ValidationError{Message: "customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id, child_session_id, and child_run_id are required"}
	}
	metadata := jsonObject(spec.Metadata)
	var out AgentDelegation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_delegations(customer_id,user_id,source_agent_instance_id,target_agent_instance_id,target_handle,parent_session_id,parent_run_id,child_session_id,child_run_id,exact_message,context_summary,status,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'queued',$12)
		RETURNING id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at`,
		spec.CustomerID, spec.UserID, spec.SourceAgentInstanceID, spec.TargetAgentInstanceID, NormalizeAgentHandle(spec.TargetHandle), spec.ParentSessionID, spec.ParentRunID, spec.ChildSessionID, spec.ChildRunID, spec.ExactMessage, spec.ContextSummary, metadata).
		Scan(&out.ID, &out.CustomerID, &out.UserID, &out.SourceAgentInstanceID, &out.TargetAgentInstanceID, &out.TargetHandle, &out.ParentSessionID, &out.ParentRunID, &out.ChildSessionID, &out.ChildRunID, &out.ExactMessage, &out.ContextSummary, &out.Status, &out.ResultText, &out.Error, &out.Metadata, &out.CreatedAt, &out.UpdatedAt, &out.CompletedAt)
	return &out, err
}

func (s *Store) CreateAgentDelegationRun(ctx context.Context, c ACPContext, input any, progress any, spec AgentDelegationSpec) (*Run, *AgentDelegation, error) {
	spec.CustomerID = c.CustomerID
	spec.UserID = c.UserID
	spec.TargetAgentInstanceID = c.AgentInstanceID
	spec.ChildSessionID = c.SessionID
	if spec.CustomerID == "" || spec.UserID == "" || spec.SourceAgentInstanceID == "" || spec.TargetAgentInstanceID == "" ||
		spec.TargetHandle == "" || spec.ParentSessionID == "" || spec.ParentRunID == "" || spec.ChildSessionID == "" {
		return nil, nil, ValidationError{Message: "customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id, and child_session_id are required"}
	}
	var outRun *Run
	var outDelegation *AgentDelegation
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
		if effectiveAgentInstanceID != c.AgentInstanceID {
			return fmt.Errorf("delegation child session is assigned to %q, not %q", effectiveAgentInstanceID, c.AgentInstanceID)
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
			outRun = &existing
			var delegation AgentDelegation
			outDelegation = &delegation
			return tx.QueryRow(ctx, `
				SELECT id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at
				FROM agent_delegations
				WHERE child_run_id=$1`, existing.ID).
				Scan(&outDelegation.ID, &outDelegation.CustomerID, &outDelegation.UserID, &outDelegation.SourceAgentInstanceID, &outDelegation.TargetAgentInstanceID, &outDelegation.TargetHandle, &outDelegation.ParentSessionID, &outDelegation.ParentRunID, &outDelegation.ChildSessionID, &outDelegation.ChildRunID, &outDelegation.ExactMessage, &outDelegation.ContextSummary, &outDelegation.Status, &outDelegation.ResultText, &outDelegation.Error, &outDelegation.Metadata, &outDelegation.CreatedAt, &outDelegation.UpdatedAt, &outDelegation.CompletedAt)
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
		if err := addEventTx(ctx, tx, run.ID, "run.queued", map[string]any{"state": run.State, "run_mode": "background", "source": "agent_delegation"}); err != nil {
			return err
		}
		metadata := jsonObject(spec.Metadata)
		var delegation AgentDelegation
		if err := tx.QueryRow(ctx, `
			INSERT INTO agent_delegations(customer_id,user_id,source_agent_instance_id,target_agent_instance_id,target_handle,parent_session_id,parent_run_id,child_session_id,child_run_id,exact_message,context_summary,status,metadata)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'queued',$12)
			RETURNING id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at`,
			spec.CustomerID, spec.UserID, spec.SourceAgentInstanceID, spec.TargetAgentInstanceID, NormalizeAgentHandle(spec.TargetHandle), spec.ParentSessionID, spec.ParentRunID, spec.ChildSessionID, run.ID, spec.ExactMessage, spec.ContextSummary, metadata).
			Scan(&delegation.ID, &delegation.CustomerID, &delegation.UserID, &delegation.SourceAgentInstanceID, &delegation.TargetAgentInstanceID, &delegation.TargetHandle, &delegation.ParentSessionID, &delegation.ParentRunID, &delegation.ChildSessionID, &delegation.ChildRunID, &delegation.ExactMessage, &delegation.ContextSummary, &delegation.Status, &delegation.ResultText, &delegation.Error, &delegation.Metadata, &delegation.CreatedAt, &delegation.UpdatedAt, &delegation.CompletedAt); err != nil {
			return err
		}
		outRun = &run
		outDelegation = &delegation
		return nil
	})
	if err != nil {
		if IsQuotaExceeded(err) {
			_ = s.AddObservabilityEvent(ctx, c.CustomerID, "", "quota_exceeded", map[string]any{"error": err.Error(), "agent_instance_id": c.AgentInstanceID, "run_mode": "background", "source": "agent_delegation"})
		}
		return nil, nil, err
	}
	return outRun, outDelegation, nil
}

func (s *Store) AgentDelegation(ctx context.Context, customerID, delegationID string) (*AgentDelegation, error) {
	var out AgentDelegation
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at
		FROM agent_delegations
		WHERE customer_id=$1 AND id=$2`, strings.TrimSpace(customerID), strings.TrimSpace(delegationID)).
		Scan(&out.ID, &out.CustomerID, &out.UserID, &out.SourceAgentInstanceID, &out.TargetAgentInstanceID, &out.TargetHandle, &out.ParentSessionID, &out.ParentRunID, &out.ChildSessionID, &out.ChildRunID, &out.ExactMessage, &out.ContextSummary, &out.Status, &out.ResultText, &out.Error, &out.Metadata, &out.CreatedAt, &out.UpdatedAt, &out.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) AgentDelegationByChildRun(ctx context.Context, childRunID string) (*AgentDelegation, error) {
	var out AgentDelegation
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, user_id, source_agent_instance_id, target_agent_instance_id, target_handle, parent_session_id, parent_run_id::text, child_session_id, child_run_id::text, exact_message, context_summary, status, result_text, error, metadata, created_at, updated_at, completed_at
		FROM agent_delegations
		WHERE child_run_id=$1`, strings.TrimSpace(childRunID)).
		Scan(&out.ID, &out.CustomerID, &out.UserID, &out.SourceAgentInstanceID, &out.TargetAgentInstanceID, &out.TargetHandle, &out.ParentSessionID, &out.ParentRunID, &out.ChildSessionID, &out.ChildRunID, &out.ExactMessage, &out.ContextSummary, &out.Status, &out.ResultText, &out.Error, &out.Metadata, &out.CreatedAt, &out.UpdatedAt, &out.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) CompleteAgentDelegation(ctx context.Context, delegationID, resultText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_delegations
		SET status='completed', result_text=$2, error=NULL, completed_at=now(), updated_at=now()
		WHERE id=$1`, strings.TrimSpace(delegationID), resultText)
	return err
}

func (s *Store) FailAgentDelegation(ctx context.Context, delegationID, errorText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_delegations
		SET status='failed', error=$2, completed_at=now(), updated_at=now()
		WHERE id=$1`, strings.TrimSpace(delegationID), errorText)
	return err
}

func (s *Store) CancelAgentDelegation(ctx context.Context, delegationID, errorText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE agent_delegations
		SET status='cancelled', error=$2, completed_at=now(), updated_at=now()
		WHERE id=$1`, strings.TrimSpace(delegationID), errorText)
	return err
}

func (s *Store) AgentDelegationArtifactsForRun(ctx context.Context, parentRunID string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, target_handle, target_agent_instance_id, child_session_id, child_run_id::text, status
		FROM agent_delegations
		WHERE parent_run_id=$1
		ORDER BY created_at ASC`, strings.TrimSpace(parentRunID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, handle, agentID, sessionID, runID, status string
		if err := rows.Scan(&id, &handle, &agentID, &sessionID, &runID, &status); err != nil {
			return nil, err
		}
		out = append(out, AgentDelegationArtifact(AgentDelegation{ID: id, TargetHandle: handle, TargetAgentInstanceID: agentID, ChildSessionID: sessionID, ChildRunID: runID, Status: status}))
	}
	return out, rows.Err()
}

func AgentDelegationArtifact(d AgentDelegation) map[string]any {
	return map[string]any{
		"type":                     "agent_delegation_reference",
		"id":                       d.ID,
		"delegation_id":            d.ID,
		"target_handle":            d.TargetHandle,
		"target_agent_instance_id": d.TargetAgentInstanceID,
		"session_id":               d.ChildSessionID,
		"run_id":                   d.ChildRunID,
		"status":                   d.Status,
		"routes": map[string]any{
			"status": "/acp/agent-delegations/" + d.ID,
		},
	}
}
