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
	ProfileConfig      json.RawMessage `json:"profile_config"`
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
	ProfileConfig       any
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
		profileConfig := jsonObject(spec.ProfileConfig)
		metadata := jsonObject(spec.Metadata)
		err := tx.QueryRow(ctx, `
			INSERT INTO agent_instance_versions(customer_id,agent_instance_id,version,name,model_config,system_instructions,tool_config,mcp_config,workflow_config,policy_config,profile_config,metadata,activated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12, CASE WHEN $13 THEN now() ELSE NULL END)
			RETURNING id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, profile_config, metadata, created_at, activated_at`,
			spec.CustomerID, spec.AgentInstanceID, version, spec.Name, modelConfig, spec.SystemInstructions, toolConfig, mcpConfig, workflowConfig, policyConfig, profileConfig, metadata, spec.ActivateImmediately).
			Scan(&out.ID, &out.CustomerID, &out.AgentInstanceID, &out.Version, &out.Name, &out.ModelConfig, &out.SystemInstructions, &out.ToolConfig, &out.MCPConfig, &out.WorkflowConfig, &out.PolicyConfig, &out.ProfileConfig, &out.Metadata, &out.CreatedAt, &out.ActivatedAt)
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
		SELECT id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, profile_config, metadata, created_at, activated_at
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
		if err := rows.Scan(&v.ID, &v.CustomerID, &v.AgentInstanceID, &v.Version, &v.Name, &v.ModelConfig, &v.SystemInstructions, &v.ToolConfig, &v.MCPConfig, &v.WorkflowConfig, &v.PolicyConfig, &v.ProfileConfig, &v.Metadata, &v.CreatedAt, &v.ActivatedAt); err != nil {
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
			RETURNING id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, profile_config, metadata, created_at, activated_at`,
			versionID, customerID, agentInstanceID).
			Scan(&out.ID, &out.CustomerID, &out.AgentInstanceID, &out.Version, &out.Name, &out.ModelConfig, &out.SystemInstructions, &out.ToolConfig, &out.MCPConfig, &out.WorkflowConfig, &out.PolicyConfig, &out.ProfileConfig, &out.Metadata, &out.CreatedAt, &out.ActivatedAt)
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
		SELECT id::text, customer_id, agent_instance_id, version, name, model_config, system_instructions, tool_config, mcp_config, workflow_config, policy_config, profile_config, metadata, created_at, activated_at
		FROM agent_instance_versions
		WHERE id=$1`, versionID).
		Scan(&v.ID, &v.CustomerID, &v.AgentInstanceID, &v.Version, &v.Name, &v.ModelConfig, &v.SystemInstructions, &v.ToolConfig, &v.MCPConfig, &v.WorkflowConfig, &v.PolicyConfig, &v.ProfileConfig, &v.Metadata, &v.CreatedAt, &v.ActivatedAt)
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
	if err := validateObjectConfig("model_config", spec.ModelConfig, []string{"primary", "model", "fallbacks", "options"}); err != nil {
		return err
	}
	if err := validateModelConfigValues(spec.ModelConfig); err != nil {
		return err
	}
	if err := validateObjectConfig("tool_config", spec.ToolConfig, []string{"allowed_tools", "disabled_tools", "max_iterations", "max_tool_calls_per_run", "interleave_tool_calls", "tool_aliases", "tool_metadata"}); err != nil {
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
	if err := validateObjectConfig("profile_config", spec.ProfileConfig, []string{"personality", "communication_style", "language_capabilities", "domain_scope", "recommendation", "tool_selection", "agent_delegation"}); err != nil {
		return err
	}
	if err := validateProfileConfigValues(spec.ProfileConfig); err != nil {
		return err
	}
	if err := validateObjectConfig("mcp_config", spec.MCPConfig, []string{"servers"}); err != nil {
		return err
	}
	return validateMCPConfigValues(spec.MCPConfig)
}

func validateModelConfigValues(value any) error {
	obj, err := decodeConfigObject("model_config", value)
	if err != nil || obj == nil {
		return err
	}
	if raw, ok := obj["options"]; ok {
		if _, ok := raw.(map[string]any); !ok {
			return fmt.Errorf("model_config.options must be an object")
		}
	}
	return nil
}

func validateProfileConfigValues(value any) error {
	obj, err := decodeConfigObject("profile_config", value)
	if err != nil || obj == nil {
		return err
	}
	if raw, ok := obj["language_capabilities"]; ok {
		if err := validateStringArray("profile_config.language_capabilities", raw); err != nil {
			return err
		}
	}
	if raw, ok := obj["domain_scope"]; ok {
		scope, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("profile_config.domain_scope must be an object")
		}
		for _, key := range []string{"allowed_domains", "forbidden_domains"} {
			if raw, ok := scope[key]; ok {
				if err := validateStringArray("profile_config.domain_scope."+key, raw); err != nil {
					return err
				}
			}
		}
	}
	if raw, ok := obj["recommendation"]; ok {
		rec, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("profile_config.recommendation must be an object")
		}
		if enabled, _ := rec["enabled"].(bool); enabled {
			timeout, ok := numericValue(rec["timeout_ms"])
			if !ok || timeout <= 0 {
				return fmt.Errorf("profile_config.recommendation.timeout_ms must be positive when recommendation is enabled")
			}
		}
		for _, key := range []string{"model", "merge_model", "disclosure_style"} {
			if raw, ok := rec[key]; ok {
				if _, ok := raw.(string); !ok {
					return fmt.Errorf("profile_config.recommendation.%s must be a string", key)
				}
			}
		}
		for _, key := range []string{"sensitive_context_terms", "product_request_terms"} {
			if raw, ok := rec[key]; ok {
				if err := validateStringArray("profile_config.recommendation."+key, raw); err != nil {
					return err
				}
			}
		}
		if raw, ok := rec["max_candidates"]; ok {
			max, ok := numericValue(raw)
			if !ok || max < 0 {
				return fmt.Errorf("profile_config.recommendation.max_candidates must be a non-negative number")
			}
		}
		if raw, ok := rec["allow_sponsored"]; ok {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("profile_config.recommendation.allow_sponsored must be a boolean")
			}
		}
		if raw, ok := rec["block_sensitive_product_mix"]; ok {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("profile_config.recommendation.block_sensitive_product_mix must be a boolean")
			}
		}
	}
	if raw, ok := obj["tool_selection"]; ok {
		selection, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("profile_config.tool_selection must be an object")
		}
		if raw, ok := selection["enabled"]; ok {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("profile_config.tool_selection.enabled must be a boolean")
			}
		}
		for _, key := range []string{"mode", "model"} {
			if raw, ok := selection[key]; ok {
				if _, ok := raw.(string); !ok {
					return fmt.Errorf("profile_config.tool_selection.%s must be a string", key)
				}
			}
		}
		if raw, ok := selection["mode"]; ok {
			mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
			switch mode {
			case "", "disabled", "heuristic", "hybrid", "llm":
			default:
				return fmt.Errorf("profile_config.tool_selection.mode must be disabled, heuristic, hybrid, or llm")
			}
		}
		if raw, ok := selection["max_tools"]; ok {
			if err := validateNonNegativeInteger("profile_config.tool_selection.max_tools", raw); err != nil {
				return err
			}
		}
		if raw, ok := selection["confidence_threshold"]; ok {
			threshold, ok := numericValue(raw)
			if !ok || threshold < 0 || threshold > 1 {
				return fmt.Errorf("profile_config.tool_selection.confidence_threshold must be between 0 and 1")
			}
		}
	}
	if raw, ok := obj["agent_delegation"]; ok {
		delegation, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("profile_config.agent_delegation must be an object")
		}
		if raw, ok := delegation["enabled"]; ok {
			if _, ok := raw.(bool); !ok {
				return fmt.Errorf("profile_config.agent_delegation.enabled must be a boolean")
			}
		}
		if raw, ok := delegation["max_mentions_per_message"]; ok {
			if err := validateNonNegativeInteger("profile_config.agent_delegation.max_mentions_per_message", raw); err != nil {
				return err
			}
		}
	}
	return nil
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
	if raw, ok := obj["interleave_tool_calls"]; ok {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("tool_config.interleave_tool_calls must be a boolean")
		}
	}
	if raw, ok := obj["tool_aliases"]; ok {
		aliases, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("tool_config.tool_aliases must be an object")
		}
		seen := map[string]string{}
		for original, aliasRaw := range aliases {
			original = strings.TrimSpace(original)
			if original == "" {
				return fmt.Errorf("tool_config.tool_aliases contains an empty tool name")
			}
			alias, ok := aliasRaw.(string)
			if !ok {
				return fmt.Errorf("tool_config.tool_aliases.%s must be a string", original)
			}
			alias = strings.TrimSpace(alias)
			if !providerSafeToolAlias(alias) {
				return fmt.Errorf("tool_config.tool_aliases.%s must match ^[a-zA-Z0-9_-]{1,128}$", original)
			}
			if prior, exists := seen[alias]; exists && prior != original {
				return fmt.Errorf("tool_config.tool_aliases alias %q is assigned to both %q and %q", alias, prior, original)
			}
			seen[alias] = original
		}
	}
	if raw, ok := obj["tool_metadata"]; ok {
		metadata, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("tool_config.tool_metadata must be an object")
		}
		for name, rawMeta := range metadata {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("tool_config.tool_metadata contains an empty tool name")
			}
			meta, ok := rawMeta.(map[string]any)
			if !ok {
				return fmt.Errorf("tool_config.tool_metadata.%s must be an object", name)
			}
			for _, key := range []string{"tags", "conflicts_with"} {
				if raw, ok := meta[key]; ok {
					if err := validateStringArray("tool_config.tool_metadata."+name+"."+key, raw); err != nil {
						return err
					}
				}
			}
			if raw, ok := meta["side_effect"]; ok {
				if _, ok := raw.(string); !ok {
					return fmt.Errorf("tool_config.tool_metadata.%s.side_effect must be a string", name)
				}
			}
		}
	}
	return nil
}

func providerSafeToolAlias(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
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

func numericValue(raw any) (float64, bool) {
	value, ok := raw.(float64)
	return value, ok
}
