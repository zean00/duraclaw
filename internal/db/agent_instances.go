package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type AgentInstanceVersion struct {
	ID                 string          `json:"id"`
	CustomerID         string          `json:"customer_id"`
	AgentInstanceID    string          `json:"agent_instance_id"`
	Version            int             `json:"version"`
	Name               string          `json:"name"`
	ModelConfig        json.RawMessage `json:"model_config"`
	SystemInstructions string          `json:"system_instructions"`
	ToolConfig         json.RawMessage `json:"tool_config"`
	MCPConfig          json.RawMessage `json:"mcp_config"`
	WorkflowConfig     json.RawMessage `json:"workflow_config"`
	PolicyConfig       json.RawMessage `json:"policy_config"`
	Metadata           json.RawMessage `json:"metadata"`
	CreatedAt          time.Time       `json:"created_at"`
	ActivatedAt        *time.Time      `json:"activated_at,omitempty"`
}

type AgentInstanceVersionSpec struct {
	CustomerID          string
	AgentInstanceID     string
	Version             int
	Name                string
	ModelConfig         any
	SystemInstructions  string
	ToolConfig          any
	MCPConfig           any
	WorkflowConfig      any
	PolicyConfig        any
	Metadata            any
	ActivateImmediately bool
}

func (s *Store) CreateAgentInstanceVersion(ctx context.Context, spec AgentInstanceVersionSpec) (*AgentInstanceVersion, error) {
	if spec.CustomerID == "" || spec.AgentInstanceID == "" {
		return nil, fmt.Errorf("customer_id and agent_instance_id are required")
	}
	var out AgentInstanceVersion
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO customers(id) VALUES($1) ON CONFLICT DO NOTHING`, spec.CustomerID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, spec.CustomerID, spec.AgentInstanceID); err != nil {
			return err
		}
		version := spec.Version
		if version <= 0 {
			if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM agent_instance_versions WHERE customer_id=$1 AND agent_instance_id=$2`, spec.CustomerID, spec.AgentInstanceID).Scan(&version); err != nil {
				return err
			}
		}
		modelConfig := jsonObject(spec.ModelConfig)
		toolConfig := jsonObject(spec.ToolConfig)
		mcpConfig := jsonObject(spec.MCPConfig)
		workflowConfig := jsonObject(spec.WorkflowConfig)
		policyConfig := jsonObject(spec.PolicyConfig)
		metadata := jsonObject(spec.Metadata)
		err := tx.QueryRow(ctx, `
			INSERT INTO agent_instance_versions(customer_id,agent_instance_id,version,name,model_config,system_instructions,tool_config,mcp_config,workflow_config,policy_config,metadata,activated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, CASE WHEN $12 THEN now() ELSE NULL END)
			RETURNING id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, metadata, created_at, activated_at`,
			spec.CustomerID, spec.AgentInstanceID, version, spec.Name, modelConfig, spec.SystemInstructions, toolConfig, mcpConfig, workflowConfig, policyConfig, metadata, spec.ActivateImmediately).
			Scan(&out.ID, &out.CustomerID, &out.AgentInstanceID, &out.Version, &out.Name, &out.ModelConfig, &out.SystemInstructions, &out.ToolConfig, &out.MCPConfig, &out.WorkflowConfig, &out.PolicyConfig, &out.Metadata, &out.CreatedAt, &out.ActivatedAt)
		if err != nil {
			return err
		}
		if spec.ActivateImmediately {
			_, err = tx.Exec(ctx, `UPDATE agent_instances SET current_version_id=$3 WHERE customer_id=$1 AND id=$2`, spec.CustomerID, spec.AgentInstanceID, out.ID)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListAgentInstanceVersions(ctx context.Context, customerID, agentInstanceID string, limit int) ([]AgentInstanceVersion, error) {
	if customerID == "" || agentInstanceID == "" {
		return nil, fmt.Errorf("customer_id and agent_instance_id are required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, metadata, created_at, activated_at
		FROM agent_instance_versions
		WHERE customer_id=$1 AND agent_instance_id=$2
		ORDER BY version DESC
		LIMIT $3`, customerID, agentInstanceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentInstanceVersion
	for rows.Next() {
		var v AgentInstanceVersion
		if err := rows.Scan(&v.ID, &v.CustomerID, &v.AgentInstanceID, &v.Version, &v.Name, &v.ModelConfig, &v.SystemInstructions, &v.ToolConfig, &v.MCPConfig, &v.WorkflowConfig, &v.PolicyConfig, &v.Metadata, &v.CreatedAt, &v.ActivatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ActivateAgentInstanceVersion(ctx context.Context, customerID, agentInstanceID, versionID string) (*AgentInstanceVersion, error) {
	if customerID == "" || agentInstanceID == "" || versionID == "" {
		return nil, fmt.Errorf("customer_id, agent_instance_id, and version_id are required")
	}
	var out AgentInstanceVersion
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			UPDATE agent_instance_versions
			SET activated_at=now()
			WHERE id=$1 AND customer_id=$2 AND agent_instance_id=$3
			RETURNING id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, metadata, created_at, activated_at`,
			versionID, customerID, agentInstanceID).
			Scan(&out.ID, &out.CustomerID, &out.AgentInstanceID, &out.Version, &out.Name, &out.ModelConfig, &out.SystemInstructions, &out.ToolConfig, &out.MCPConfig, &out.WorkflowConfig, &out.PolicyConfig, &out.Metadata, &out.CreatedAt, &out.ActivatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_instances SET current_version_id=$3 WHERE customer_id=$1 AND id=$2`, customerID, agentInstanceID, versionID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) AgentInstanceVersion(ctx context.Context, versionID string) (*AgentInstanceVersion, error) {
	if versionID == "" {
		return nil, nil
	}
	var v AgentInstanceVersion
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, metadata, created_at, activated_at
		FROM agent_instance_versions
		WHERE id=$1`, versionID).
		Scan(&v.ID, &v.CustomerID, &v.AgentInstanceID, &v.Version, &v.Name, &v.ModelConfig, &v.SystemInstructions, &v.ToolConfig, &v.MCPConfig, &v.WorkflowConfig, &v.PolicyConfig, &v.Metadata, &v.CreatedAt, &v.ActivatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *Store) currentAgentInstanceVersionID(ctx context.Context, customerID, agentInstanceID string) (string, error) {
	var versionID string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(current_version_id::text,'')
		FROM agent_instances
		WHERE customer_id=$1 AND id=$2`, customerID, agentInstanceID).Scan(&versionID)
	return versionID, err
}

func ensureDefaultAgentInstanceVersion(ctx context.Context, tx pgx.Tx, customerID, agentInstanceID string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_instance_versions(customer_id,agent_instance_id,version,name,metadata,activated_at)
		VALUES($1,$2,1,'Initial version','{"source":"ensure_session"}'::jsonb,now())
		ON CONFLICT (customer_id, agent_instance_id, version) DO NOTHING`, customerID, agentInstanceID); err != nil {
		return err
	}
	var versionID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM agent_instance_versions
		WHERE customer_id=$1 AND agent_instance_id=$2 AND version=1`, customerID, agentInstanceID).Scan(&versionID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE agent_instances
		SET current_version_id=COALESCE(current_version_id,$3)
		WHERE customer_id=$1 AND id=$2`, customerID, agentInstanceID, versionID)
	return err
}

func jsonObject(v any) []byte {
	b, _ := json.Marshal(v)
	if len(b) == 0 || string(b) == "null" {
		return []byte(`{}`)
	}
	return b
}
