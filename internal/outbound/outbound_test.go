package outbound

import (
	"context"
	"testing"

	"duraclaw/internal/db"
)

type fakeStore struct {
	intent db.OutboundIntent
}

func (s *fakeStore) CreateOutboundIntent(_ context.Context, intent db.OutboundIntent) (string, int64, error) {
	s.intent = intent
	return "intent-1", 42, nil
}

func TestEmitValidatesAndPersistsIntent(t *testing.T) {
	store := &fakeStore{}
	id, outboxID, err := NewService(store).Emit(context.Background(), Intent{
		CustomerID: "c", UserID: "u", SessionID: "s", RunID: "r", Type: "message", Payload: map[string]any{"text": "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "intent-1" || outboxID != 42 {
		t.Fatalf("id=%s outbox=%d", id, outboxID)
	}
	if store.intent.RunID == nil || *store.intent.RunID != "r" || store.intent.Type != "message" {
		t.Fatalf("intent=%#v", store.intent)
	}
}

func TestEmitRequiresRoutingFields(t *testing.T) {
	_, _, err := NewService(&fakeStore{}).Emit(context.Background(), Intent{CustomerID: "c"})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}
