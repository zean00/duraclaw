package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const toolAccessCacheTTL = 30 * time.Second

type ToolAccessRule struct {
	CustomerID      string          `json:"customer_id"`
	AgentInstanceID string          `json:"agent_instance_id"`
	UserID          string          `json:"user_id,omitempty"`
	AllowedTools    []string        `json:"allowed_tools"`
	DeniedTools     []string        `json:"denied_tools"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type EffectiveToolAccess struct {
	CustomerID      string   `json:"customer_id"`
	AgentInstanceID string   `json:"agent_instance_id"`
	UserID          string   `json:"user_id,omitempty"`
	AllowedTools    []string `json:"allowed_tools"`
	DeniedTools     []string `json:"denied_tools"`
	HasRule         bool     `json:"has_rule"`
	Source          string   `json:"source,omitempty"`
}

type toolAccessCacheEntry struct {
	access    EffectiveToolAccess
	expiresAt time.Time
}

func (s *Store) UpsertToolAccessRule(ctx context.Context, rule ToolAccessRule) (*ToolAccessRule, error) {
	rule.CustomerID = strings.TrimSpace(rule.CustomerID)
	rule.AgentInstanceID = strings.TrimSpace(rule.AgentInstanceID)
	rule.UserID = strings.TrimSpace(rule.UserID)
	if rule.CustomerID == "" || rule.AgentInstanceID == "" {
		return nil, ValidationError{Message: "customer_id and agent_instance_id are required"}
	}
	allowed := normalizedStrings(rule.AllowedTools)
	denied := normalizedStrings(rule.DeniedTools)
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
	var out ToolAccessRule
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tool_access_rules(customer_id,agent_instance_id,user_id,allowed_tools,denied_tools,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (customer_id,agent_instance_id,user_id) DO UPDATE SET
			allowed_tools=EXCLUDED.allowed_tools,
			denied_tools=EXCLUDED.denied_tools,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, agent_instance_id, user_id, allowed_tools, denied_tools, metadata, updated_at`,
		rule.CustomerID, rule.AgentInstanceID, rule.UserID, allowedJSON, deniedJSON, metadata).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := decodeToolLists(allowedJSON, deniedJSON, &out); err != nil {
		return nil, err
	}
	s.invalidateToolAccess(rule.CustomerID, rule.AgentInstanceID, rule.UserID)
	return &out, nil
}

func (s *Store) ToolAccessRule(ctx context.Context, customerID, agentInstanceID, userID string) (*ToolAccessRule, error) {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	userID = strings.TrimSpace(userID)
	var out ToolAccessRule
	var allowedJSON, deniedJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, user_id, allowed_tools, denied_tools, metadata, updated_at
		FROM tool_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3`,
		customerID, agentInstanceID, userID).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := decodeToolLists(allowedJSON, deniedJSON, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) DeleteToolAccessRule(ctx context.Context, customerID, agentInstanceID, userID string) error {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	userID = strings.TrimSpace(userID)
	_, err := s.pool.Exec(ctx, `
		DELETE FROM tool_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3`,
		customerID, agentInstanceID, userID)
	if err == nil {
		s.invalidateToolAccess(customerID, agentInstanceID, userID)
	}
	return err
}

func (s *Store) EffectiveToolAccess(ctx context.Context, customerID, agentInstanceID, userID string) (EffectiveToolAccess, error) {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	userID = strings.TrimSpace(userID)
	key := toolAccessCacheKey(customerID, agentInstanceID, userID)
	if cached, ok := s.toolAccessCache.Load(key); ok {
		entry := cached.(toolAccessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.access, nil
		}
		s.toolAccessCache.Delete(key)
	}
	access, err := s.effectiveToolAccess(ctx, customerID, agentInstanceID, userID)
	if err != nil {
		return access, err
	}
	s.toolAccessCache.Store(key, toolAccessCacheEntry{access: access, expiresAt: time.Now().Add(toolAccessCacheTTL)})
	return access, nil
}

func (s *Store) CheckToolAccess(ctx context.Context, customerID, agentInstanceID, userID, toolName string) error {
	customerID = strings.TrimSpace(customerID)
	agentInstanceID = strings.TrimSpace(agentInstanceID)
	toolName = strings.TrimSpace(toolName)
	if customerID == "" || agentInstanceID == "" || toolName == "" {
		return ValidationError{Message: "customer_id, agent_instance_id, and tool_name are required for tool access"}
	}
	access, err := s.EffectiveToolAccess(ctx, customerID, agentInstanceID, userID)
	if err != nil {
		return err
	}
	if !ToolAllowed(access, toolName) {
		return fmt.Errorf("tool %q is not allowed", toolName)
	}
	return nil
}

func ToolAllowed(access EffectiveToolAccess, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return false
	}
	denied := mcpToolSet(access.DeniedTools)
	if denied[toolName] {
		return false
	}
	allowed := mcpToolSet(access.AllowedTools)
	return len(allowed) == 0 || allowed[toolName]
}

func (s *Store) effectiveToolAccess(ctx context.Context, customerID, agentInstanceID, userID string) (EffectiveToolAccess, error) {
	base := EffectiveToolAccess{CustomerID: customerID, AgentInstanceID: agentInstanceID, UserID: userID}
	if strings.TrimSpace(userID) != "" {
		rule, err := s.ToolAccessRule(ctx, customerID, agentInstanceID, userID)
		if err == nil {
			return rule.effective("user"), nil
		}
		if err != nil && err != pgx.ErrNoRows {
			return base, err
		}
	}
	rule, err := s.ToolAccessRule(ctx, customerID, agentInstanceID, "")
	if err == nil {
		return rule.effective("customer"), nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return base, err
	}
	return base, nil
}

func (r ToolAccessRule) effective(source string) EffectiveToolAccess {
	return EffectiveToolAccess{
		CustomerID: r.CustomerID, AgentInstanceID: r.AgentInstanceID, UserID: r.UserID,
		AllowedTools: normalizedStrings(r.AllowedTools), DeniedTools: normalizedStrings(r.DeniedTools), HasRule: true, Source: source,
	}
}

func (s *Store) invalidateToolAccess(_, _, _ string) {
	s.toolAccessCache.Range(func(key, _ any) bool {
		s.toolAccessCache.Delete(key)
		return true
	})
}

func toolAccessCacheKey(customerID, agentInstanceID, userID string) string {
	return strings.Join([]string{customerID, agentInstanceID, strings.TrimSpace(userID)}, "\x00")
}

func decodeToolLists(allowedJSON, deniedJSON []byte, out *ToolAccessRule) error {
	if err := json.Unmarshal(allowedJSON, &out.AllowedTools); err != nil {
		return fmt.Errorf("decode allowed tools: %w", err)
	}
	if err := json.Unmarshal(deniedJSON, &out.DeniedTools); err != nil {
		return fmt.Errorf("decode denied tools: %w", err)
	}
	out.AllowedTools = normalizedStrings(out.AllowedTools)
	out.DeniedTools = normalizedStrings(out.DeniedTools)
	return nil
}
