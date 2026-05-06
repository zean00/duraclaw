package outbound

import (
	"context"
	"encoding/json"
	"fmt"

	"duraclaw/internal/db"
)

type Intent struct {
	CustomerID  string         `json:"customer_id"`
	UserID      string         `json:"user_id"`
	SessionID   string         `json:"session_id"`
	RunID       string         `json:"run_id,omitempty"`
	Type        string         `json:"intent_type"`
	ChannelType string         `json:"channel_type,omitempty"`
	Payload     map[string]any `json:"payload"`
}

type Store interface {
	CreateOutboundIntent(ctx context.Context, intent db.OutboundIntent) (string, int64, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) Emit(ctx context.Context, intent Intent) (string, int64, error) {
	if s.store == nil {
		return "", 0, fmt.Errorf("outbound store is nil")
	}
	if intent.CustomerID == "" || intent.UserID == "" || intent.SessionID == "" || intent.Type == "" {
		return "", 0, fmt.Errorf("customer_id, user_id, session_id, and intent_type are required")
	}
	payload, _ := json.Marshal(intent.Payload)
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	var runID *string
	if intent.RunID != "" {
		runID = &intent.RunID
	}
	return s.store.CreateOutboundIntent(ctx, db.OutboundIntent{
		CustomerID:  intent.CustomerID,
		UserID:      intent.UserID,
		SessionID:   intent.SessionID,
		RunID:       runID,
		Type:        intent.Type,
		ChannelType: intent.ChannelType,
		Payload:     payload,
	})
}
