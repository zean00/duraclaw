package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"duraclaw/internal/db"
)

type fakeReminderStore struct {
	spec      db.ReminderSubscriptionSpec
	updateID  string
	update    db.ReminderSubscriptionUpdate
	updateSub *db.ReminderSubscription
}

func (s *fakeReminderStore) CreateReminderSubscription(_ context.Context, spec db.ReminderSubscriptionSpec) (*db.ReminderSubscription, error) {
	s.spec = spec
	return &db.ReminderSubscription{
		ID:              "rem-1",
		CustomerID:      spec.CustomerID,
		UserID:          spec.UserID,
		SessionID:       spec.SessionID,
		AgentInstanceID: spec.AgentInstanceID,
		Title:           spec.Title,
		Schedule:        spec.Schedule,
		Timezone:        "UTC",
		Enabled:         true,
		NextRunAt:       spec.NextRunAt,
	}, nil
}

func (s *fakeReminderStore) UpdateUserReminderSubscription(_ context.Context, id, customerID, userID string, update db.ReminderSubscriptionUpdate) (*db.ReminderSubscription, error) {
	s.updateID = id
	s.update = update
	if s.updateSub != nil {
		return s.updateSub, nil
	}
	title := ""
	if update.Title != nil {
		title = *update.Title
	}
	schedule := ""
	if update.Schedule != nil {
		schedule = *update.Schedule
	}
	timezone := "UTC"
	if update.Timezone != nil {
		timezone = *update.Timezone
	}
	next := time.Now().UTC().Add(time.Hour)
	if update.NextRunAt != nil {
		next = *update.NextRunAt
	}
	return &db.ReminderSubscription{
		ID:              id,
		CustomerID:      customerID,
		UserID:          userID,
		SessionID:       "s1",
		AgentInstanceID: "a1",
		Title:           title,
		Schedule:        schedule,
		Timezone:        timezone,
		Enabled:         true,
		NextRunAt:       next,
	}, nil
}

func TestCreateReminderToolReturnsReferenceArtifact(t *testing.T) {
	store := &fakeReminderStore{}
	result := (CreateReminderTool{Store: store}).Execute(context.Background(), ExecutionContext{
		CustomerID: "c1", UserID: "u1", SessionID: "s1", AgentInstanceID: "a1", RunID: "run-1", RequestID: "req-1",
	}, map[string]any{
		"title":       "standup",
		"schedule":    "@once",
		"next_run_at": "2030-01-01T09:00:00Z",
		"payload":     map[string]any{"message": "standup"},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts=%#v", result.Artifacts)
	}
	ref := result.Artifacts[0]
	if ref.Type != "reminder_reference" || ref.ID != "rem-1" {
		t.Fatalf("ref=%#v", ref)
	}
	if ref.Data["subscription_id"] != "rem-1" || !strings.Contains(ref.Data["delete_api"].(string), "/acp/reminders/rem-1") {
		t.Fatalf("ref data=%#v", ref.Data)
	}
	if !strings.Contains(result.ForLLM, `"reminder_reference"`) || !strings.Contains(result.ForLLM, `"subscription_id":"rem-1"`) {
		t.Fatalf("for_llm=%s", result.ForLLM)
	}
	if store.spec.CustomerID != "c1" || store.spec.UserID != "u1" || store.spec.NextRunAt.IsZero() {
		t.Fatalf("spec=%#v", store.spec)
	}
}

func TestCreateReminderToolRequiresNextRunAtForOnce(t *testing.T) {
	result := (CreateReminderTool{Store: &fakeReminderStore{}}).Execute(context.Background(), ExecutionContext{}, map[string]any{"schedule": "@once"})
	if !result.IsError || !strings.Contains(result.ForLLM, "next_run_at") {
		t.Fatalf("result=%#v", result)
	}
}

func TestReminderToolGuidanceRequiresClarificationForAmbiguousTimes(t *testing.T) {
	desc := CreateReminderTool{}.Description()
	for _, want := range []string{"Do not assume ambiguous times", "ask the user for clarification", "use update_reminder instead of creating a duplicate"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %s", want, desc)
		}
	}
}

func TestCreateReminderToolRejectsPastNextRunAt(t *testing.T) {
	result := (CreateReminderTool{Store: &fakeReminderStore{}}).Execute(context.Background(), ExecutionContext{}, map[string]any{
		"schedule":    "@once",
		"next_run_at": "2024-04-28T08:00:00+07:00",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "must be in the future") {
		t.Fatalf("result=%#v", result)
	}
}

func TestCreateReminderToolAllowsNearFutureNextRunAt(t *testing.T) {
	store := &fakeReminderStore{}
	next := time.Now().UTC().Add(10 * time.Second).Format(time.RFC3339)
	result := (CreateReminderTool{Store: store}).Execute(context.Background(), ExecutionContext{
		CustomerID: "c1", UserID: "u1", SessionID: "s1", AgentInstanceID: "a1",
	}, map[string]any{
		"title":       "quick reminder",
		"schedule":    "@once",
		"next_run_at": next,
	})
	if result.IsError {
		t.Fatalf("near-future reminders should be allowed: %#v", result)
	}
	if store.spec.NextRunAt.IsZero() {
		t.Fatalf("spec=%#v", store.spec)
	}
}

func TestCreateReminderToolKeepsPayloadEmptyForTitleFallback(t *testing.T) {
	store := &fakeReminderStore{}
	result := (CreateReminderTool{Store: store}).Execute(context.Background(), ExecutionContext{
		CustomerID: "c1", UserID: "u1", SessionID: "s1", AgentInstanceID: "a1", RunID: "run-1",
	}, map[string]any{
		"title":       "drink water",
		"schedule":    "@once",
		"next_run_at": "2030-01-01T09:00:00Z",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	raw, _ := json.Marshal(store.spec.Payload)
	if string(raw) != "{}" {
		t.Fatalf("payload should remain empty so scheduler falls back to title, got %s", string(raw))
	}
}

func TestUpdateReminderToolReturnsReferenceArtifact(t *testing.T) {
	store := &fakeReminderStore{}
	result := (UpdateReminderTool{Store: store}).Execute(context.Background(), ExecutionContext{
		CustomerID: "c1", UserID: "u1", SessionID: "s1", AgentInstanceID: "a1", RunID: "run-1", RequestID: "req-1",
	}, map[string]any{
		"subscription_id": "rem-1",
		"title":           "buy eggs",
		"schedule":        "@once",
		"timezone":        "Asia/Jakarta",
		"next_run_at":     "2030-01-01T08:00:00+07:00",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if store.updateID != "rem-1" || store.update.Title == nil || *store.update.Title != "buy eggs" || store.update.NextRunAt == nil {
		t.Fatalf("updateID=%q update=%#v", store.updateID, store.update)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Type != "reminder_reference" || result.Artifacts[0].ID != "rem-1" {
		t.Fatalf("artifacts=%#v", result.Artifacts)
	}
	if !strings.Contains(result.ForLLM, `"status":"updated"`) || !strings.Contains(result.ForLLM, `"subscription_id":"rem-1"`) || !strings.Contains(result.ForLLM, "set/scheduled with the latest details") {
		t.Fatalf("for_llm=%s", result.ForLLM)
	}
}

func TestUpdateReminderToolRequiresSubscriptionID(t *testing.T) {
	result := (UpdateReminderTool{Store: &fakeReminderStore{}}).Execute(context.Background(), ExecutionContext{}, map[string]any{"title": "x"})
	if !result.IsError || !strings.Contains(result.ForLLM, "subscription_id") {
		t.Fatalf("result=%#v", result)
	}
}

func TestReminderNextRunAtParsesCron(t *testing.T) {
	next, err := reminderNextRunAt("* * * * *", "")
	if err != nil {
		t.Fatal(err)
	}
	if next.Before(time.Now().UTC()) {
		t.Fatalf("next=%s", next)
	}
}
