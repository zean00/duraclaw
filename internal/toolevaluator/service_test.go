package toolevaluator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/providers"
)

type fakeStore struct {
	queued    int
	global    bool
	claimed   []db.ToolEvaluation
	claimOne  *db.ToolEvaluation
	run       *db.Run
	trace     *db.RunTrace
	events    []db.Event
	messages  []db.Message
	version   *db.AgentInstanceVersion
	completed db.ToolEvaluationUpdate
	outbound  int
}

func (s *fakeStore) QueueSuspiciousToolEvaluations(_ context.Context, _ int, _ float64, globalEnabled bool) (int, error) {
	s.queued++
	s.global = globalEnabled
	return 1, nil
}
func (s *fakeStore) ClaimToolEvaluations(context.Context, string, time.Duration, int) ([]db.ToolEvaluation, error) {
	return s.claimed, nil
}
func (s *fakeStore) ClaimToolEvaluation(context.Context, string, string, time.Duration) (*db.ToolEvaluation, error) {
	if s.claimOne != nil {
		return s.claimOne, nil
	}
	return &s.claimed[0], nil
}
func (s *fakeStore) CompleteToolEvaluation(_ context.Context, _ string, update db.ToolEvaluationUpdate) error {
	s.completed = update
	return nil
}
func (s *fakeStore) QueueToolEvaluation(context.Context, string, any) (*db.ToolEvaluation, error) {
	return &s.claimed[0], nil
}
func (s *fakeStore) ToolEvaluationForRun(context.Context, string) (*db.ToolEvaluation, error) {
	return &db.ToolEvaluation{ID: "eval-1", Status: s.completed.Status, Category: s.completed.Category}, nil
}
func (s *fakeStore) GetRun(context.Context, string) (*db.Run, error) { return s.run, nil }
func (s *fakeStore) RunTrace(context.Context, string) (*db.RunTrace, error) {
	return s.trace, nil
}
func (s *fakeStore) Events(context.Context, string, int64) ([]db.Event, error) {
	return s.events, nil
}
func (s *fakeStore) RecentMessages(context.Context, string, string, int) ([]db.Message, error) {
	return s.messages, nil
}
func (s *fakeStore) AgentInstanceVersion(context.Context, string) (*db.AgentInstanceVersion, error) {
	return s.version, nil
}
func (s *fakeStore) CreateOutboundIntent(context.Context, db.OutboundIntent) (string, int64, error) {
	s.outbound++
	return "out-1", 1, nil
}
func (s *fakeStore) AddObservabilityEvent(context.Context, string, string, string, any) error {
	return nil
}

type verdictProvider struct{}

func (verdictProvider) GetDefaultModel() string { return "mock/evaluator" }
func (verdictProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: `{"category":"missed_tool","expected_tools":["create_reminder"],"actual_tools":[],"confidence":0.91,"reason":"reminder tool was selected but not called","safe_repair":false}`}, nil
}

func TestRunOnceDisabledStillScansForPerAgentEnablement(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, nil, providers.ModelConfig{Primary: "mock/duraclaw"}, "test", Config{})
	processed, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || store.queued != 1 || store.global {
		t.Fatalf("processed=%d queued=%d global=%v", processed, store.queued, store.global)
	}
}

func TestRunOnceEvaluatesSuspiciousRunWithSeparateModel(t *testing.T) {
	signals, _ := json.Marshal([]string{"selected_tool_missing"})
	selectedPayload, _ := json.Marshal(map[string]any{"selected_tools": []string{"create_reminder"}, "confidence": 0.92})
	store := &fakeStore{
		claimed: []db.ToolEvaluation{{ID: "eval-1", RunID: "run-1", SuspiciousSignals: signals}},
		run:     &db.Run{ID: "run-1", CustomerID: "c1", UserID: "u1", AgentInstanceID: "a1", AgentInstanceVersionID: "v1", SessionID: "s1", State: "completed"},
		trace:   &db.RunTrace{},
		events:  []db.Event{{Type: "tool_selection.completed", Payload: selectedPayload}},
	}
	registry := providers.NewRegistry("judge")
	registry.Register("judge", verdictProvider{})
	service := NewService(store, registry, providers.ModelConfig{Primary: "mock/duraclaw"}, "test", Config{Enabled: true, Model: "judge/custom", RepairMode: "safe"})
	processed, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d", processed)
	}
	if store.completed.Category != "missed_tool" || store.completed.Confidence != 0.91 {
		t.Fatalf("completed=%#v", store.completed)
	}
}

func TestEvaluateRunClaimsRequestedEvaluationOnly(t *testing.T) {
	signals, _ := json.Marshal([]string{"manual"})
	selectedPayload, _ := json.Marshal(map[string]any{"selected_tools": []string{"create_reminder"}, "confidence": 0.92})
	manual := &db.ToolEvaluation{ID: "manual-eval", RunID: "run-1", SuspiciousSignals: signals}
	store := &fakeStore{
		claimed:  []db.ToolEvaluation{{ID: "older-eval", RunID: "other-run"}},
		claimOne: manual,
		run:      &db.Run{ID: "run-1", CustomerID: "c1", UserID: "u1", AgentInstanceID: "a1", AgentInstanceVersionID: "v1", SessionID: "s1", State: "completed"},
		trace:    &db.RunTrace{},
		events:   []db.Event{{Type: "tool_selection.completed", Payload: selectedPayload}},
	}
	registry := providers.NewRegistry("judge")
	registry.Register("judge", verdictProvider{})
	service := NewService(store, registry, providers.ModelConfig{Primary: "mock/duraclaw"}, "test", Config{Enabled: true, Model: "judge/custom"})
	if _, err := service.EvaluateRun(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	if store.completed.Category != "missed_tool" {
		t.Fatalf("completed=%#v", store.completed)
	}
}

func TestSafeRepairQueuesOnlySafeClarification(t *testing.T) {
	store := &fakeStore{run: &db.Run{ID: "run-1", CustomerID: "c1", UserID: "u1", SessionID: "s1"}}
	service := NewService(store, nil, providers.ModelConfig{Primary: "mock/duraclaw"}, "test", Config{Enabled: true, ConfidenceThreshold: 0.7, RepairMode: "safe"})
	action, status := service.maybeRepair(context.Background(), service.cfg, store.run, verdict{Category: "clarification_needed", Confidence: 0.9, SafeRepair: true, RepairInstruction: "What time should I use?"})
	if action != "safe_outbound" || status != "queued" || store.outbound != 1 {
		t.Fatalf("action=%q status=%q outbound=%d", action, status, store.outbound)
	}
	action, status = service.maybeRepair(context.Background(), service.cfg, store.run, verdict{Category: "missed_tool", Confidence: 0.95, SafeRepair: true})
	if action != "" || status != "skipped" || store.outbound != 1 {
		t.Fatalf("unsafe action=%q status=%q outbound=%d", action, status, store.outbound)
	}
}
