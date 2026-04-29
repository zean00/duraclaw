package db

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
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
	if err := validateAgentInstanceVersionSpec(spec); err != nil {
		return nil, err
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

func validateAgentInstanceVersionSpec(spec AgentInstanceVersionSpec) error {
	if err := validateObjectConfig("model_config", spec.ModelConfig, []string{"primary", "model", "fallbacks"}); err != nil {
		return err
	}
	if err := validateObjectConfig("tool_config", spec.ToolConfig, []string{"allowed_tools", "disabled_tools", "max_iterations", "max_tool_calls_per_run"}); err != nil {
		return err
	}
	if err := validateToolConfigValues(spec.ToolConfig); err != nil {
		return err
	}
	if err := validateObjectConfig("workflow_config", spec.WorkflowConfig, []string{"allowed_workflows", "disabled_workflows"}); err != nil {
		return err
	}
	if err := validateObjectConfig("policy_config", spec.PolicyConfig, []string{"instructions", "blocked_terms", "policy_pack_ids"}); err != nil {
		return err
	}
	if err := validatePolicyConfigValues(spec.PolicyConfig); err != nil {
		return err
	}
	if err := validateObjectConfig("mcp_config", spec.MCPConfig, []string{"servers"}); err != nil {
		return err
	}
	return validateMCPConfigValues(spec.MCPConfig)
}

func ValidateAgentInstanceVersionSpecForTest(spec AgentInstanceVersionSpec) error {
	return validateAgentInstanceVersionSpec(spec)
}

func validateObjectConfig(name string, value any, allowed []string) error {
	b := jsonObject(value)
	if string(b) == "{}" {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", name, err)
	}
	allowedSet := map[string]bool{}
	for _, key := range allowed {
		allowedSet[key] = true
	}
	for key := range obj {
		if !allowedSet[key] {
			return fmt.Errorf("%s contains unsupported key %q", name, key)
		}
	}
	return nil
}

func decodeConfigObject(name string, value any) (map[string]any, error) {
	b := jsonObject(value)
	if string(b) == "{}" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", name, err)
	}
	return obj, nil
}

func validatePolicyConfigValues(value any) error {
	obj, err := decodeConfigObject("policy_config", value)
	if err != nil || obj == nil {
		return err
	}
	for _, key := range []string{"instructions", "blocked_terms", "policy_pack_ids"} {
		if raw, ok := obj[key]; ok {
			if err := validateStringArray("policy_config."+key, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateToolConfigValues(value any) error {
	obj, err := decodeConfigObject("tool_config", value)
	if err != nil || obj == nil {
		return err
	}
	for _, key := range []string{"allowed_tools", "disabled_tools"} {
		if raw, ok := obj[key]; ok {
			if err := validateStringArray("tool_config."+key, raw); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"max_iterations", "max_tool_calls_per_run"} {
		if raw, ok := obj[key]; ok {
			if err := validateNonNegativeInteger("tool_config."+key, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMCPConfigValues(value any) error {
	obj, err := decodeConfigObject("mcp_config", value)
	if err != nil || obj == nil {
		return err
	}
	rawServers, ok := obj["servers"]
	if !ok {
		return nil
	}
	servers, ok := rawServers.([]any)
	if !ok {
		return fmt.Errorf("mcp_config.servers must be an array")
	}
	for i, raw := range servers {
		server, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("mcp_config.servers[%d] must be an object", i)
		}
		name, _ := server["name"].(string)
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("mcp_config.servers[%d].name is required", i)
		}
		transport, _ := server["transport"].(string)
		transport = strings.TrimSpace(transport)
		if transport == "" {
			transport = "http"
		}
		switch transport {
		case "http", "sse":
			baseURL, _ := server["base_url"].(string)
			if strings.TrimSpace(baseURL) == "" {
				return fmt.Errorf("mcp_config.servers[%d].base_url is required for %s transport", i, transport)
			}
		case "stdio":
			command, _ := server["command"].(string)
			if strings.TrimSpace(command) == "" {
				return fmt.Errorf("mcp_config.servers[%d].command is required for stdio transport", i)
			}
		default:
			return fmt.Errorf("mcp_config.servers[%d].transport %q is unsupported", i, transport)
		}
		for _, key := range []string{"max_retries", "retry_delay_ms", "max_concurrent"} {
			if raw, ok := server[key]; ok {
				if err := validateNonNegativeInteger(fmt.Sprintf("mcp_config.servers[%d].%s", i, key), raw); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateStringArray(name string, raw any) error {
	values, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array of strings", name)
	}
	for i, value := range values {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s[%d] must be a string", name, i)
		}
	}
	return nil
}

func validateNonNegativeInteger(name string, raw any) error {
	value, ok := raw.(float64)
	if !ok || math.Trunc(value) != value || value < 0 {
		return fmt.Errorf("%s must be a non-negative integer", name)
	}
	return nil
}
