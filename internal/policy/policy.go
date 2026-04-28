package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"duraclaw/internal/db"
)

type ArtifactRule struct {
	MaxSizeBytes int64
	MediaTypes   map[string]bool
}

func AllowArtifact(size int64, mediaType string, rule ArtifactRule) error {
	if rule.MaxSizeBytes > 0 && size > rule.MaxSizeBytes {
		return fmt.Errorf("artifact exceeds max size")
	}
	if len(rule.MediaTypes) > 0 && !rule.MediaTypes[mediaType] {
		return fmt.Errorf("media type %q is not allowed", mediaType)
	}
	return nil
}

func RejectRawArtifactMetadata(metadata map[string]any) error {
	for key, value := range metadata {
		switch key {
		case "raw", "raw_payload", "binary", "bytes", "base64", "data_uri":
			return fmt.Errorf("artifact metadata contains raw payload field %q", key)
		}
		if nested, ok := value.(map[string]any); ok {
			if err := RejectRawArtifactMetadata(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

type RuleStore interface {
	PolicyRulesForScope(ctx context.Context, customerID, agentInstanceID, enforcementMode string) ([]db.PolicyRule, error)
	RecordPolicyEvaluation(ctx context.Context, ev db.PolicyEvaluation) error
}

type Context struct {
	CustomerID       string         `json:"customer_id,omitempty"`
	UserID           string         `json:"user_id,omitempty"`
	AgentInstanceID  string         `json:"agent_instance_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	RunID            string         `json:"run_id,omitempty"`
	StepID           string         `json:"step_id,omitempty"`
	WorkflowRunID    string         `json:"workflow_run_id,omitempty"`
	WorkflowNodeKey  string         `json:"workflow_node_key,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	WorkflowID       string         `json:"workflow_id,omitempty"`
	ArtifactID       string         `json:"artifact_id,omitempty"`
	Processor        string         `json:"processor,omitempty"`
	Content          string         `json:"content,omitempty"`
	AdditionalFields map[string]any `json:"additional_fields,omitempty"`
}

func (pc Context) Redacted() Context {
	pc.Content = RedactString(pc.Content)
	pc.AdditionalFields = RedactMap(pc.AdditionalFields)
	return pc
}

type Decision struct {
	Action       string          `json:"action"`
	Reason       string          `json:"reason,omitempty"`
	Instructions []string        `json:"instructions,omitempty"`
	MatchedRules []db.PolicyRule `json:"matched_rules,omitempty"`
}

type Engine struct {
	store RuleStore
}

func NewEngine(store RuleStore) *Engine {
	return &Engine{store: store}
}

func (e *Engine) Evaluate(ctx context.Context, mode string, pc Context) (Decision, error) {
	if e == nil || e.store == nil || mode == "" {
		return Decision{Action: "allow"}, nil
	}
	rules, err := e.store.PolicyRulesForScope(ctx, pc.CustomerID, pc.AgentInstanceID, mode)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{Action: "allow"}
	for _, rule := range rules {
		if !ruleMatches(rule, pc) {
			continue
		}
		decision.MatchedRules = append(decision.MatchedRules, rule)
		if strings.TrimSpace(rule.InstructionText) != "" {
			decision.Instructions = append(decision.Instructions, rule.InstructionText)
		}
		reason := rule.RuleType
		if strings.TrimSpace(rule.InstructionText) != "" {
			reason = rule.InstructionText
		}
		if err := e.record(ctx, mode, pc, rule, rule.Action, reason); err != nil {
			return Decision{}, err
		}
		decision = strongerDecision(decision, rule.Action, reason)
	}
	if len(decision.MatchedRules) == 0 {
		_ = e.record(ctx, mode, pc, db.PolicyRule{}, "allow", "no matching policy rule")
	}
	return decision, nil
}

func (e *Engine) PromptInstructions(ctx context.Context, pc Context) ([]string, error) {
	decision, err := e.Evaluate(ctx, "prompt", pc)
	if err != nil {
		return nil, err
	}
	return decision.Instructions, nil
}

func (e *Engine) Enforce(ctx context.Context, mode string, pc Context) (Decision, error) {
	decision, err := e.Evaluate(ctx, mode, pc)
	if err != nil {
		return decision, err
	}
	switch decision.Action {
	case "deny":
		if decision.Reason == "" {
			decision.Reason = "policy denied"
		}
		return decision, fmt.Errorf("policy denied at %s: %s", mode, decision.Reason)
	default:
		return decision, nil
	}
}

func (e *Engine) record(ctx context.Context, mode string, pc Context, rule db.PolicyRule, action, reason string) error {
	if e == nil || e.store == nil {
		return nil
	}
	payload, _ := json.Marshal(pc.Redacted())
	ev := db.PolicyEvaluation{
		WorkflowNodeKey: pc.WorkflowNodeKey,
		EnforcementMode: mode,
		Decision:        action,
		Reason:          reason,
		Payload:         payload,
	}
	if pc.RunID != "" {
		ev.RunID = &pc.RunID
	}
	if pc.StepID != "" {
		ev.StepID = &pc.StepID
	}
	if pc.WorkflowRunID != "" {
		ev.WorkflowRunID = &pc.WorkflowRunID
	}
	if rule.PolicyPackID != "" {
		ev.PolicyPackID = &rule.PolicyPackID
	}
	if rule.ID != "" {
		ev.PolicyRuleID = &rule.ID
	}
	return e.store.RecordPolicyEvaluation(ctx, ev)
}

func RedactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if sensitiveKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = RedactValue(value)
	}
	return out
}

func RedactValue(value any) any {
	switch v := value.(type) {
	case string:
		return RedactString(v)
	case map[string]any:
		return RedactMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = RedactValue(item)
		}
		return out
	default:
		return value
	}
}

func RedactString(value string) string {
	value = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*([^\s,;]+)`).ReplaceAllString(value, `$1=[REDACTED]`)
	value = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`).ReplaceAllString(value, "[REDACTED_EMAIL]")
	return regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`).ReplaceAllString(value, "[REDACTED_NUMBER]")
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, needle := range []string{"password", "secret", "token", "api_key", "apikey", "authorization", "cookie", "credential"} {
		if strings.Contains(key, needle) {
			return true
		}
	}
	return false
}

func strongerDecision(current Decision, action, reason string) Decision {
	if strength(action) > strength(current.Action) {
		current.Action = action
		current.Reason = reason
	}
	return current
}

func strength(action string) int {
	switch action {
	case "deny":
		return 4
	case "require_clarification":
		return 3
	case "modify":
		return 2
	default:
		return 1
	}
}

func ruleMatches(rule db.PolicyRule, pc Context) bool {
	if len(rule.Condition) == 0 || string(rule.Condition) == "{}" {
		return true
	}
	var condition map[string]any
	if err := json.Unmarshal(rule.Condition, &condition); err != nil {
		return false
	}
	if raw, ok := condition["equals"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return fmt.Sprint(valueAt(pc, key)) == fmt.Sprint(raw["value"])
	}
	if raw, ok := condition["not_equals"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return fmt.Sprint(valueAt(pc, key)) != fmt.Sprint(raw["value"])
	}
	if raw, ok := condition["contains"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		needle := strings.ToLower(fmt.Sprint(raw["value"]))
		return needle != "" && strings.Contains(strings.ToLower(fmt.Sprint(valueAt(pc, key))), needle)
	}
	if raw, ok := condition["prefix"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return strings.HasPrefix(fmt.Sprint(valueAt(pc, key)), fmt.Sprint(raw["value"]))
	}
	if raw, ok := condition["suffix"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return strings.HasSuffix(fmt.Sprint(valueAt(pc, key)), fmt.Sprint(raw["value"]))
	}
	if raw, ok := condition["in"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		value := fmt.Sprint(valueAt(pc, key))
		if list, ok := raw["values"].([]any); ok {
			for _, candidate := range list {
				if value == fmt.Sprint(candidate) {
					return true
				}
			}
		}
		return false
	}
	if raw, ok := condition["matches"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		pattern, _ := raw["pattern"].(string)
		if pattern == "" {
			return false
		}
		matched, err := regexp.MatchString(pattern, fmt.Sprint(valueAt(pc, key)))
		return err == nil && matched
	}
	if raw, ok := condition["all"].([]any); ok {
		for _, item := range raw {
			if !conditionMapMatches(item, pc) {
				return false
			}
		}
		return true
	}
	if raw, ok := condition["any"].([]any); ok {
		for _, item := range raw {
			if conditionMapMatches(item, pc) {
				return true
			}
		}
		return false
	}
	if raw, ok := condition["not"]; ok {
		return !conditionMapMatches(raw, pc)
	}
	return true
}

func conditionMapMatches(raw any, pc Context) bool {
	condition, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	b, _ := json.Marshal(condition)
	return ruleMatches(db.PolicyRule{Condition: b}, pc)
}

func valueAt(pc Context, key string) any {
	switch key {
	case "customer_id":
		return pc.CustomerID
	case "user_id":
		return pc.UserID
	case "agent_instance_id":
		return pc.AgentInstanceID
	case "session_id":
		return pc.SessionID
	case "run_id":
		return pc.RunID
	case "tool_name":
		return pc.ToolName
	case "workflow_id":
		return pc.WorkflowID
	case "artifact_id":
		return pc.ArtifactID
	case "processor":
		return pc.Processor
	case "content":
		return pc.Content
	default:
		if pc.AdditionalFields != nil {
			return pc.AdditionalFields[key]
		}
		return nil
	}
}
