package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const mcpToolAccessCacheTTL = 30 * time.Second

type MCPToolAccessRule struct {
	CustomerID      string          `json:"customer_id"`
	AgentInstanceID string          `json:"agent_instance_id"`
	UserID          string          `json:"user_id,omitempty"`
	ServerName      string          `json:"server_name"`
	AllowedTools    []string        `json:"allowed_tools"`
	DeniedTools     []string        `json:"denied_tools"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type EffectiveMCPToolAccess struct {
	CustomerID      string   `json:"customer_id"`
	AgentInstanceID string   `json:"agent_instance_id"`
	UserID          string   `json:"user_id,omitempty"`
	ServerName      string   `json:"server_name"`
	AllowedTools    []string `json:"allowed_tools"`
	DeniedTools     []string `json:"denied_tools"`
	HasRule         bool     `json:"has_rule"`
	Source          string   `json:"source,omitempty"`
}

type mcpToolAccessCacheEntry struct {
	access    EffectiveMCPToolAccess
	expiresAt time.Time
}

func (s *Store) UpsertMCPToolAccessRule(ctx context.Context, rule MCPToolAccessRule) (*MCPToolAccessRule, error) {
	rule.CustomerID = strings.TrimSpace(rule.CustomerID)
	rule.AgentInstanceID = strings.TrimSpace(rule.AgentInstanceID)
	rule.UserID = strings.TrimSpace(rule.UserID)
	rule.ServerName = strings.TrimSpace(rule.ServerName)
	if rule.CustomerID == "" || rule.AgentInstanceID == "" || rule.ServerName == "" {
		return nil, ValidationError{Message: "customer_id, agent_instance_id, and server_name are required"}
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
	var out MCPToolAccessRule
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mcp_tool_access_rules(customer_id,agent_instance_id,user_id,server_name,allowed_tools,denied_tools,metadata,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (customer_id,agent_instance_id,user_id,server_name) DO UPDATE SET
			allowed_tools=EXCLUDED.allowed_tools,
			denied_tools=EXCLUDED.denied_tools,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING customer_id, agent_instance_id, user_id, server_name, allowed_tools, denied_tools, metadata, updated_at`,
		rule.CustomerID, rule.AgentInstanceID, rule.UserID, rule.ServerName, allowedJSON, deniedJSON, metadata).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &out.ServerName, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(allowedJSON, &out.AllowedTools)
	_ = json.Unmarshal(deniedJSON, &out.DeniedTools)
	s.invalidateMCPToolAccess(rule.CustomerID, rule.AgentInstanceID, rule.UserID, rule.ServerName)
	return &out, nil
}

func (s *Store) MCPToolAccessRule(ctx context.Context, customerID, agentInstanceID, userID, serverName string) (*MCPToolAccessRule, error) {
	var out MCPToolAccessRule
	var allowedJSON, deniedJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT customer_id, agent_instance_id, user_id, server_name, allowed_tools, denied_tools, metadata, updated_at
		FROM mcp_tool_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3 AND server_name=$4`,
		customerID, agentInstanceID, strings.TrimSpace(userID), serverName).
		Scan(&out.CustomerID, &out.AgentInstanceID, &out.UserID, &out.ServerName, &allowedJSON, &deniedJSON, &out.Metadata, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(allowedJSON, &out.AllowedTools)
	_ = json.Unmarshal(deniedJSON, &out.DeniedTools)
	return &out, nil
}

func (s *Store) DeleteMCPToolAccessRule(ctx context.Context, customerID, agentInstanceID, userID, serverName string) error {
	userID = strings.TrimSpace(userID)
	_, err := s.pool.Exec(ctx, `
		DELETE FROM mcp_tool_access_rules
		WHERE customer_id=$1 AND agent_instance_id=$2 AND user_id=$3 AND server_name=$4`,
		customerID, agentInstanceID, userID, serverName)
	if err == nil {
		s.invalidateMCPToolAccess(customerID, agentInstanceID, userID, serverName)
	}
	return err
}

func (s *Store) EffectiveMCPToolAccess(ctx context.Context, customerID, agentInstanceID, userID, serverName string) (EffectiveMCPToolAccess, error) {
	key := mcpToolAccessCacheKey(customerID, agentInstanceID, userID, serverName)
	if cached, ok := s.mcpAccessCache.Load(key); ok {
		entry := cached.(mcpToolAccessCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.access, nil
		}
		s.mcpAccessCache.Delete(key)
	}
	access, err := s.effectiveMCPToolAccess(ctx, customerID, agentInstanceID, userID, serverName)
	if err != nil {
		return access, err
	}
	s.mcpAccessCache.Store(key, mcpToolAccessCacheEntry{access: access, expiresAt: time.Now().Add(mcpToolAccessCacheTTL)})
	return access, nil
}

func (s *Store) CheckMCPToolAccess(ctx context.Context, customerID, agentInstanceID, userID, serverName, toolName string) error {
	access, err := s.EffectiveMCPToolAccess(ctx, customerID, agentInstanceID, userID, serverName)
	if err != nil {
		return err
	}
	if !MCPToolAllowed(access, toolName) {
		return fmt.Errorf("mcp tool %q on server %q is not allowed", toolName, serverName)
	}
	return nil
}

func MCPToolAllowed(access EffectiveMCPToolAccess, toolName string) bool {
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

func (s *Store) effectiveMCPToolAccess(ctx context.Context, customerID, agentInstanceID, userID, serverName string) (EffectiveMCPToolAccess, error) {
	base := EffectiveMCPToolAccess{CustomerID: customerID, AgentInstanceID: agentInstanceID, UserID: userID, ServerName: serverName}
	if strings.TrimSpace(userID) != "" {
		rule, err := s.MCPToolAccessRule(ctx, customerID, agentInstanceID, userID, serverName)
		if err == nil {
			return rule.effective("user"), nil
		}
		if err != nil && err != pgx.ErrNoRows {
			return base, err
		}
	}
	rule, err := s.MCPToolAccessRule(ctx, customerID, agentInstanceID, "", serverName)
	if err == nil {
		return rule.effective("customer"), nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return base, err
	}
	return base, nil
}

func (r MCPToolAccessRule) effective(source string) EffectiveMCPToolAccess {
	return EffectiveMCPToolAccess{
		CustomerID: r.CustomerID, AgentInstanceID: r.AgentInstanceID, UserID: r.UserID, ServerName: r.ServerName,
		AllowedTools: normalizedStrings(r.AllowedTools), DeniedTools: normalizedStrings(r.DeniedTools), HasRule: true, Source: source,
	}
}

func (s *Store) invalidateMCPToolAccess(_, _, _, _ string) {
	s.mcpAccessCache.Range(func(key, _ any) bool {
		s.mcpAccessCache.Delete(key)
		return true
	})
}

func mcpToolAccessCacheKey(customerID, agentInstanceID, userID, serverName string) string {
	return strings.Join([]string{customerID, agentInstanceID, strings.TrimSpace(userID), serverName}, "\x00")
}

func normalizedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func mcpToolSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range normalizedStrings(values) {
		out[value] = true
	}
	return out
}
