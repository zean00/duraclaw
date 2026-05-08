package toolevaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/providers"
)

type Store interface {
	QueueSuspiciousToolEvaluations(ctx context.Context, limit int, threshold float64, globalEnabled bool) (int, error)
	ClaimToolEvaluations(ctx context.Context, owner string, leaseFor time.Duration, limit int) ([]db.ToolEvaluation, error)
	ClaimToolEvaluation(ctx context.Context, id, owner string, leaseFor time.Duration) (*db.ToolEvaluation, error)
	CompleteToolEvaluation(ctx context.Context, id string, update db.ToolEvaluationUpdate) error
	QueueToolEvaluation(ctx context.Context, runID string, signals any) (*db.ToolEvaluation, error)
	ToolEvaluationForRun(ctx context.Context, runID string) (*db.ToolEvaluation, error)
	GetRun(ctx context.Context, id string) (*db.Run, error)
	RunTrace(ctx context.Context, runID string) (*db.RunTrace, error)
	Events(ctx context.Context, runID string, after int64) ([]db.Event, error)
	RecentMessages(ctx context.Context, customerID, sessionID string, limit int) ([]db.Message, error)
	AgentInstanceVersion(ctx context.Context, versionID string) (*db.AgentInstanceVersion, error)
	CreateOutboundIntent(ctx context.Context, intent db.OutboundIntent) (string, int64, error)
	AddObservabilityEvent(ctx context.Context, customerID, runID, eventType string, payload any) error
}

type Config struct {
	Enabled             bool           `json:"enabled"`
	Model               string         `json:"model"`
	Options             map[string]any `json:"options"`
	ConfidenceThreshold float64        `json:"confidence_threshold"`
	RepairMode          string         `json:"repair_mode"`
	Limit               int            `json:"limit"`
	LeaseFor            time.Duration  `json:"-"`
}

type Service struct {
	store       Store
	providers   *providers.Registry
	modelConfig providers.ModelConfig
	owner       string
	cfg         Config
}

type profileConfig struct {
	ToolEvaluator Config `json:"tool_evaluator"`
}

type verdict struct {
	Category          string   `json:"category"`
	ExpectedTools     []string `json:"expected_tools"`
	ActualTools       []string `json:"actual_tools"`
	Confidence        float64  `json:"confidence"`
	Reason            string   `json:"reason"`
	SafeRepair        bool     `json:"safe_repair"`
	RepairInstruction string   `json:"repair_instruction"`
}

func NewService(store Store, registry *providers.Registry, modelConfig providers.ModelConfig, owner string, cfg Config) *Service {
	if owner == "" {
		owner = "duraclaw-tool-evaluator"
	}
	if cfg.ConfidenceThreshold <= 0 || cfg.ConfidenceThreshold > 1 {
		cfg.ConfidenceThreshold = 0.75
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 25
	}
	if cfg.LeaseFor <= 0 {
		cfg.LeaseFor = 5 * time.Minute
	}
	cfg.RepairMode = normalizeRepairMode(cfg.RepairMode)
	if registry == nil {
		registry = providers.NewRegistry("mock")
		registry.Register("mock", providers.MockProvider{})
	}
	if modelConfig.Primary == "" {
		modelConfig.Primary = "mock/duraclaw"
	}
	return &Service{store: store, providers: registry, modelConfig: modelConfig, owner: owner, cfg: cfg}
}

func (s *Service) RunOnce(ctx context.Context) (int, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("tool evaluator store is not configured")
	}
	if _, err := s.store.QueueSuspiciousToolEvaluations(ctx, s.cfg.Limit, s.cfg.ConfidenceThreshold, s.cfg.Enabled); err != nil {
		return 0, err
	}
	items, err := s.store.ClaimToolEvaluations(ctx, s.owner, s.cfg.LeaseFor, s.cfg.Limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, item := range items {
		if err := s.evaluate(ctx, item); err != nil {
			_ = s.store.CompleteToolEvaluation(context.Background(), item.ID, db.ToolEvaluationUpdate{Status: "failed", Category: "evaluation_failed", Reason: err.Error(), Error: err})
			continue
		}
		processed++
	}
	return processed, nil
}

func (s *Service) EvaluateRun(ctx context.Context, runID string) (*db.ToolEvaluation, error) {
	item, err := s.store.QueueToolEvaluation(ctx, runID, []string{"manual"})
	if err != nil {
		return nil, err
	}
	claimed, err := s.store.ClaimToolEvaluation(ctx, item.ID, s.owner, s.cfg.LeaseFor)
	if err != nil {
		return nil, err
	}
	if err := s.evaluate(ctx, *claimed); err != nil {
		_ = s.store.CompleteToolEvaluation(ctx, claimed.ID, db.ToolEvaluationUpdate{Status: "failed", Category: "evaluation_failed", Reason: err.Error(), Error: err})
		return nil, err
	}
	return s.store.ToolEvaluationForRun(ctx, runID)
}

func (s *Service) evaluate(ctx context.Context, item db.ToolEvaluation) error {
	run, err := s.store.GetRun(ctx, item.RunID)
	if err != nil {
		return err
	}
	cfg := s.configForRun(ctx, run)
	if !cfg.Enabled && !hasManualSignal(item.SuspiciousSignals) {
		return s.store.CompleteToolEvaluation(ctx, item.ID, db.ToolEvaluationUpdate{Status: "completed", Category: "skipped", Reason: "tool evaluator disabled for run"})
	}
	trace, err := s.store.RunTrace(ctx, run.ID)
	if err != nil {
		return err
	}
	events, _ := s.store.Events(ctx, run.ID, 0)
	messages, _ := s.store.RecentMessages(ctx, run.CustomerID, run.SessionID, 8)
	expected := expectedToolsFromEvents(events)
	actual := actualTools(trace)
	fallback := deterministicVerdict(item, events, trace, expected, actual)
	v := fallback
	if llmVerdict, err := s.llmVerdict(ctx, cfg, run, item, trace, events, messages, expected, actual); err == nil && strings.TrimSpace(llmVerdict.Category) != "" {
		v = llmVerdict
	}
	if v.Category == "" {
		v.Category = "ok"
	}
	if v.Confidence <= 0 {
		v.Confidence = fallback.Confidence
	}
	repairAction, repairStatus := s.maybeRepair(ctx, cfg, run, v)
	return s.store.CompleteToolEvaluation(ctx, item.ID, db.ToolEvaluationUpdate{
		Status: "completed", Category: v.Category, Confidence: v.Confidence, ExpectedTools: v.ExpectedTools,
		ActualTools: v.ActualTools, Reason: v.Reason, RepairAction: repairAction, RepairStatus: repairStatus, Finding: v,
	})
}

func (s *Service) configForRun(ctx context.Context, run *db.Run) Config {
	cfg := s.cfg
	version, err := s.store.AgentInstanceVersion(ctx, run.AgentInstanceVersionID)
	if err != nil || version == nil || len(version.ProfileConfig) == 0 {
		return cfg
	}
	var profile profileConfig
	if err := json.Unmarshal(version.ProfileConfig, &profile); err != nil {
		return cfg
	}
	override := profile.ToolEvaluator
	if override.Model != "" {
		cfg.Model = override.Model
	}
	if override.Options != nil {
		cfg.Options = override.Options
	}
	if override.ConfidenceThreshold > 0 {
		cfg.ConfidenceThreshold = override.ConfidenceThreshold
	}
	if override.RepairMode != "" {
		cfg.RepairMode = normalizeRepairMode(override.RepairMode)
	}
	if override.Enabled {
		cfg.Enabled = true
	}
	return cfg
}

func (s *Service) llmVerdict(ctx context.Context, cfg Config, run *db.Run, item db.ToolEvaluation, trace *db.RunTrace, events []db.Event, messages []db.Message, expected, actual []string) (verdict, error) {
	modelConfig := s.modelConfig
	if strings.TrimSpace(cfg.Model) != "" {
		modelConfig.Primary = cfg.Model
		modelConfig.Fallbacks = nil
	}
	payload := map[string]any{
		"run":                run,
		"suspicious_signals": json.RawMessage(item.SuspiciousSignals),
		"expected_tools":     expected,
		"actual_tools":       actual,
		"trace":              trace,
		"events":             events,
		"recent_messages":    messages,
	}
	raw, _ := json.Marshal(payload)
	options := providers.MergeOptions(cfg.Options, map[string]any{"response_format": map[string]any{"type": "json_object"}, "purpose": "tool_correctness_evaluation"})
	resp, err := s.providers.ChatWithFallback(ctx, modelConfig, []providers.Message{
		{Role: "system", Content: "You evaluate whether an assistant should have called a tool. Return JSON only with category one of ok, missed_tool, wrong_tool, bad_args, tool_failed, false_success_claim, clarification_needed; expected_tools array; actual_tools array; confidence 0..1; reason; safe_repair boolean; repair_instruction. Do not recommend executing side-effect tools unless an existing reference id makes it idempotent."},
		{Role: "user", Content: string(raw)},
	}, nil, options)
	if err != nil {
		return verdict{}, err
	}
	var out verdict
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Response.Content)), &out); err != nil {
		return verdict{}, err
	}
	return out, nil
}

func deterministicVerdict(item db.ToolEvaluation, events []db.Event, trace *db.RunTrace, expected, actual []string) verdict {
	signals := signalSet(item.SuspiciousSignals)
	for _, call := range trace.ToolCalls {
		if call.State == "failed" {
			return verdict{Category: "tool_failed", ExpectedTools: expected, ActualTools: actual, Confidence: 0.9, Reason: "tool call failed"}
		}
	}
	for _, call := range trace.MCPCalls {
		if call.State == "failed" {
			return verdict{Category: "tool_failed", ExpectedTools: expected, ActualTools: actual, Confidence: 0.9, Reason: "MCP tool call failed"}
		}
	}
	if signals["selected_tool_missing"] && len(expected) > 0 && len(actual) == 0 {
		return verdict{Category: "missed_tool", ExpectedTools: expected, ActualTools: actual, Confidence: 0.82, Reason: "tool selection chose tools but no tool was called"}
	}
	for _, ev := range events {
		switch ev.Type {
		case "tool.required_missing":
			return verdict{Category: "missed_tool", ExpectedTools: expected, ActualTools: actual, Confidence: 0.88, Reason: "runtime required a tool but model did not call one"}
		case "tool.suppressed":
			return verdict{Category: "wrong_tool", ExpectedTools: expected, ActualTools: actual, Confidence: 0.75, Reason: "model requested unavailable or suppressed tool"}
		}
	}
	return verdict{Category: "ok", ExpectedTools: expected, ActualTools: actual, Confidence: 0.5, Reason: "no deterministic tool issue confirmed"}
}

func (s *Service) maybeRepair(ctx context.Context, cfg Config, run *db.Run, v verdict) (string, string) {
	if normalizeRepairMode(cfg.RepairMode) != "safe" || !v.SafeRepair || v.Confidence < cfg.ConfidenceThreshold {
		return "", "skipped"
	}
	if v.Category != "clarification_needed" && v.Category != "false_success_claim" {
		return "", "skipped"
	}
	text := strings.TrimSpace(v.RepairInstruction)
	if text == "" {
		if v.Category == "false_success_claim" {
			text = "I need to correct my previous response: I could not confirm that the action was completed."
		} else {
			text = "I need one more detail before I can complete that."
		}
	}
	payload, _ := json.Marshal(map[string]any{"text": text, "source": "tool_evaluator", "category": v.Category})
	_, _, err := s.store.CreateOutboundIntent(ctx, db.OutboundIntent{CustomerID: run.CustomerID, UserID: run.UserID, SessionID: run.SessionID, RunID: &run.ID, Type: "assistant_message", Payload: payload})
	if err != nil {
		return "safe_outbound", "failed"
	}
	_ = s.store.AddObservabilityEvent(ctx, run.CustomerID, run.ID, "tool_evaluator.repair_queued", map[string]any{"category": v.Category})
	return "safe_outbound", "queued"
}

func expectedToolsFromEvents(events []db.Event) []string {
	seen := map[string]bool{}
	var out []string
	for _, ev := range events {
		if ev.Type != "tool_selection.completed" {
			continue
		}
		var payload struct {
			SelectedTools []string `json:"selected_tools"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		for _, name := range payload.SelectedTools {
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func actualTools(trace *db.RunTrace) []string {
	if trace == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, call := range trace.ToolCalls {
		if call.ToolName != "" && !seen[call.ToolName] {
			seen[call.ToolName] = true
			out = append(out, call.ToolName)
		}
	}
	for _, call := range trace.MCPCalls {
		name := call.ServerName + "." + call.ToolName
		if strings.Trim(name, ".") != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func signalSet(raw json.RawMessage) map[string]bool {
	var values []string
	_ = json.Unmarshal(raw, &values)
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func hasManualSignal(raw json.RawMessage) bool {
	return signalSet(raw)["manual"]
}

func normalizeRepairMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "safe":
		return "safe"
	default:
		return "record_only"
	}
}

func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		return text
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}
