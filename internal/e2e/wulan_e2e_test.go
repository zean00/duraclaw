package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"duraclaw/internal/artifacts"
	"duraclaw/internal/db"
	"duraclaw/internal/embeddings"
	"duraclaw/internal/knowledge"
	"duraclaw/internal/outbound"
	"duraclaw/internal/providers"
	"duraclaw/internal/runtime"
	"duraclaw/internal/scheduler"
	"duraclaw/internal/sessionmonitor"
	"duraclaw/internal/wulan"
)

func TestWulanCriticalPathE2E(t *testing.T) {
	store, pool, cleanup := e2eStore(t)
	defer cleanup()
	ctx := context.Background()
	seed, err := wulan.Seed(ctx, store, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	nexus := &nexusSink{}
	registry := providers.NewRegistry("openrouter")
	fake := &scriptedProvider{}
	registry.Register("openrouter", fake)
	worker := runtime.NewWorkerWithProviders(store, registry, providers.ModelConfig{Primary: "openrouter/openai/gpt-4.1-mini"}, "wulan-e2e-worker")
	worker.WithOutbound(outbound.NewService(store))
	worker.WithProcessors(artifacts.NewRegistry(artifacts.MockProcessor{}))
	drainOutboxIfAny(t, ctx, store, nexus)

	t.Run("durable failure then resume", func(t *testing.T) {
		fake.Reset()
		fake.FailMainOnce = true
		run := nexusSend(t, store, "durable-failure", "Wulan, bantu aku susun prioritas hari ini.")
		if ok, err := worker.RunOnce(ctx); !ok || err == nil {
			t.Fatalf("first run ok=%v err=%v, want injected failure", ok, err)
		}
		assertRunState(t, store, run.ID, "failed")
		if got := countModelCalls(t, pool, run.ID, "failed"); got == 0 {
			t.Fatalf("expected failed model call")
		}
		if got := countOutboundIntents(t, pool, run.ID); got != 0 {
			t.Fatalf("outbound created before resume: %d", got)
		}
		fake.FailMainOnce = false
		if err := store.SetRunState(ctx, run.ID, "queued", nil); err != nil {
			t.Fatal(err)
		}
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("resume run ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		if got := countOutboundIntents(t, pool, run.ID); got != 1 {
			t.Fatalf("outbound after resume=%d", got)
		}
		drainOutbox(t, ctx, store, nexus)
		if !nexus.Contains("prioritas") {
			t.Fatalf("nexus outbound missing resumed response: %#v", nexus.Payloads())
		}
	})

	t.Run("cron reminder from conversation fixture", func(t *testing.T) {
		fake.Reset()
		reminder, err := store.CreateReminderSubscription(ctx, db.ReminderSubscriptionSpec{
			CustomerID: wulan.CustomerID, UserID: wulan.UserID, SessionID: "wulan-e2e-reminder", AgentInstanceID: wulan.AgentInstanceID,
			Title: "Telepon Mama", Schedule: "@once", Timezone: "Asia/Jakarta", NextRunAt: time.Now().UTC().Add(-time.Minute),
			Payload:  map[string]any{"text": "Ingatkan aku telepon Mama sekarang.", "source": "nexus-cli-conversation"},
			Metadata: map[string]any{"origin": "conversation"},
		})
		if err != nil {
			t.Fatal(err)
		}
		created, err := scheduler.NewService(store, "wulan-e2e-scheduler").RunOnce(ctx, time.Now().UTC())
		if err != nil || created == 0 {
			t.Fatalf("scheduler created=%d err=%v", created, err)
		}
		subs, err := store.ListReminderSubscriptions(ctx, wulan.CustomerID, wulan.UserID, 20)
		if err != nil {
			t.Fatal(err)
		}
		if !subscriptionDisabled(subs, reminder.ID) {
			t.Fatalf("one-shot reminder was not completed: %#v", subs)
		}
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("reminder worker ok=%v err=%v", ok, err)
		}
		drainOutbox(t, ctx, store, nexus)
		if !nexus.Contains("Telepon Mama") && !nexus.Contains("pengingat") {
			t.Fatalf("nexus reminder missing: %#v", nexus.Payloads())
		}
	})

	t.Run("push alert promotion", func(t *testing.T) {
		before := nexus.Count()
		_, _, err := store.CreateBroadcast(ctx, wulan.CustomerID, "Promo Wulan", map[string]any{
			"text": "Coba rutinitas 3 prioritas hari ini bersama Wulan.",
			"kind": "promotion",
		}, []db.BroadcastTargetSpec{{UserID: wulan.UserID, SessionID: "wulan-e2e-broadcast"}})
		if err != nil {
			t.Fatal(err)
		}
		drainOutbox(t, ctx, store, nexus)
		if nexus.Count() <= before || !nexus.Contains("promotion") {
			t.Fatalf("promotion outbound missing: %#v", nexus.Payloads())
		}
	})

	t.Run("policy guidelines block secret", func(t *testing.T) {
		fake.Reset()
		run := nexusSend(t, store, "policy-block", "Simpan api_key: sk-or-v1-test-secret ke memoriku.")
		if ok, err := worker.RunOnce(ctx); !ok || err == nil {
			t.Fatalf("policy worker ok=%v err=%v, want policy failure", ok, err)
		}
		assertRunState(t, store, run.ID, "failed")
		if errText := runError(t, pool, run.ID); !strings.Contains(errText, "blocked") {
			t.Fatalf("expected blocked policy_config error, got %q", errText)
		}
	})

	t.Run("agent personality and style", func(t *testing.T) {
		fake.Reset()
		run := nexusSend(t, store, "personality", "Wulan, bantu aku susun prioritas hari ini. Aku ada meeting jam 10 dan mau sempat baca Quran.")
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("personality worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		answer := latestAssistantText(t, pool, run.ID)
		for _, want := range []string{"prioritas", "Quran", "meeting"} {
			if !strings.Contains(strings.ToLower(answer), strings.ToLower(want)) {
				t.Fatalf("answer %q missing %q", answer, want)
			}
		}
	})

	t.Run("out of scope request", func(t *testing.T) {
		fake.Reset()
		run := nexusSend(t, store, "out-of-scope", "Beri aku strategi trading kripto dengan profit pasti.")
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("scope worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		answer := latestAssistantText(t, pool, run.ID)
		if !strings.Contains(answer, "di luar cakupan") {
			t.Fatalf("out-of-scope response=%q", answer)
		}
		if got := countRunEvents(t, pool, run.ID, "scope.judged"); got == 0 {
			t.Fatalf("expected scope.judged event")
		}
	})

	t.Run("direct scope does not run context pass and recommendation uses direct message", func(t *testing.T) {
		fake.Reset()
		items, err := store.ListRecommendationItems(ctx, wulan.CustomerID, "active", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			t.Fatal("expected recommendation catalog items")
		}
		fake.RecommendationItemID = items[0].ID
		run := nexusSend(t, store, "direct-scope-recommendation", "Wulan, bantu susun prioritas hari ini.")
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("direct scope worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		scopeEvents := scopeJudgedEvents(t, pool, run.ID)
		if !hasScopePass(scopeEvents, "initial") || hasScopePass(scopeEvents, "context") {
			t.Fatalf("direct intent should only have initial scope pass: %#v", scopeEvents)
		}
		decisions, err := store.ListRecommendationDecisions(ctx, wulan.CustomerID, run.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(decisions) == 0 || decisions[0].ContextMode != "direct_message" || decisions[0].DeliveryStatus != "inline_merged" {
			t.Fatalf("direct recommendation decision=%#v", decisions)
		}
	})

	t.Run("recommendation timeout queues durable job and outbox push", func(t *testing.T) {
		fake.Reset()
		items, err := store.ListRecommendationItems(ctx, wulan.CustomerID, "active", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			t.Fatal("expected recommendation catalog items")
		}
		if err := activateWulanRecommendationTimeout(t, ctx, store, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		fake.RecommendationItemID = items[0].ID
		fake.DelayRecommendation = 50 * time.Millisecond
		run := nexusSend(t, store, "recommendation-timeout", "Wulan, bantu susun prioritas hari ini.")
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("timeout worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		decisions, err := store.ListRecommendationDecisions(ctx, wulan.CustomerID, run.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(decisions) == 0 || decisions[0].DeliveryStatus != "timeout" {
			t.Fatalf("expected timeout decision, got %#v", decisions)
		}
		jobs, err := store.ListRecommendationJobs(ctx, wulan.CustomerID, "queued", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 0 {
			t.Fatal("expected queued recommendation job")
		}
		fake.DelayRecommendation = 0
		before := nexus.Count()
		if processed, err := worker.RunRecommendationJobs(ctx, 5); err != nil || processed == 0 {
			t.Fatalf("recommendation jobs processed=%d err=%v", processed, err)
		}
		drainOutbox(t, ctx, store, nexus)
		if nexus.Count() <= before || !nexus.Contains("recommendation") {
			t.Fatalf("recommendation outbox missing: %#v", nexus.Payloads())
		}
		outboundDecisions, err := store.ListRecommendationDecisions(ctx, wulan.CustomerID, run.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !hasRecommendationStatus(outboundDecisions, "outbound_queued") {
			t.Fatalf("expected outbound_queued decision, got %#v", outboundDecisions)
		}
	})

	t.Run("workflow execution", func(t *testing.T) {
		fake.Reset()
		run := nexusSendInput(t, store, "workflow", map[string]any{
			"text":                   "Buatkan prioritas hari ini.",
			"workflow_definition_id": seed.DailyPlannerID,
		})
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("workflow worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		if got := countWorkflowRuns(t, pool, run.ID); got == 0 {
			t.Fatalf("expected workflow run")
		}
		if got := countOutboundIntents(t, pool, run.ID); got == 0 {
			t.Fatalf("expected workflow outbound")
		}
	})

	t.Run("knowledge retrieval", func(t *testing.T) {
		fake.Reset()
		_, chunks, err := knowledge.NewIngester(store).WithEmbedder(embeddings.NewHashProvider(768)).IngestText(ctx, wulan.CustomerID, "Panduan Rutinitas Wulan", "wulan://knowledge/routine", "Rutinitas Wulan: mulai hari dengan tiga prioritas, jeda Quran 10 menit, dan review malam singkat.", map[string]any{"source": "e2e"})
		if err != nil {
			t.Fatal(err)
		}
		if chunks == 0 {
			t.Fatalf("expected knowledge chunks")
		}
		run := nexusSend(t, store, "knowledge", "Apa rutinitas Wulan untuk hari produktif?")
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("knowledge worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		answer := latestAssistantText(t, pool, run.ID)
		if !strings.Contains(strings.ToLower(answer), "tiga prioritas") || !strings.Contains(strings.ToLower(answer), "quran") {
			t.Fatalf("knowledge answer=%q", answer)
		}
		if got := countPolicyMode(t, pool, run.ID, "pre_knowledge_retrieval"); got == 0 {
			t.Fatalf("expected knowledge policy evaluation")
		}
	})

	t.Run("session extraction memories preferences compaction", func(t *testing.T) {
		fake.Reset()
		sessionID := "wulan-e2e-session-monitor"
		run := nexusSendInput(t, store, "session-monitor", map[string]any{"text": "Aku tinggal di Bandung. Aku lebih suka pengingat pagi jam 07:00. " + strings.Repeat("catatan panjang untuk kompaksi. ", 120)})
		if err := store.SetRunState(ctx, run.ID, "completed", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InsertMessage(ctx, wulan.CustomerID, sessionID, run.ID, "user", map[string]any{"text": "Aku tinggal di Bandung. Aku lebih suka pengingat pagi jam 07:00. " + strings.Repeat("catatan panjang untuk kompaksi. ", 120)}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.InsertMessage(ctx, wulan.CustomerID, sessionID, run.ID, "assistant", map[string]any{"text": "Siap, Wulan catat preferensi pengingat pagi."}); err != nil {
			t.Fatal(err)
		}
		monitor := sessionmonitor.NewService(store, registry, providers.ModelConfig{Primary: "openrouter/openai/gpt-4.1-mini"}, "wulan-e2e-session-monitor").WithIdleFor(time.Nanosecond).WithCompactionThreshold(100)
		processed, err := monitor.RunOnce(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if processed == 0 {
			t.Fatalf("expected idle session to be processed")
		}
		memories, err := store.ListMemories(ctx, wulan.CustomerID, wulan.UserID, 20)
		if err != nil {
			t.Fatal(err)
		}
		if !memoryContains(memories, "Bandung") {
			t.Fatalf("memories=%#v", memories)
		}
		preferences, err := store.ListPreferences(ctx, wulan.CustomerID, wulan.UserID, 20)
		if err != nil {
			t.Fatal(err)
		}
		if !preferenceContains(preferences, "07:00") {
			t.Fatalf("preferences=%#v", preferences)
		}
		summary, err := store.SessionSummary(ctx, wulan.CustomerID, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if summary == nil || !strings.Contains(strings.ToLower(summary.Summary), "bandung") {
			t.Fatalf("summary=%#v", summary)
		}
		if got := countObservabilityEvents(t, pool, wulan.CustomerID, "session_memory_extracted"); got == 0 {
			t.Fatalf("expected session extraction observability event")
		}
	})

	t.Run("multimodal artifacts voice image pdf", func(t *testing.T) {
		fake.Reset()
		run := nexusSendInput(t, store, "multimodal", map[string]any{
			"text": "Ringkas voice note, gambar, dan PDF ini untuk Wulan.",
			"parts": []map[string]any{
				{"type": "text", "text": "Tolong proses lampiran."},
				{"type": "artifact_ref", "data": map[string]any{"artifact_id": "voice-note-1"}},
				{"type": "artifact_ref", "data": map[string]any{"artifact_id": "image-1"}},
				{"type": "artifact_ref", "data": map[string]any{"artifact_id": "pdf-1"}},
			},
		})
		for _, artifact := range []db.Artifact{
			{ID: "voice-note-1", Modality: "audio", MediaType: "audio/ogg", Filename: "note.ogg", StorageRef: "memory://voice-note-1", State: "available"},
			{ID: "image-1", Modality: "image", MediaType: "image/png", Filename: "photo.png", StorageRef: "memory://image-1", State: "available"},
			{ID: "pdf-1", Modality: "document", MediaType: "application/pdf", Filename: "doc.pdf", StorageRef: "memory://pdf-1", State: "available"},
		} {
			if err := store.AttachArtifact(ctx, run.ID, artifact); err != nil {
				t.Fatal(err)
			}
		}
		if ok, err := worker.RunOnce(ctx); err != nil || !ok {
			t.Fatalf("multimodal worker ok=%v err=%v", ok, err)
		}
		assertRunState(t, store, run.ID, "completed")
		if got := countProcessorCalls(t, pool, run.ID); got != 3 {
			t.Fatalf("processor calls=%d", got)
		}
		for _, artifactID := range []string{"voice-note-1", "image-1", "pdf-1"} {
			reps, err := store.ArtifactRepresentations(ctx, wulan.CustomerID, artifactID)
			if err != nil {
				t.Fatal(err)
			}
			if len(reps) == 0 {
				t.Fatalf("missing representation for %s", artifactID)
			}
		}
		answer := latestAssistantText(t, pool, run.ID)
		if !strings.Contains(strings.ToLower(answer), "audio") || !strings.Contains(strings.ToLower(answer), "image") || !strings.Contains(strings.ToLower(answer), "document") {
			t.Fatalf("multimodal answer=%q", answer)
		}
	})
}

func TestWulanOpenRouterLiveSmoke(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	provider := providers.OpenRouterProvider{
		APIKey: key, DefaultModel: "openai/gpt-4.1-mini", Referer: "https://duraclaw.local/e2e", Title: "Duraclaw Wulan E2E",
	}
	resp, err := provider.Chat(context.Background(), []providers.Message{
		{Role: "system", Content: "Jawab singkat dalam Bahasa Indonesia."},
		{Role: "user", Content: "Balas hanya: Wulan siap."},
	}, nil, "openai/gpt-4.1-mini", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(resp.Content), "wulan") {
		t.Fatalf("unexpected live response: %q", resp.Content)
	}
}

func TestWulanOpenRouterLiveScopeRecommendationE2E(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	store, pool, cleanup := e2eStore(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := wulan.Seed(ctx, store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	nexus := &nexusSink{}
	registry := providers.NewRegistry("openrouter")
	registry.Register("openrouter", providers.OpenRouterProvider{
		APIKey: key, DefaultModel: "openai/gpt-4.1-mini", Referer: "https://duraclaw.local/e2e", Title: "Duraclaw Wulan E2E",
	})
	worker := runtime.NewWorkerWithProviders(store, registry, providers.ModelConfig{Primary: "openrouter/openai/gpt-4.1-mini"}, "wulan-live-e2e-worker")
	worker.WithOutbound(outbound.NewService(store))
	drainOutboxIfAny(t, ctx, store, nexus)

	priorRun, err := store.CreateRun(ctx, db.ACPContext{
		CustomerID: wulan.CustomerID, UserID: wulan.UserID, SessionID: "wulan-e2e-live-scope-rec", AgentInstanceID: wulan.AgentInstanceID,
		RequestID: "nexus-cli-live-scope-rec-prior", IdempotencyKey: "nexus-cli-live-scope-rec-prior",
	}, map[string]any{"text": "Besok aku ada meeting penting dan ingin tetap sempat baca Quran."})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunState(ctx, priorRun.ID, "completed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertMessage(ctx, wulan.CustomerID, "wulan-e2e-live-scope-rec", priorRun.ID, "assistant", map[string]any{"text": "Siap, besok kita susun rencana ringan untuk meeting dan jeda Quran."}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSessionSummary(ctx, wulan.CustomerID, "wulan-e2e-live-scope-rec", "", "Pengguna sedang menyiapkan hari dengan meeting penting dan ingin menjaga rutinitas Quran ringan.", map[string]any{"source": "live_e2e"}); err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, db.ACPContext{
		CustomerID: wulan.CustomerID, UserID: wulan.UserID, SessionID: "wulan-e2e-live-scope-rec", AgentInstanceID: wulan.AgentInstanceID,
		RequestID: "nexus-cli-live-scope-rec", IdempotencyKey: "nexus-cli-live-scope-rec-" + fmt.Sprint(time.Now().UnixNano()),
	}, map[string]any{"text": "Bantu susun itu jadi rencana ringan."})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := worker.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("live worker ok=%v err=%v", ok, err)
	}
	assertRunState(t, store, run.ID, "completed")
	answer := latestAssistantText(t, pool, run.ID)
	lower := strings.ToLower(answer)
	if !strings.Contains(lower, "meeting") || !strings.Contains(lower, "quran") {
		t.Fatalf("live answer did not use implicit context: %q", answer)
	}
	scopeEvents := scopeJudgedEvents(t, pool, run.ID)
	if !hasScopePass(scopeEvents, "initial") || !hasScopePass(scopeEvents, "context") {
		t.Fatalf("expected two-pass scope events, got %#v", scopeEvents)
	}
	decisions, err := store.ListRecommendationDecisions(ctx, wulan.CustomerID, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) == 0 {
		t.Fatalf("expected recommendation decision")
	}
	if decisions[0].ContextMode != "implicit_context" {
		t.Fatalf("recommendation context mode=%q decisions=%#v", decisions[0].ContextMode, decisions)
	}
	if decisions[0].DeliveryStatus == "failed" || decisions[0].DeliveryStatus == "timeout" {
		t.Fatalf("recommendation failed: %#v", decisions[0])
	}
	t.Logf("live Wulan answer: %s", answer)
	t.Logf("scope events: %#v", scopeEvents)
	t.Logf("recommendation decision: status=%s item=%v reason=%s", decisions[0].DeliveryStatus, decisions[0].SelectedItemID, decisions[0].Reason)
}

type scriptedProvider struct {
	mu                   sync.Mutex
	FailMainOnce         bool
	RecommendationItemID string
	DelayRecommendation  time.Duration
	failed               bool
}

func (p *scriptedProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = false
	p.RecommendationItemID = ""
	p.DelayRecommendation = 0
}

func (p *scriptedProvider) GetDefaultModel() string { return "e2e/wulan" }

func (p *scriptedProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, options map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	delayRecommendation := p.DelayRecommendation
	recommendationItemID := p.RecommendationItemID
	p.mu.Unlock()
	text := joinedMessages(messages)
	if options != nil && options["purpose"] == "recommendation" {
		if delayRecommendation > 0 {
			select {
			case <-time.After(delayRecommendation):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if recommendationItemID == "" {
			return &providers.LLMResponse{Content: `{"should_recommend":false,"reason":"no scripted recommendation"}`, FinishReason: "stop"}, nil
		}
		return &providers.LLMResponse{Content: fmt.Sprintf(`{"should_recommend":true,"item_id":%q,"recommendation_text":"Kalau membantu, coba Paket Rutinitas 3 Prioritas untuk menjaga fokus harian.","reason":"matches planning request","confidence":0.9}`, recommendationItemID), FinishReason: "stop"}, nil
	}
	if options != nil && options["purpose"] == "recommendation_merge" {
		return &providers.LLMResponse{Content: "Prioritas hari ini: pilih 3 hal utama, mulai dari yang paling ringan, lalu sisipkan jeda singkat untuk ibadah. Kalau membantu, coba Paket Rutinitas 3 Prioritas sebagai panduan ringan.", FinishReason: "stop"}, nil
	}
	if options != nil && options["purpose"] == "session_compaction" {
		return &providers.LLMResponse{Content: "Pengguna tinggal di Bandung dan menyukai pengingat pagi jam 07:00.", FinishReason: "stop"}, nil
	}
	if options != nil && options["purpose"] == "idle_memory_preference_extraction" {
		return &providers.LLMResponse{Content: `{"memories":[{"type":"fact","content":"Pengguna tinggal di Bandung."}],"preferences":[{"category":"reminder","content":"Pengguna lebih suka pengingat pagi jam 07:00.","condition":{"time_of_day":"morning"}}]}`, FinishReason: "stop"}, nil
	}
	if options != nil && options["purpose"] == "scope_judge" {
		if strings.Contains(strings.ToLower(text), "trading kripto") {
			return &providers.LLMResponse{Content: `{"intent":"direct","in_scope":false,"confidence":0.95,"reason":"financial advice","recommended_response":"Maaf, ini di luar cakupan Wulan. Wulan bisa bantu pengingat, catatan, rutinitas, dan produktivitas harian."}`, FinishReason: "stop"}, nil
		}
		if strings.Contains(strings.ToLower(text), "bantu susun itu") {
			return &providers.LLMResponse{Content: `{"intent":"implicit","in_scope":true,"confidence":0.95,"reason":"follow-up personal assistant request","recommended_response":""}`, FinishReason: "stop"}, nil
		}
		return &providers.LLMResponse{Content: `{"intent":"direct","in_scope":true,"confidence":0.95,"reason":"personal assistant request","recommended_response":""}`, FinishReason: "stop"}, nil
	}
	p.mu.Lock()
	if p.FailMainOnce && !p.failed {
		p.failed = true
		p.mu.Unlock()
		return nil, errors.New("injected provider failure")
	}
	p.mu.Unlock()
	switch {
	case strings.Contains(strings.ToLower(text), "telepon mama"):
		return &providers.LLMResponse{Content: "Pengingat dari Wulan: Telepon Mama sekarang.", FinishReason: "stop"}, nil
	case strings.Contains(strings.ToLower(text), "rutinitas wulan"):
		return &providers.LLMResponse{Content: "Rutinitas Wulan: mulai dengan tiga prioritas, lanjutkan jeda Quran 10 menit, lalu review malam singkat.", FinishReason: "stop"}, nil
	case strings.Contains(strings.ToLower(text), "artifact context"):
		return &providers.LLMResponse{Content: "Ringkasan lampiran: audio voice note, image/gambar, dan document/PDF sudah diproses.", FinishReason: "stop"}, nil
	case strings.Contains(strings.ToLower(text), "meeting jam 10"):
		return &providers.LLMResponse{Content: "Prioritas hari ini: 1. Siapkan meeting jam 10. 2. Selesaikan tugas utama. 3. Sisipkan 10 menit baca Quran.", FinishReason: "stop"}, nil
	case strings.Contains(strings.ToLower(text), "prioritas hari ini"):
		return &providers.LLMResponse{Content: "Prioritas hari ini: pilih 3 hal utama, mulai dari yang paling ringan, lalu sisipkan jeda singkat untuk ibadah.", FinishReason: "stop"}, nil
	case strings.Contains(strings.ToLower(text), "rencana harian"):
		return &providers.LLMResponse{Content: `{"priorities":["meeting jam 10","tugas utama"],"schedule_blocks":["09:30 persiapan","10:00 meeting"],"ibadah_reminders":["baca Quran 10 menit"],"next_action":"mulai dari persiapan meeting"}`, FinishReason: "stop"}, nil
	default:
		return &providers.LLMResponse{Content: "Wulan siap. Aku bantu susun prioritas dengan langkah singkat dan praktis.", FinishReason: "stop"}, nil
	}
}

func e2eStore(t *testing.T) (*db.Store, db.Pool, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	truncateAll(t, ctx, pool)
	return db.NewStore(pool), pool, pool.Close
}

func nexusSend(t *testing.T, store *db.Store, key, text string) *db.Run {
	t.Helper()
	return nexusSendInput(t, store, key, map[string]any{"text": text})
}

func nexusSendInput(t *testing.T, store *db.Store, key string, input map[string]any) *db.Run {
	t.Helper()
	run, err := store.CreateRun(context.Background(), db.ACPContext{
		CustomerID: wulan.CustomerID, UserID: wulan.UserID, SessionID: "wulan-e2e-" + key, AgentInstanceID: wulan.AgentInstanceID,
		RequestID: "nexus-cli-" + key, IdempotencyKey: "nexus-cli-" + key + "-" + fmt.Sprint(time.Now().UnixNano()),
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

type nexusSink struct {
	mu      sync.Mutex
	payload []string
}

func (s *nexusSink) Handle(_ context.Context, item db.OutboxItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload = append(s.payload, string(item.Payload))
	return nil
}

func (s *nexusSink) Contains(needle string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	needle = strings.ToLower(needle)
	for _, payload := range s.payload {
		if strings.Contains(strings.ToLower(payload), needle) {
			return true
		}
	}
	return false
}

func (s *nexusSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.payload)
}

func (s *nexusSink) Payloads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.payload...)
}

func drainOutbox(t *testing.T, ctx context.Context, store *db.Store, sink *nexusSink) {
	t.Helper()
	n, err := outbound.NewOutboxWorker(store, sink, "nexus-cli-e2e").RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("expected outbox items")
	}
}

func drainOutboxIfAny(t *testing.T, ctx context.Context, store *db.Store, sink *nexusSink) {
	t.Helper()
	if _, err := outbound.NewOutboxWorker(store, sink, "nexus-cli-e2e").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertRunState(t *testing.T, store *db.Store, runID, want string) {
	t.Helper()
	got, err := store.RunState(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("run %s state=%s want=%s", runID, got, want)
	}
}

func latestAssistantText(t *testing.T, pool db.Pool, runID string) string {
	t.Helper()
	var content []byte
	if err := pool.QueryRow(context.Background(), `SELECT content FROM messages WHERE run_id=$1 AND role='assistant' ORDER BY created_at DESC LIMIT 1`, runID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatal(err)
	}
	if text, _ := payload["text"].(string); text != "" {
		return text
	}
	if parts, _ := payload["parts"].([]any); len(parts) > 0 {
		var out []string
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			if text, _ := part["text"].(string); text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "\n")
		}
	}
	return string(content)
}

func runError(t *testing.T, pool db.Pool, runID string) string {
	t.Helper()
	var errText *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM runs WHERE id=$1`, runID).Scan(&errText); err != nil {
		t.Fatal(err)
	}
	if errText == nil {
		return ""
	}
	return *errText
}

func countModelCalls(t *testing.T, pool db.Pool, runID, state string) int {
	return countWhere(t, pool, `SELECT count(*) FROM model_calls WHERE run_id=$1 AND state=$2`, runID, state)
}

func countOutboundIntents(t *testing.T, pool db.Pool, runID string) int {
	return countWhere(t, pool, `SELECT count(*) FROM outbound_intents WHERE run_id=$1`, runID)
}

func countPolicyDecisions(t *testing.T, pool db.Pool, runID, decision string) int {
	return countWhere(t, pool, `SELECT count(*) FROM policy_evaluations WHERE run_id=$1 AND decision=$2`, runID, decision)
}

func countPolicyMode(t *testing.T, pool db.Pool, runID, mode string) int {
	return countWhere(t, pool, `SELECT count(*) FROM policy_evaluations WHERE run_id=$1 AND enforcement_mode=$2`, runID, mode)
}

func countRunEvents(t *testing.T, pool db.Pool, runID, typ string) int {
	return countWhere(t, pool, `SELECT count(*) FROM run_events WHERE run_id=$1 AND event_type=$2`, runID, typ)
}

func scopeJudgedEvents(t *testing.T, pool db.Pool, runID string) []map[string]any {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT payload FROM run_events WHERE run_id=$1 AND event_type='scope.judged' ORDER BY id`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func hasScopePass(events []map[string]any, pass string) bool {
	for _, event := range events {
		if got, _ := event["pass"].(string); got == pass {
			return true
		}
	}
	return false
}

func hasRecommendationStatus(decisions []db.RecommendationDecision, status string) bool {
	for _, decision := range decisions {
		if decision.DeliveryStatus == status {
			return true
		}
	}
	return false
}

func activateWulanRecommendationTimeout(t *testing.T, ctx context.Context, store *db.Store, timeout time.Duration) error {
	t.Helper()
	profile := wulan.ProfileConfig()
	recommendation, _ := profile["recommendation"].(map[string]any)
	recommendation["timeout_ms"] = int(timeout / time.Millisecond)
	_, err := store.CreateAgentInstanceVersion(ctx, db.AgentInstanceVersionSpec{
		CustomerID:          wulan.CustomerID,
		AgentInstanceID:     wulan.AgentInstanceID,
		Name:                "Wulan Assistant recommendation timeout test",
		ModelConfig:         map[string]any{"primary": "openrouter/openai/gpt-4.1-mini", "fallbacks": []string{}},
		SystemInstructions:  wulan.SystemInstructions,
		ToolConfig:          map[string]any{"allowed_tools": []string{"remember", "list_memories", "save_preference", "list_preferences"}, "max_iterations": 8, "max_tool_calls_per_run": 8},
		WorkflowConfig:      map[string]any{"allowed_workflows": []string{}},
		ProfileConfig:       profile,
		ActivateImmediately: true,
	})
	return err
}

func countWorkflowRuns(t *testing.T, pool db.Pool, runID string) int {
	return countWhere(t, pool, `SELECT count(*) FROM workflow_runs WHERE run_id=$1`, runID)
}

func countProcessorCalls(t *testing.T, pool db.Pool, runID string) int {
	return countWhere(t, pool, `SELECT count(*) FROM processor_calls WHERE run_id=$1 AND state='succeeded'`, runID)
}

func countObservabilityEvents(t *testing.T, pool db.Pool, customerID, eventType string) int {
	return countWhere(t, pool, `SELECT count(*) FROM observability_events WHERE customer_id=$1 AND event_type=$2`, customerID, eventType)
}

func countWhere(t *testing.T, pool db.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func subscriptionDisabled(subs []db.ReminderSubscription, id string) bool {
	for _, sub := range subs {
		if sub.ID == id {
			return !sub.Enabled
		}
	}
	return false
}

func memoryContains(memories []db.Memory, needle string) bool {
	needle = strings.ToLower(needle)
	for _, memory := range memories {
		if strings.Contains(strings.ToLower(memory.Content), needle) {
			return true
		}
	}
	return false
}

func preferenceContains(preferences []db.Preference, needle string) bool {
	needle = strings.ToLower(needle)
	for _, preference := range preferences {
		if strings.Contains(strings.ToLower(preference.Content), needle) {
			return true
		}
	}
	return false
}

func joinedMessages(messages []providers.Message) string {
	var parts []string
	for _, msg := range messages {
		parts = append(parts, msg.Text())
	}
	return strings.Join(parts, "\n")
}

func truncateAll(t *testing.T, ctx context.Context, pool db.Pool) {
	t.Helper()
	const sql = `
DO $$
DECLARE
	tables text;
BEGIN
	SELECT string_agg(format('%I.%I', schemaname, tablename), ', ')
	INTO tables
	FROM pg_tables
	WHERE schemaname = current_schema()
		AND tablename <> 'schema_migrations';

	IF tables IS NOT NULL THEN
		EXECUTE 'TRUNCATE TABLE ' || tables || ' RESTART IDENTITY CASCADE';
	END IF;
END $$`
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}
