package sessionmonitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/providers"
)

type compactStore struct {
	messages    []db.Message
	summary     string
	metadata    any
	events      []string
	memories    []string
	preferences []string
}

func (s *compactStore) ClaimIdleSessions(context.Context, string, time.Duration, time.Duration, int) ([]db.MonitoredSession, error) {
	return nil, nil
}
func (s *compactStore) CompleteSessionMonitor(context.Context, string, string, time.Time, any) error {
	return nil
}
func (s *compactStore) ReleaseSessionMonitor(context.Context, string, string) error { return nil }
func (s *compactStore) RecentMessages(context.Context, string, string, int) ([]db.Message, error) {
	return s.messages, nil
}
func (s *compactStore) RecentUserMessages(context.Context, string, string, int) ([]db.Message, error) {
	return s.messages, nil
}
func (s *compactStore) MonitoredSession(context.Context, string, string) (*db.MonitoredSession, error) {
	return &db.MonitoredSession{CustomerID: "c1", UserID: "u1", AgentInstanceID: "agent1", SessionID: "s1"}, nil
}
func (s *compactStore) UpsertSessionSummary(_ context.Context, _, _, _, summary string, metadata any) error {
	s.summary = summary
	s.metadata = metadata
	return nil
}
func (s *compactStore) ListMemories(context.Context, string, string, int) ([]db.Memory, error) {
	return nil, nil
}
func (s *compactStore) AddMemory(_ context.Context, _, _, _, _, content string, _ any) (string, error) {
	s.memories = append(s.memories, content)
	return "", nil
}
func (s *compactStore) ListPreferences(context.Context, string, string, int) ([]db.Preference, error) {
	return nil, nil
}
func (s *compactStore) AddPreference(_ context.Context, _, _, _, _, content string, _ any, _ any) (string, error) {
	s.preferences = append(s.preferences, content)
	return "", nil
}
func (s *compactStore) AddObservabilityEvent(_ context.Context, _, _, eventType string, _ any) error {
	s.events = append(s.events, eventType)
	return nil
}
func (s *compactStore) PolicyRulesForScope(context.Context, string, string, string) ([]db.PolicyRule, error) {
	return nil, nil
}
func (s *compactStore) RecordPolicyEvaluation(context.Context, db.PolicyEvaluation) error {
	return nil
}

type compactionProfileProvider struct{}

func (compactionProfileProvider) GetDefaultModel() string { return "mock/profile" }

func (compactionProfileProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, options map[string]any) (*providers.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	purpose, _ := options["purpose"].(string)
	if purpose == "idle_memory_preference_extraction" {
		return &providers.LLMResponse{
			Content: `{"memories":[{"type":"fact","content":"User has a child named Luqman"}],"preferences":[{"category":"style","content":"User prefers concise answers","condition":{}}]}`,
		}, nil
	}
	return &providers.LLMResponse{Content: "Profile-aware summary"}, nil
}

type emptyExtractionProvider struct{}

func (emptyExtractionProvider) GetDefaultModel() string { return "mock/empty" }

func (emptyExtractionProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, options map[string]any) (*providers.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	purpose, _ := options["purpose"].(string)
	if purpose == "idle_memory_preference_extraction" {
		return &providers.LLMResponse{Content: `{"memories":[],"preferences":[]}`}, nil
	}
	return &providers.LLMResponse{Content: "summary"}, nil
}

func TestActivePatternCountsUserMessageHoursAndWeekdays(t *testing.T) {
	messages := []db.Message{
		{CreatedAt: time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)},
	}
	got := activePattern(nil, messages, time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC))
	hours := got["hours"].(map[string]int)
	weekdays := got["weekdays"].(map[string]int)
	if hours["09"] != 2 || hours["21"] != 1 {
		t.Fatalf("hours=%#v", hours)
	}
	if weekdays["Monday"] != 1 || weekdays["Tuesday"] != 2 {
		t.Fatalf("weekdays=%#v", weekdays)
	}
	if got["last_active"] != "2026-04-28T21:00:00Z" {
		t.Fatalf("last_active=%#v", got["last_active"])
	}
}

func TestMonitoredWindowSkipsAlreadyCountedAndConcurrentMessages(t *testing.T) {
	after := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	through := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	messages := []db.Message{
		{CreatedAt: after.Add(-time.Minute)},
		{CreatedAt: after},
		{CreatedAt: after.Add(time.Minute)},
		{CreatedAt: through},
		{CreatedAt: through.Add(time.Minute)},
	}
	got := monitoredWindow(messages, &after, through)
	if len(got) != 2 || !got[0].CreatedAt.Equal(after.Add(time.Minute)) || !got[1].CreatedAt.Equal(through) {
		t.Fatalf("window=%#v", got)
	}
	pattern := activePattern(nil, got, through)
	hours := pattern["hours"].(map[string]int)
	if hours["10"] != 1 || hours["11"] != 1 {
		t.Fatalf("hours=%#v", hours)
	}
}

func TestTranscriptExtractsTextParts(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"parts": []map[string]any{{"type": "text", "text": "hello"}}})
	got := transcript([]db.Message{{Role: "user", Content: raw}})
	if got != "user: hello" {
		t.Fatalf("transcript=%q", got)
	}
}

func TestMessageTextPrefersTopLevelText(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"text":  "top",
		"parts": []map[string]any{{"type": "text", "text": "part"}},
	})
	if got := messageText(raw); got != "top" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeAndExtractJSONObject(t *testing.T) {
	if got := normalize("  Hello   WORLD "); got != "hello world" {
		t.Fatalf("normalize=%q", got)
	}
	cases := map[string]string{
		`{"ok":true}`:            `{"ok":true}`,
		"prefix {\"ok\":true} x": `{"ok":true}`,
		"no json":                "no json",
	}
	for input, want := range cases {
		if got := extractJSONObject(input); got != want {
			t.Fatalf("extractJSONObject(%q)=%q want %q", input, got, want)
		}
	}
}

func TestCompactSessionForcesSummaryAndReturnsIt(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"text": "please remember I prefer concise answers"})
	store := &compactStore{messages: []db.Message{{ID: "m1", Role: "user", Content: raw, CreatedAt: time.Now()}}}
	registry := providers.NewRegistry("mock")
	registry.Register("mock", providers.MockProvider{})
	service := NewService(store, registry, providers.ModelConfig{Primary: "mock/duraclaw"}, "test")
	result, err := service.CompactSession(context.Background(), CompactRequest{CustomerID: "c1", SessionID: "s1", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || result.Summary == "" || store.summary != result.Summary {
		t.Fatalf("result=%#v stored=%q", result, store.summary)
	}
	if result.MessageCount != 1 || result.Provider != "mock" || result.Model == "" {
		t.Fatalf("result=%#v", result)
	}
	if len(store.events) != 1 || store.events[0] != "session_context_compacted" {
		t.Fatalf("events=%#v", store.events)
	}
}

func TestCompactSessionCanExtractProfile(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"text": "My child is Luqman and I prefer concise answers"})
	store := &compactStore{messages: []db.Message{{ID: "m1", Role: "user", Content: raw, CreatedAt: time.Now()}}}
	registry := providers.NewRegistry("profile")
	registry.Register("profile", compactionProfileProvider{})
	service := NewService(store, registry, providers.ModelConfig{Primary: "profile/mock"}, "test")

	result, err := service.CompactSession(context.Background(), CompactRequest{CustomerID: "c1", SessionID: "s1", Force: true, ExtractProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || result.Summary != "Profile-aware summary" {
		t.Fatalf("result=%#v", result)
	}
	if len(store.memories) != 1 || store.memories[0] != "User has a child named Luqman" {
		t.Fatalf("memories=%#v", store.memories)
	}
	if len(store.preferences) != 1 || store.preferences[0] != "User prefers concise answers" {
		t.Fatalf("preferences=%#v", store.preferences)
	}
	extraction, _ := result.Metadata["profile_extraction"].(map[string]any)
	if extraction["memories"] != 1 || extraction["preferences"] != 1 {
		t.Fatalf("metadata=%#v", result.Metadata)
	}
}

func TestProfileExtractionFallbacksCaptureNicknameAndChild(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"text": "Mulai sekarang panggil aku Kak Zen ya. Besok ingatkan bawa tas hitamnya Luqman anakku."})
	store := &compactStore{messages: []db.Message{{ID: "m1", Role: "user", Content: raw, CreatedAt: time.Now()}}}
	registry := providers.NewRegistry("empty")
	registry.Register("empty", emptyExtractionProvider{})
	service := NewService(store, registry, providers.ModelConfig{Primary: "empty/mock"}, "test")

	result, err := service.CompactSession(context.Background(), CompactRequest{CustomerID: "c1", SessionID: "s1", Force: true, ExtractProfile: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("result=%#v", result)
	}
	if len(store.memories) != 1 || store.memories[0] != "Luqman is the user's child" {
		t.Fatalf("memories=%#v", store.memories)
	}
	if len(store.preferences) != 1 || store.preferences[0] != "User prefers to be called Kak Zen" {
		t.Fatalf("preferences=%#v", store.preferences)
	}
}

func TestProfileExtractionSemanticKeysAndSkips(t *testing.T) {
	if !skipMemoryExtraction("Nama panggilan pengguna adalah Kak Zen") {
		t.Fatal("expected nickname fact to be skipped as memory")
	}
	if !skipProfileExtraction("User memiliki seorang ibu") {
		t.Fatal("expected trivial inferred parent fact to be skipped")
	}
	if !seenProfileSemantic(map[string]bool{"profile:child:luqman": true}, "Pengguna memiliki anak bernama Luqman", "fact") {
		t.Fatal("expected child semantic duplicate")
	}
	if !seenProfileSemantic(map[string]bool{"profile:addressing:kak zen": true}, "Panggil saya Kak Zen", "panggilan") {
		t.Fatal("expected addressing semantic duplicate")
	}
}
