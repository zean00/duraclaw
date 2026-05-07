package sessionmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/embeddings"
	"duraclaw/internal/policy"
	"duraclaw/internal/providers"
)

type Store interface {
	ClaimIdleSessions(ctx context.Context, owner string, idleFor, leaseFor time.Duration, limit int) ([]db.MonitoredSession, error)
	CompleteSessionMonitor(ctx context.Context, customerID, sessionID string, monitoredThrough time.Time, activePattern any) error
	ReleaseSessionMonitor(ctx context.Context, customerID, sessionID string) error
	RecentMessages(ctx context.Context, customerID, sessionID string, limit int) ([]db.Message, error)
	RecentUserMessages(ctx context.Context, customerID, sessionID string, limit int) ([]db.Message, error)
	MonitoredSession(ctx context.Context, customerID, sessionID string) (*db.MonitoredSession, error)
	UpsertSessionSummary(ctx context.Context, customerID, sessionID, sourceRunID, summary string, metadata any) error
	ListMemories(ctx context.Context, customerID, userID string, limit int) ([]db.Memory, error)
	AddMemory(ctx context.Context, customerID, userID, sessionID, memoryType, content string, metadata any) (string, error)
	SetMemoryEmbedding(ctx context.Context, memoryID, customerID, userID string, embedding []float32) error
	ListPreferences(ctx context.Context, customerID, userID string, limit int) ([]db.Preference, error)
	AddPreference(ctx context.Context, customerID, userID, sessionID, category, content string, condition, metadata any) (string, error)
	SetPreferenceEmbedding(ctx context.Context, preferenceID, customerID, userID string, embedding []float32) error
	AddObservabilityEvent(ctx context.Context, customerID, runID, eventType string, payload any) error
	PolicyRulesForScope(ctx context.Context, customerID, agentInstanceID, enforcementMode string) ([]db.PolicyRule, error)
	RecordPolicyEvaluation(ctx context.Context, ev db.PolicyEvaluation) error
}

type Service struct {
	store               Store
	providers           *providers.Registry
	modelConfig         providers.ModelConfig
	embedder            embeddings.Provider
	policy              *policy.Engine
	owner               string
	idleFor             time.Duration
	leaseFor            time.Duration
	limit               int
	messageLimit        int
	compactionThreshold int
}

type CompactRequest struct {
	CustomerID     string `json:"customer_id"`
	SessionID      string `json:"session_id"`
	Force          bool   `json:"force"`
	ExtractProfile bool   `json:"extract_profile,omitempty"`
	MessageLimit   int    `json:"message_limit,omitempty"`
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

func (s *Service) WithEmbedder(embedder embeddings.Provider) *Service {
	if embedder != nil {
		s.embedder = embedder
	}
	return s
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
			"strategy":        "manual_llm_compaction",
			"message_count":   len(messages),
			"requested_at":    time.Now().UTC().Format(time.RFC3339),
			"forced":          req.Force,
			"extract_profile": req.ExtractProfile,
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
	if req.ExtractProfile {
		session, err := s.store.MonitoredSession(ctx, req.CustomerID, req.SessionID)
		if err != nil {
			return nil, err
		}
		memories, preferences, err := s.extractMemoryAndPreferences(ctx, time.Now().UTC(), *session, transcript)
		if err != nil {
			return nil, err
		}
		result.Metadata["profile_extraction"] = map[string]any{"memories": memories, "preferences": preferences}
	}
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
		if _, _, err := s.extractMemoryAndPreferences(ctx, now, session, transcript); err != nil {
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
	Memories    []extractedMemory     `json:"memories"`
	Preferences []extractedPreference `json:"preferences"`
}

type extractedMemory struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type extractedPreference struct {
	Category  string         `json:"category"`
	Content   string         `json:"content"`
	Condition map[string]any `json:"condition"`
}

func (s *Service) extractMemoryAndPreferences(ctx context.Context, now time.Time, session db.MonitoredSession, transcript string) (int, int, error) {
	resp, err := s.providers.ChatWithFallback(ctx, s.modelConfig, []providers.Message{
		{Role: "system", Content: "Extract stable user memories and conditional preferences explicitly stated by the user. Return JSON only: {\"memories\":[{\"type\":\"fact\",\"content\":\"...\"}],\"preferences\":[{\"category\":\"...\",\"content\":\"...\",\"condition\":{}}]}. Include durable profile facts such as family relationships, names, and identity details. Include preferences such as what the user wants to be called, language/style preferences, and recurring likes/dislikes. Do not extract reminders, temporary moods, assistant suggestions, or generic notes/bookmarks as profile memory."},
		{Role: "user", Content: transcript},
	}, nil, map[string]any{"response_format": map[string]any{"type": "json_object"}, "purpose": "idle_memory_preference_extraction"})
	if err != nil {
		return 0, 0, err
	}
	var result extractionResult
	if err := json.Unmarshal([]byte(extractJSONObject(resp.Response.Content)), &result); err != nil {
		_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_memory_extraction_failed", map[string]any{"session_id": session.SessionID, "error": err.Error(), "raw_preview": preview(resp.Response.Content, 500)})
		return 0, 0, nil
	}
	applyProfileExtractionFallbacks(transcript, &result)
	existingMemories, _ := s.store.ListMemories(ctx, session.CustomerID, session.UserID, 200)
	existingPreferences, _ := s.store.ListPreferences(ctx, session.CustomerID, session.UserID, 200)
	seenMemories := map[string]bool{}
	for _, m := range existingMemories {
		seenMemories[normalize(m.Content)] = true
		for _, key := range profileSemanticKeys(m.Content, m.Type) {
			seenMemories[key] = true
		}
	}
	seenPreferences := map[string]bool{}
	for _, p := range existingPreferences {
		seenPreferences[normalize(p.Category+" "+p.Content+" "+string(p.Condition))] = true
		for _, key := range profileSemanticKeys(p.Content, p.Category) {
			seenPreferences[key] = true
		}
	}
	writtenMemories := 0
	writtenPreferences := 0
	for _, m := range result.Memories {
		content := strings.TrimSpace(m.Content)
		if content == "" || skipMemoryExtraction(content) || seenMemories[normalize(content)] || seenProfileSemantic(seenMemories, content, memoryTypeForCandidate(m.Type)) {
			continue
		}
		if _, err := s.policy.Enforce(ctx, "pre_memory_write", policy.Context{CustomerID: session.CustomerID, UserID: session.UserID, AgentInstanceID: session.AgentInstanceID, SessionID: session.SessionID, Content: content}); err != nil {
			continue
		}
		memoryType := strings.TrimSpace(m.Type)
		if memoryType == "" {
			memoryType = "fact"
		}
		memoryID, err := s.store.AddMemory(ctx, session.CustomerID, session.UserID, session.SessionID, memoryType, content, map[string]any{"source": "session_monitor", "extracted_at": now.Format(time.RFC3339), "provider": resp.Provider, "model": resp.Model})
		if err != nil {
			return writtenMemories, writtenPreferences, err
		}
		s.setMemoryEmbeddingBestEffort(ctx, session, memoryID, content)
		seenMemories[normalize(content)] = true
		for _, key := range profileSemanticKeys(content, memoryType) {
			seenMemories[key] = true
		}
		writtenMemories++
	}
	for _, p := range result.Preferences {
		content := strings.TrimSpace(p.Content)
		category := strings.TrimSpace(p.Category)
		if category == "" {
			category = "general"
		}
		keyBytes, _ := json.Marshal(p.Condition)
		key := normalize(category + " " + content + " " + string(keyBytes))
		if content == "" || skipProfileExtraction(content) || seenPreferences[key] || seenProfileSemantic(seenPreferences, content, category) {
			continue
		}
		if _, err := s.policy.Enforce(ctx, "pre_memory_write", policy.Context{CustomerID: session.CustomerID, UserID: session.UserID, AgentInstanceID: session.AgentInstanceID, SessionID: session.SessionID, Content: content}); err != nil {
			continue
		}
		preferenceID, err := s.store.AddPreference(ctx, session.CustomerID, session.UserID, session.SessionID, category, content, p.Condition, map[string]any{"source": "session_monitor", "extracted_at": now.Format(time.RFC3339), "provider": resp.Provider, "model": resp.Model})
		if err != nil {
			return writtenMemories, writtenPreferences, err
		}
		s.setPreferenceEmbeddingBestEffort(ctx, session, preferenceID, content)
		seenPreferences[key] = true
		for _, semanticKey := range profileSemanticKeys(content, category) {
			seenPreferences[semanticKey] = true
		}
		writtenPreferences++
	}
	_ = s.store.AddObservabilityEvent(ctx, session.CustomerID, "", "session_memory_extracted", map[string]any{"session_id": session.SessionID, "memories": writtenMemories, "preferences": writtenPreferences})
	return writtenMemories, writtenPreferences, nil
}

func (s *Service) setMemoryEmbeddingBestEffort(ctx context.Context, session db.MonitoredSession, memoryID, content string) {
	if s == nil || s.embedder == nil || strings.TrimSpace(memoryID) == "" || strings.TrimSpace(content) == "" {
		return
	}
	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return
	}
	_ = s.store.SetMemoryEmbedding(ctx, memoryID, session.CustomerID, session.UserID, embedding)
}

func (s *Service) setPreferenceEmbeddingBestEffort(ctx context.Context, session db.MonitoredSession, preferenceID, content string) {
	if s == nil || s.embedder == nil || strings.TrimSpace(preferenceID) == "" || strings.TrimSpace(content) == "" {
		return
	}
	embedding, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return
	}
	_ = s.store.SetPreferenceEmbedding(ctx, preferenceID, session.CustomerID, session.UserID, embedding)
}

func memoryTypeForCandidate(memoryType string) string {
	memoryType = strings.TrimSpace(memoryType)
	if memoryType == "" {
		return "fact"
	}
	return memoryType
}

func skipMemoryExtraction(content string) bool {
	if skipProfileExtraction(content) {
		return true
	}
	text := normalize(content)
	return strings.Contains(text, "nama panggilan") || strings.Contains(text, "dipanggil") || strings.Contains(text, "called")
}

func skipProfileExtraction(content string) bool {
	text := normalize(content)
	if text == "" {
		return true
	}
	if looksLikeOneOffCaptureNote(text) {
		return true
	}
	// Avoid storing unsupported inferences as profile.
	if strings.Contains(text, "masih hidup") || strings.Contains(text, "still alive") {
		return true
	}
	if strings.Contains(text, "memiliki") && strings.Contains(text, "ibu") {
		return true
	}
	if strings.Contains(text, "has") && strings.Contains(text, "mother") {
		return true
	}
	return false
}

func looksLikeOneOffCaptureNote(text string) bool {
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") || strings.Contains(text, "www.") || strings.Contains(text, "github.com") {
		return true
	}
	for _, token := range []string{"repo", "repository", "link", "url", "product", "produk"} {
		if strings.Contains(" "+text+" ", " "+token+" ") {
			return true
		}
	}
	placeOrLocation := false
	for _, token := range []string{"place", "places", "tempat", "location", "lokasi", "address", "alamat"} {
		if strings.Contains(" "+text+" ", " "+token+" ") {
			placeOrLocation = true
			break
		}
	}
	if !placeOrLocation {
		return false
	}
	return strings.Contains(text, " namanya ") || strings.Contains(text, " nama ") || strings.Contains(text, " jalan ") || strings.Contains(text, " jl.")
}

func seenProfileSemantic(seen map[string]bool, content, category string) bool {
	for _, key := range profileSemanticKeys(content, category) {
		if seen[key] {
			return true
		}
	}
	return false
}

func profileSemanticKeys(content, category string) []string {
	var out []string
	for _, name := range profileNameCandidates(category + " " + content) {
		out = append(out, "profile:addressing:"+normalize(name))
	}
	for _, name := range childNameCandidates(content) {
		out = append(out, "profile:child:"+normalize(name))
	}
	return out
}

func applyProfileExtractionFallbacks(transcript string, result *extractionResult) {
	if strings.TrimSpace(transcript) == "" || result == nil {
		return
	}
	for _, nickname := range profileNameCandidates(transcript) {
		if hasExtractedPreference(result.Preferences, "called "+nickname) || hasExtractedPreference(result.Preferences, nickname) {
			continue
		}
		result.Preferences = append(result.Preferences, extractedPreference{Category: "addressing", Content: "User prefers to be called " + nickname, Condition: map[string]any{}})
	}
	for _, child := range childNameCandidates(transcript) {
		if hasExtractedMemory(result.Memories, child, "anak") || hasExtractedMemory(result.Memories, child, "child") {
			continue
		}
		result.Memories = append(result.Memories, extractedMemory{Type: "family", Content: child + " is the user's child"})
	}
}

func profileNameCandidates(text string) []string {
	patterns := []string{
		`(?i)\bpanggil\s+(?:aku|saya|gue|gua)\s+([\pL][\pL .'-]{1,40}?)(?:\s+ya|\s+mulai|\?|$)`,
		`(?i)\b(?:call|address)\s+me\s+([\pL][\pL .'-]{1,40}?)(?:\s+please|\.|,|\?|$)`,
		`(?i)\b(?:dipanggil|called|addressed as)\s+([\pL][\pL .'-]{1,40}?)(?:\.|,|\?|$)`,
	}
	return regexpCaptureAllPatterns(text, patterns...)
}

func childNameCandidates(text string) []string {
	patterns := []string{
		`(?i)(?:^|[\s"'(])([\pL][\pL'-]{1,30})\s+anak(?:ku| saya| gue| gua| pengguna)?\b`,
		`(?i)\banak(?:ku| saya| gue| gua| pengguna)?\s+(?:namanya|bernama)\s+([\pL][\pL .'-]{1,40})(?:\s|\.|,|$)`,
		`(?i)\b(?:my|user(?:'?s| s))\s+child\s+(?:is\s+|named\s+)?([\pL][\pL .'-]{1,40})(?:\s|\.|,|$)`,
		`(?i)\b([\pL][\pL .'-]{1,40})\s+is\s+(?:the\s+)?user(?:'?s| s)\s+child\b`,
	}
	return regexpCaptureAllPatterns(text, patterns...)
}

func hasExtractedMemory(items []extractedMemory, needles ...string) bool {
	for _, item := range items {
		text := normalize(item.Content)
		matched := true
		for _, needle := range needles {
			if !strings.Contains(text, normalize(needle)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func hasExtractedPreference(items []extractedPreference, needles ...string) bool {
	for _, item := range items {
		text := normalize(item.Category + " " + item.Content)
		matched := true
		for _, needle := range needles {
			if !strings.Contains(text, normalize(needle)) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func regexpCaptureAll(text, pattern string) []string {
	return regexpCaptureAllPatterns(text, pattern)
}

func regexpCaptureAllPatterns(text string, patterns ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		for _, match := range re.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			value := cleanProfileCandidate(match[1])
			if value == "" || seen[normalize(value)] {
				continue
			}
			seen[normalize(value)] = true
			out = append(out, value)
		}
	}
	return out
}

func cleanProfileCandidate(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"'.,!? `)
	lower := strings.ToLower(value)
	for _, sep := range []string{" and ", " dan ", " lalu ", " terus "} {
		if idx := strings.Index(lower, sep); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
			lower = strings.ToLower(value)
		}
	}
	return strings.Trim(value, `"'.,!? `)
}

func preview(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func transcript(messages []db.Message) string {
	var lines []string
	for _, msg := range messages {
		if messageExcludedFromTranscript(msg.Content) {
			continue
		}
		text := messageText(msg.Content)
		if strings.TrimSpace(text) != "" {
			lines = append(lines, msg.Role+": "+strings.TrimSpace(text))
		}
	}
	return strings.Join(lines, "\n")
}

func messageExcludedFromTranscript(raw json.RawMessage) bool {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if boolMapValue(payload, "context_excluded", "contextExcluded") {
		return true
	}
	if boolMapValue(mapValue(payload, "metadata"), "context_excluded", "contextExcluded") {
		return true
	}
	if _, ok := payload["agent_delegation"]; ok {
		return true
	}
	if isAgentDelegationInstructionText(messageText(raw)) {
		return true
	}
	return false
}

func isAgentDelegationInstructionText(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "You are receiving an asynchronous delegated task from another agent")
}

func boolMapValue(data map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := data[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func mapValue(data map[string]any, key string) map[string]any {
	if value, ok := data[key].(map[string]any); ok {
		return value
	}
	return nil
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
