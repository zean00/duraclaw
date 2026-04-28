package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type AgentPolicy struct {
	CustomerID           string   `json:"customer_id"`
	AgentInstanceID      string   `json:"agent_instance_id"`
	ArtifactMaxSizeBytes int64    `json:"artifact_max_size_bytes"`
	ArtifactMediaTypes   []string `json:"artifact_media_types"`
}

type PolicyPack struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Version    int       `json:"version"`
	Status     string    `json:"status"`
	OwnerScope string    `json:"owner_scope"`
	CreatedAt  time.Time `json:"created_at"`
}

type PolicyRule struct {
	ID              string          `json:"id"`
	PolicyPackID    string          `json:"policy_pack_id"`
	RuleType        string          `json:"rule_type"`
	EnforcementMode string          `json:"enforcement_mode"`
	Priority        int             `json:"priority"`
	Condition       json.RawMessage `json:"condition"`
	Action          string          `json:"action"`
	InstructionText string          `json:"instruction_text"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"created_at"`
}

type PolicyAssignment struct {
	ID              string    `json:"id"`
	PolicyPackID    string    `json:"policy_pack_id"`
	CustomerID      string    `json:"customer_id"`
	AgentInstanceID string    `json:"agent_instance_id"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
}

type PolicyEvaluation struct {
	ID              int64           `json:"id"`
	RunID           *string         `json:"run_id,omitempty"`
	StepID          *string         `json:"step_id,omitempty"`
	WorkflowRunID   *string         `json:"workflow_run_id,omitempty"`
	WorkflowNodeKey string          `json:"workflow_node_key"`
	PolicyPackID    *string         `json:"policy_pack_id,omitempty"`
	PolicyRuleID    *string         `json:"policy_rule_id,omitempty"`
	EnforcementMode string          `json:"enforcement_mode"`
	Decision        string          `json:"decision"`
	Reason          string          `json:"reason"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

func (s *Store) UpsertAgentPolicy(ctx context.Context, p AgentPolicy) error {
	mediaTypes, _ := json.Marshal(p.ArtifactMediaTypes)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_policies(customer_id,agent_instance_id,artifact_max_size_bytes,artifact_media_types)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (customer_id, agent_instance_id)
		DO UPDATE SET artifact_max_size_bytes=EXCLUDED.artifact_max_size_bytes, artifact_media_types=EXCLUDED.artifact_media_types, updated_at=now()`,
		p.CustomerID, p.AgentInstanceID, p.ArtifactMaxSizeBytes, mediaTypes)
	return err
}

func (s *Store) AgentPolicy(ctx context.Context, customerID, agentInstanceID string) (*AgentPolicy, error) {
	var p AgentPolicy
	var mediaTypes []byte
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, artifact_max_size_bytes, artifact_media_types
		FROM agent_policies
		WHERE customer_id=$1 AND agent_instance_id=$2`, customerID, agentInstanceID).
		Scan(&p.CustomerID, &p.AgentInstanceID, &p.ArtifactMaxSizeBytes, &mediaTypes)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(mediaTypes, &p.ArtifactMediaTypes)
	return &p, nil
}

func (s *Store) CreatePolicyPack(ctx context.Context, name string, version int, ownerScope string) (string, error) {
	if name == "" || version <= 0 {
		return "", fmt.Errorf("name and positive version are required")
	}
	if ownerScope == "" {
		ownerScope = "customer"
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO policy_packs(name,version,owner_scope)
		VALUES($1,$2,$3)
		ON CONFLICT (name, version) DO UPDATE SET owner_scope=EXCLUDED.owner_scope
		RETURNING id::text`, name, version, ownerScope).Scan(&id)
	return id, err
}

func (s *Store) ListPolicyPacks(ctx context.Context, limit int) ([]PolicyPack, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, version, status, owner_scope, created_at
		FROM policy_packs
		ORDER BY name, version DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyPack
	for rows.Next() {
		var pack PolicyPack
		if err := rows.Scan(&pack.ID, &pack.Name, &pack.Version, &pack.Status, &pack.OwnerScope, &pack.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, pack)
	}
	return out, rows.Err()
}

func (s *Store) UpsertPolicyRule(ctx context.Context, rule PolicyRule) (string, error) {
	if rule.PolicyPackID == "" || rule.RuleType == "" || rule.EnforcementMode == "" || rule.Action == "" {
		return "", fmt.Errorf("policy_pack_id, rule_type, enforcement_mode, and action are required")
	}
	if rule.Status == "" {
		rule.Status = "active"
	}
	if len(rule.Condition) == 0 {
		rule.Condition = json.RawMessage(`{}`)
	}
	var id string
	if rule.ID == "" {
		err := s.pool.QueryRow(ctx, `
			INSERT INTO policy_rules(policy_pack_id,rule_type,enforcement_mode,priority,condition,action,instruction_text,status)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id::text`,
			rule.PolicyPackID, rule.RuleType, rule.EnforcementMode, rule.Priority, rule.Condition, rule.Action, rule.InstructionText, rule.Status).Scan(&id)
		return id, err
	}
	err := s.pool.QueryRow(ctx, `
		UPDATE policy_rules
		SET rule_type=$3, enforcement_mode=$4, priority=$5, condition=$6, action=$7, instruction_text=$8, status=$9
		WHERE id=$1 AND policy_pack_id=$2
		RETURNING id::text`,
		rule.ID, rule.PolicyPackID, rule.RuleType, rule.EnforcementMode, rule.Priority, rule.Condition, rule.Action, rule.InstructionText, rule.Status).Scan(&id)
	return id, err
}

func (s *Store) ListPolicyRules(ctx context.Context, policyPackID string) ([]PolicyRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, policy_pack_id::text, rule_type, enforcement_mode, priority, condition, action, instruction_text, status, created_at
		FROM policy_rules
		WHERE policy_pack_id=$1
		ORDER BY enforcement_mode, priority DESC, created_at ASC`, policyPackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRule
	for rows.Next() {
		var rule PolicyRule
		if err := rows.Scan(&rule.ID, &rule.PolicyPackID, &rule.RuleType, &rule.EnforcementMode, &rule.Priority, &rule.Condition, &rule.Action, &rule.InstructionText, &rule.Status, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Store) AssignPolicyPack(ctx context.Context, policyPackID, customerID, agentInstanceID string, enabled bool) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO policy_assignments(policy_pack_id,customer_id,agent_instance_id,enabled)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (policy_pack_id, customer_id, agent_instance_id)
		DO UPDATE SET enabled=EXCLUDED.enabled
		RETURNING id::text`, policyPackID, customerID, agentInstanceID, enabled).Scan(&id)
	return id, err
}

func (s *Store) PolicyRulesForScope(ctx context.Context, customerID, agentInstanceID, enforcementMode string) ([]PolicyRule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id::text, r.policy_pack_id::text, r.rule_type, r.enforcement_mode, r.priority, r.condition, r.action, r.instruction_text, r.status, r.created_at
		FROM policy_rules r
		JOIN policy_packs p ON p.id=r.policy_pack_id
		JOIN policy_assignments a ON a.policy_pack_id=p.id
		WHERE p.status='active'
		AND r.status='active'
		AND a.enabled=true
		AND r.enforcement_mode=$3
		AND (
			(a.customer_id='' AND a.agent_instance_id='')
			OR (a.customer_id=$1 AND a.agent_instance_id='')
			OR (a.customer_id=$1 AND a.agent_instance_id=$2)
		)
		ORDER BY
			CASE
				WHEN a.customer_id='' AND a.agent_instance_id='' THEN 1
				WHEN a.customer_id=$1 AND a.agent_instance_id='' THEN 2
				ELSE 3
			END,
			r.priority DESC,
			r.created_at ASC`, customerID, agentInstanceID, enforcementMode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyRule
	for rows.Next() {
		var rule PolicyRule
		if err := rows.Scan(&rule.ID, &rule.PolicyPackID, &rule.RuleType, &rule.EnforcementMode, &rule.Priority, &rule.Condition, &rule.Action, &rule.InstructionText, &rule.Status, &rule.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *Store) RecordPolicyEvaluation(ctx context.Context, ev PolicyEvaluation) error {
	b := ev.Payload
	if len(b) == 0 {
		b = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO policy_evaluations(run_id,step_id,workflow_run_id,workflow_node_key,policy_pack_id,policy_rule_id,enforcement_mode,decision,reason,payload)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		nullableStringPtr(ev.RunID), nullableStringPtr(ev.StepID), nullableStringPtr(ev.WorkflowRunID), ev.WorkflowNodeKey,
		nullableStringPtr(ev.PolicyPackID), nullableStringPtr(ev.PolicyRuleID), ev.EnforcementMode, ev.Decision, ev.Reason, b)
	return err
}

func (s *Store) ListPolicyEvaluations(ctx context.Context, runID string, limit int) ([]PolicyEvaluation, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{limit}
	filter := ""
	if runID != "" {
		args = append(args, runID)
		filter = "WHERE run_id=$2"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id::text, step_id::text, workflow_run_id::text, workflow_node_key, policy_pack_id::text, policy_rule_id::text, enforcement_mode, decision, reason, payload, created_at
		FROM policy_evaluations
		`+filter+`
		ORDER BY created_at DESC, id DESC
		LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PolicyEvaluation
	for rows.Next() {
		var ev PolicyEvaluation
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.StepID, &ev.WorkflowRunID, &ev.WorkflowNodeKey, &ev.PolicyPackID, &ev.PolicyRuleID, &ev.EnforcementMode, &ev.Decision, &ev.Reason, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func nullableStringPtr(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}
