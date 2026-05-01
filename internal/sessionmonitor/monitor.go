package sessionmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/policy"
	"duraclaw/internal/providers"
)

type Store interface {
	ClaimIdleSessions(ctx context.Context, owner string, idleFor, leaseFor time.Duration, limit int) ([]db.MonitoredSession, error)
	CompleteSessionMonitor(ctx context.Context, customerID, sessionID string, monitoredThrough time.Time, activePattern any) error
	ReleaseSessionMonitor(ctx context.Context, customerID, sessionID string) error
	RecentMessages(ctx context.Context, customerID, sessionID string, limit int) ([]db.Message, error)
	RecentUserMessages(ctx context.Context, customerID, sessionID string, limit int) ([]db.Message, error)
	UpsertSessionSummary(ctx context.Context, customerID, sessionID, sourceRunID, summary string, metadata any) error
	ListMemories(ctx context.Context, customerID, userID string, limit int) ([]db.Memory, error)
	AddMemory(ctx context.Context, customerID, userID, sessionID, memoryType, content string, metadata any) (string, error)
	ListPreferences(ctx context.Context, customerID, userID string, limit int) ([]db.Preference, error)
	AddPreference(ctx context.Context, customerID, userID, sessionID, category, content string, condition, metadata any) (string, error)
	AddObservabilityEvent(ctx context.Context, customerID, runID, eventType string, payload any) error
	PolicyRulesForScope(ctx context.Context, customerID, agentInstanceID, enforcementMode string) ([]db.PolicyRule, error)
	RecordPolicyEvaluation(ctx context.Context, ev db.PolicyEvaluation) error
}

type Service struct {
	store               Store
	providers           *providers.Registry
	modelConfig         providers.ModelConfig
	policy              *policy.Engine
	owner               string
	idleFor             time.Duration
	leaseFor            time.Duration
	limit               int
	messageLimit        int
	compactionThreshold int
}

type CompactRequest struct {
	CustomerID   string `json:"customer_id"`
	SessionID    string `json:"session_id"`
	Force        bool   `json:"force"`
	MessageLimit int    `json:"message_limit,omitempty"`
}

type CompactResult struct {
	CustomerID    string         `json:"customer_id"`
	SessionID     string         `json:"session_id"`
	Summary       string         `json:"summary"`
	Compacted     bool           `json:"compacted"`
	MessageCount  int            `json:"message_count"`
	TranscriptLen int            `json:"transcript_chars"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Metadata      map[string]any `json:"metadata"`
}

func NewService(store Store, registry *providers.Registry, modelConfig providers.ModelConfig, owner string) *Service {
	if owner == "" {
		owner = "duraclaw-session-monitor"
	}
	if registry == nil {
		registry = providers.NewRegistry("mock")
		registry.Register("mock", providers.MockProvider{})
	}
	if modelConfig.Primary == "" {
		modelConfig.Primary = "mock/duraclaw"
	}
	return &Service{
		store: store, providers: registry, modelConfig: modelConfig, policy: policy.NewEngine(store),
		owner: owner, idleFor: 30 * time.Minute, leaseFor: 5 * time.Minute, limit: 25, messageLimit: 40, compactionThreshold: 12000,
	}
}

func (s *Service) WithIdleFor(d time.Duration) *Service {
	if d > 0 {
		s.idleFor = d
	}
	return s
}

func (s *Service) WithLimit(limit int) *Service {
	if limit > 0 {
		s.limit = limit
	}
	return s
}

func (s *Service) WithMessageLimit(limit int) *Service {
	if limit > 0 {
		s.messageLimit = limit
	}
	return s
}

func (s *Service) WithCompactionThreshold(chars int) *Service {
	if chars > 0 {
		s.compactionThreshold = chars
	}
	return s
}

func (s *Service) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sessions, err := s.store.ClaimIdleSessions(ctx, s.owner, s.idleFor, s.leaseFor, s.limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, session := range sessions {
		if err := s.processSession(ctx, now, session); err != nil {
			_ = s.store.AddObservabilityEvent(context.Background(), session.CustomerID, "", "session_monitor_failed", map[string]any{"session_id": session.SessionID, "error": err.Error()})
			_ = s.store.ReleaseSessionMonitor(context.Background(), session.CustomerID, session.SessionID)
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (s *Service) CompactSession(ctx context.Context, req CompactRequest) (*CompactResult, error) {
	req.CustomerID = strings.TrimSpace(req.CustomerID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.CustomerID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("customer_id and session_id are required")
	}
	limit := req.MessageLimit
	if limit <= 0 {
		limit = s.messageLimit
	}
	messages, err := s.store.RecentMessages(ctx, req.CustomerID, req.SessionID, limit)
	if err != nil {
		return nil, err
	}
	transcript := transcript(messages)
	result := &CompactResult{
		CustomerID:    req.CustomerID,
		SessionID:     req.SessionID,
		MessageCount:  len(messages),
		TranscriptLen: len(transcript),
		Metadata: map[string]any{
			"strategy":      "manual_llm_compaction",
			"message_count": len(messages),
			"requested_at":  time.Now().UTC().Format(time.RFC3339),
			"forced":        req.Force,
		},
	}
	if strings.TrimSpace(transcript) == "" {
		return result, nil
	}
	if !req.Force && len(transcript) < s.compactionThreshold {
		return result, nil
	}
	summary, provider, model, err := s.extractSummaryWithMetadata(ctx, transcript)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return result, nil
	}
	result.Summary = summary
	result.Compacted = true
	result.Provider = provider
	result.Model = model
	result.Metadata["provider"] = provider
	result.Metadata["model"] = model
	if err := s.store.UpsertSessionSummary(ctx, req.CustomerID, req.SessionID, "", summary, result.Metadata); err != nil {
		return nil, err
	}
	_ = s.store.AddObservabilityEvent(ctx, req.CustomerID, "", "session_context_compacted", map[string]any{"session_id": req.SessionID, "messages": len(messages), "summary_chars": len(summary), "manual": true})
	return result, nil
}

func (s *Service) processSession(ctx context.Context, now time.Time, session db.MonitoredSession) error {
	_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_monitor_claimed", map[string]any{"session_id": session.SessionID})
	monitoredThrough := session.UpdatedAt
	if session.LastMessageAt != nil {
		monitoredThrough = *session.LastMessageAt
	}
	messages, err := s.store.RecentMessages(ctx, session.CustomerID, session.SessionID, s.messageLimit)
	if err != nil {
		return err
	}
	messages = monitoredWindow(messages, session.LastMonitoredAt, monitoredThrough)
	userMessages, err := s.store.RecentUserMessages(ctx, session.CustomerID, session.SessionID, s.messageLimit)
	if err != nil {
		return err
	}
	userMessages = monitoredWindow(userMessages, session.LastMonitoredAt, monitoredThrough)
	transcript := transcript(messages)
	if transcript != "" && len(transcript) >= s.compactionThreshold {
		if summary, err := s.extractSummary(ctx, transcript); err == nil && strings.TrimSpace(summary) != "" {
			if err := s.store.UpsertSessionSummary(ctx, session.CustomerID, session.SessionID, "", summary, map[string]any{"strategy": "idle_llm_compaction", "message_count": len(messages), "compacted_at": now.Format(time.RFC3339)}); err != nil {
				return err
			}
			_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_context_compacted", map[string]any{"session_id": session.SessionID, "messages": len(messages), "summary_chars": len(summary)})
		}
	}
	if transcript != "" {
		if err := s.extractMemoryAndPreferences(ctx, now, session, transcript); err != nil {
			return err
		}
	}
	pattern := activePattern(session.ActivePattern, userMessages, now)
	if err := s.store.CompleteSessionMonitor(ctx, session.CustomerID, session.SessionID, monitoredThrough, pattern); err != nil {
		return err
	}
	_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_active_pattern_updated", map[string]any{"session_id": session.SessionID})
	return nil
}

func monitoredWindow(messages []db.Message, after *time.Time, through time.Time) []db.Message {
	if after == nil && through.IsZero() {
		return messages
	}
	out := make([]db.Message, 0, len(messages))
	for _, msg := range messages {
		if after != nil && !msg.CreatedAt.After(*after) {
			continue
		}
		if !through.IsZero() && msg.CreatedAt.After(through) {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func (s *Service) extractSummary(ctx context.Context, transcript string) (string, error) {
	summary, _, _, err := s.extractSummaryWithMetadata(ctx, transcript)
	return summary, err
}

func (s *Service) extractSummaryWithMetadata(ctx context.Context, transcript string) (string, string, string, error) {
	resp, err := s.providers.ChatWithFallback(ctx, s.modelConfig, []providers.Message{
		{Role: "system", Content: "Summarize this assistant session for future prompt context. Keep durable facts, open threads, and user preferences concise."},
		{Role: "user", Content: transcript},
	}, nil, map[string]any{"purpose": "session_compaction"})
	if err != nil {
		return "", "", "", err
	}
	return strings.TrimSpace(resp.Response.Content), resp.Provider, resp.Model, nil
}

type extractionResult struct {
	Memories []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"memories"`
	Preferences []struct {
		Category  string         `json:"category"`
		Content   string         `json:"content"`
		Condition map[string]any `json:"condition"`
	} `json:"preferences"`
}

func (s *Service) extractMemoryAndPreferences(ctx context.Context, now time.Time, session db.MonitoredSession, transcript string) error {
	resp, err := s.providers.ChatWithFallback(ctx, s.modelConfig, []providers.Message{
		{Role: "system", Content: "Extract stable user memories and conditional preferences. Return JSON only with arrays: memories[{type,content}], preferences[{category,content,condition}]. Stable memories are facts that rarely change. Preferences may be conditional."},
		{Role: "user", Content: transcript},
	}, nil, map[string]any{"response_format": "json_object", "purpose": "idle_memory_preference_extraction"})
	if err != nil {
		return err
	}
	var result extractionResult
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Response.Content)), &result); err != nil {
		return nil
	}
	existingMemories, _ := s.store.ListMemories(ctx, session.CustomerID, session.UserID, 200)
	existingPreferences, _ := s.store.ListPreferences(ctx, session.CustomerID, session.UserID, 200)
	seenMemories := map[string]bool{}
	for _, m := range existingMemories {
		seenMemories[normalize(m.Content)] = true
	}
	seenPreferences := map[string]bool{}
	for _, p := range existingPreferences {
		seenPreferences[normalize(p.Category+" "+p.Content+" "+string(p.Condition))] = true
	}
	writtenMemories := 0
	for _, m := range result.Memories {
		content := strings.TrimSpace(m.Content)
		if content == "" || seenMemories[normalize(content)] {
			continue
		}
		if _, err := s.policy.Enforce(ctx, "pre_memory_write", policy.Context{CustomerID: session.CustomerID, UserID: session.UserID, AgentInstanceID: session.AgentInstanceID, SessionID: session.SessionID, Content: content}); err != nil {
			continue
		}
		memoryType := strings.TrimSpace(m.Type)
		if memoryType == "" {
			memoryType = "fact"
		}
		if _, err := s.store.AddMemory(ctx, session.CustomerID, session.UserID, session.SessionID, memoryType, content, map[string]any{"source": "session_monitor", "extracted_at": now.Format(time.RFC3339), "provider": resp.Provider, "model": resp.Model}); err != nil {
			return err
		}
		writtenMemories++
	}
	writtenPreferences := 0
	for _, p := range result.Preferences {
		content := strings.TrimSpace(p.Content)
		category := strings.TrimSpace(p.Category)
		if category == "" {
			category = "general"
		}
		keyBytes, _ := json.Marshal(p.Condition)
		key := normalize(category + " " + content + " " + string(keyBytes))
		if content == "" || seenPreferences[key] {
			continue
		}
		if _, err := s.policy.Enforce(ctx, "pre_memory_write", policy.Context{CustomerID: session.CustomerID, UserID: session.UserID, AgentInstanceID: session.AgentInstanceID, SessionID: session.SessionID, Content: content}); err != nil {
			continue
		}
		if _, err := s.store.AddPreference(ctx, session.CustomerID, session.UserID, session.SessionID, category, content, p.Condition, map[string]any{"source": "session_monitor", "extracted_at": now.Format(time.RFC3339), "provider": resp.Provider, "model": resp.Model}); err != nil {
			return err
		}
		writtenPreferences++
	}
	_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_memory_extracted", map[string]any{"session_id": session.SessionID, "memories": writtenMemories, "preferences": writtenPreferences})
	return nil
}

func transcript(messages []db.Message) string {
	var lines []string
	for _, msg := range messages {
		text := messageText(msg.Content)
		if strings.TrimSpace(text) != "" {
			lines = append(lines, msg.Role+": "+strings.TrimSpace(text))
		}
	}
	return strings.Join(lines, "\n")
}

func messageText(raw json.RawMessage) string {
	var payload struct {
		Text  string           `json:"text"`
		Parts []db.ContentPart `json:"parts"`
	}
	_ = json.Unmarshal(raw, &payload)
	if strings.TrimSpace(payload.Text) != "" {
		return payload.Text
	}
	var parts []string
	for _, part := range payload.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func activePattern(raw json.RawMessage, messages []db.Message, now time.Time) map[string]any {
	var pattern struct {
		Hours      map[string]int `json:"hours"`
		Weekdays   map[string]int `json:"weekdays"`
		LastActive string         `json:"last_active"`
		UpdatedAt  string         `json:"updated_at"`
	}
	_ = json.Unmarshal(raw, &pattern)
	if pattern.Hours == nil {
		pattern.Hours = map[string]int{}
	}
	if pattern.Weekdays == nil {
		pattern.Weekdays = map[string]int{}
	}
	for _, msg := range messages {
		t := msg.CreatedAt.UTC()
		pattern.Hours[fmt.Sprintf("%02d", t.Hour())]++
		pattern.Weekdays[t.Weekday().String()]++
		pattern.LastActive = t.Format(time.RFC3339)
	}
	pattern.UpdatedAt = now.UTC().Format(time.RFC3339)
	return map[string]any{"hours": pattern.Hours, "weekdays": pattern.Weekdays, "last_active": pattern.LastActive, "updated_at": pattern.UpdatedAt}
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func extractJSONObject(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return raw
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return raw[start : end+1]
	}
	return raw
}
