package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type SessionTransfer struct {
	ID                  string          `json:"id"`
	CustomerID          string          `json:"customer_id"`
	SessionID           string          `json:"session_id"`
	FromAgentInstanceID string          `json:"from_agent_instance_id"`
	ToAgentInstanceID   string          `json:"to_agent_instance_id"`
	Reason              string          `json:"reason"`
	Metadata            json.RawMessage `json:"metadata"`
	CreatedAt           time.Time       `json:"created_at"`
}

type SessionRecommendationPolicy struct {
	BlockedChannels []string `json:"blocked_channels,omitempty"`
}

type SessionRecommendationDelivery struct {
	ChannelType string `json:"channel_type,omitempty"`
	Blocked     bool   `json:"blocked"`
}

func sessionMetadataFromContext(c ACPContext) map[string]any {
	metadata := map[string]any{}
	if channelType := normalizeChannelName(c.ChannelType); channelType != "" {
		metadata["channel_type"] = channelType
	}
	if value := strings.TrimSpace(c.ChannelUserID); value != "" {
		metadata["channel_user_id"] = value
	}
	if value := strings.TrimSpace(c.ChannelConvID); value != "" {
		metadata["channel_conversation_id"] = value
	}
	return metadata
}

func NormalizeRecommendationPolicy(policy SessionRecommendationPolicy) SessionRecommendationPolicy {
	seen := map[string]bool{}
	out := SessionRecommendationPolicy{}
	for _, channel := range policy.BlockedChannels {
		channel = normalizeChannelName(channel)
		if channel == "" || seen[channel] {
			continue
		}
		seen[channel] = true
		out.BlockedChannels = append(out.BlockedChannels, channel)
	}
	return out
}

func (s *Store) SessionMetadata(ctx context.Context, customerID, sessionID string) (map[string]any, error) {
	if customerID == "" || sessionID == "" {
		return nil, fmt.Errorf("customer_id and session_id are required")
	}
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT metadata FROM sessions WHERE customer_id=$1 AND id=$2`, customerID, sessionID).Scan(&raw); err != nil {
		return nil, err
	}
	return decodeMetadata(raw), nil
}

func (s *Store) MergeSessionMetadata(ctx context.Context, customerID, sessionID string, metadata any) error {
	if customerID == "" || sessionID == "" {
		return fmt.Errorf("customer_id and session_id are required")
	}
	b, _ := json.Marshal(metadata)
	if len(b) == 0 || string(b) == "null" {
		b = []byte(`{}`)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET metadata=metadata || $3::jsonb, updated_at=now()
		WHERE customer_id=$1 AND id=$2`, customerID, sessionID, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

func (s *Store) ReassignSession(ctx context.Context, customerID, sessionID, toAgentInstanceID, reason string, metadata any) (*SessionTransfer, error) {
	if customerID == "" || sessionID == "" || toAgentInstanceID == "" {
		return nil, fmt.Errorf("customer_id, session_id, and to_agent_instance_id are required")
	}
	var transfer SessionTransfer
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx, `
			SELECT agent_instance_id
			FROM sessions
			WHERE customer_id=$1 AND id=$2
			FOR UPDATE`, customerID, sessionID).Scan(&from); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_instances(customer_id,id) VALUES($1,$2) ON CONFLICT DO NOTHING`, customerID, toAgentInstanceID); err != nil {
			return err
		}
		if err := ensureDefaultAgentInstanceVersion(ctx, tx, customerID, toAgentInstanceID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sessions SET agent_instance_id=$3, updated_at=now() WHERE customer_id=$1 AND id=$2`, customerID, sessionID, toAgentInstanceID); err != nil {
			return err
		}
		b, _ := json.Marshal(metadata)
		if len(b) == 0 || string(b) == "null" {
			b = []byte(`{}`)
		}
		return tx.QueryRow(ctx, `
			INSERT INTO session_agent_instance_transfers(customer_id,session_id,from_agent_instance_id,to_agent_instance_id,reason,metadata)
			VALUES($1,$2,$3,$4,$5,$6)
			RETURNING id::text, customer_id, session_id, from_agent_instance_id, to_agent_instance_id, reason, metadata, created_at`,
			customerID, sessionID, from, toAgentInstanceID, reason, b).
			Scan(&transfer.ID, &transfer.CustomerID, &transfer.SessionID, &transfer.FromAgentInstanceID, &transfer.ToAgentInstanceID, &transfer.Reason, &transfer.Metadata, &transfer.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) sessionAgentInstanceID(ctx context.Context, customerID, sessionID string) (string, error) {
	var agentInstanceID string
	err := s.pool.QueryRow(ctx, `
		SELECT agent_instance_id
		FROM sessions
		WHERE customer_id=$1 AND id=$2`, customerID, sessionID).Scan(&agentInstanceID)
	return agentInstanceID, err
}

func (s *Store) LatestSessionTransfer(ctx context.Context, customerID, sessionID string) (*SessionTransfer, error) {
	var transfer SessionTransfer
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, session_id, from_agent_instance_id, to_agent_instance_id, reason, metadata, created_at
		FROM session_agent_instance_transfers
		WHERE customer_id=$1 AND session_id=$2
		ORDER BY created_at DESC
		LIMIT 1`, customerID, sessionID).
		Scan(&transfer.ID, &transfer.CustomerID, &transfer.SessionID, &transfer.FromAgentInstanceID, &transfer.ToAgentInstanceID, &transfer.Reason, &transfer.Metadata, &transfer.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &transfer, nil
}

func (s *Store) SessionRecommendationDelivery(ctx context.Context, customerID, sessionID string) (SessionRecommendationDelivery, error) {
	metadata, err := s.SessionMetadata(ctx, customerID, sessionID)
	if err != nil {
		return SessionRecommendationDelivery{}, err
	}
	return sessionRecommendationDeliveryFromMetadata(metadata), nil
}

func (s *Store) RecommendationDeliveryBySession(ctx context.Context, customerID string, sessionIDs []string) (map[string]SessionRecommendationDelivery, error) {
	out := map[string]SessionRecommendationDelivery{}
	if customerID == "" || len(sessionIDs) == 0 {
		return out, nil
	}
	seen := map[string]bool{}
	var unique []string
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" || seen[sessionID] {
			continue
		}
		seen[sessionID] = true
		unique = append(unique, sessionID)
	}
	if len(unique) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, metadata FROM sessions WHERE customer_id=$1 AND id=ANY($2)`, customerID, unique)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var raw []byte
		if err := rows.Scan(&sessionID, &raw); err != nil {
			return nil, err
		}
		out[sessionID] = sessionRecommendationDeliveryFromMetadata(decodeMetadata(raw))
	}
	return out, rows.Err()
}

func sessionRecommendationDeliveryFromMetadata(metadata map[string]any) SessionRecommendationDelivery {
	channelType, _ := metadata["channel_type"].(string)
	channelType = normalizeChannelName(channelType)
	delivery := SessionRecommendationDelivery{ChannelType: channelType}
	if channelType == "" {
		return delivery
	}
	rec, _ := metadata["recommendation"].(map[string]any)
	for _, blocked := range stringSliceFromAny(rec["blocked_channels"]) {
		if normalizeChannelName(blocked) == channelType {
			delivery.Blocked = true
			return delivery
		}
	}
	return delivery
}

func decodeMetadata(raw []byte) map[string]any {
	var metadata map[string]any
	_ = json.Unmarshal(raw, &metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata
}

func stringSliceFromAny(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeChannelName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
