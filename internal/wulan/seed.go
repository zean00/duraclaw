package wulan

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"duraclaw/internal/db"
)

const (
	CustomerID      = "wulan-e2e-customer"
	UserID          = "wulan-e2e-user"
	SessionID       = "wulan-e2e-session"
	AgentInstanceID = "wulan-assistant"
)

type SeedResult struct {
	AgentVersionID       string
	PolicyPackID         string
	DailyPlannerID       string
	QuickNoteID          string
	OneShotReminderID    string
	RoutineBroadcastID   string
	ReminderID           string
	BroadcastID          string
	BroadcastTargetCount int
}

type Store interface {
	CreateAgentInstanceVersion(ctx context.Context, spec db.AgentInstanceVersionSpec) (*db.AgentInstanceVersion, error)
	CreateRecommendationItem(ctx context.Context, spec db.RecommendationItemSpec) (*db.RecommendationItem, error)
	CreatePolicyPack(ctx context.Context, name string, version int, ownerScope string) (string, error)
	UpsertPolicyRule(ctx context.Context, rule db.PolicyRule) (string, error)
	AssignPolicyPack(ctx context.Context, policyPackID, customerID, agentInstanceID string, enabled bool) (string, error)
	CreateWorkflowDefinition(ctx context.Context, name string, version int, description, whenToUse string, inputSchema, outputSchema any) (string, error)
	UpsertWorkflowNode(ctx context.Context, workflowDefinitionID, nodeKey, nodeType string, config, retryPolicy, timeoutPolicy any) error
	UpsertWorkflowEdge(ctx context.Context, workflowDefinitionID, fromNodeKey, toNodeKey string, condition any) error
	AssignWorkflow(ctx context.Context, workflowDefinitionID, customerID, agentInstanceID string, enabled bool) error
	CreateReminderSubscription(ctx context.Context, spec db.ReminderSubscriptionSpec) (*db.ReminderSubscription, error)
	CreateBroadcast(ctx context.Context, customerID, title string, payload any, targets []db.BroadcastTargetSpec) (string, int, error)
}

func Seed(ctx context.Context, store Store, now time.Time) (*SeedResult, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result := &SeedResult{}
	packID, err := store.CreatePolicyPack(ctx, "wulan-core-safety", 1, "customer")
	if err != nil {
		return nil, err
	}
	result.PolicyPackID = packID
	for _, rule := range policyRules(packID) {
		if _, err := store.UpsertPolicyRule(ctx, rule); err != nil {
			return nil, err
		}
	}
	if _, err := store.AssignPolicyPack(ctx, packID, CustomerID, AgentInstanceID, true); err != nil {
		return nil, err
	}
	version, err := store.CreateAgentInstanceVersion(ctx, db.AgentInstanceVersionSpec{
		CustomerID:          CustomerID,
		AgentInstanceID:     AgentInstanceID,
		Name:                "Wulan Assistant v1",
		ModelConfig:         map[string]any{"primary": "openrouter/openai/gpt-4.1-mini", "fallbacks": []string{}},
		SystemInstructions:  SystemInstructions,
		ToolConfig:          map[string]any{"allowed_tools": []string{"remember", "list_memories", "save_preference", "list_preferences"}, "max_iterations": 8, "max_tool_calls_per_run": 8},
		WorkflowConfig:      map[string]any{"allowed_workflows": []string{}},
		PolicyConfig:        policyConfig(packID),
		ProfileConfig:       ProfileConfig(),
		Metadata:            map[string]any{"source": "wulan.ai-inspired-e2e", "seeded_at": now.UTC().Format(time.RFC3339)},
		ActivateImmediately: true,
	})
	if err != nil {
		return nil, err
	}
	result.AgentVersionID = version.ID
	workflows := []struct {
		name        string
		description string
		whenToUse   string
		build       func(context.Context, Store, string) error
		target      *string
	}{
		{"wulan_daily_planner_v1", "Create a practical daily plan from a user request.", "Use when the user asks for priorities, planning, routines, or balancing work and ibadah.", buildDailyPlanner, &result.DailyPlannerID},
		{"wulan_quick_note_v1", "Normalize and save a user note.", "Use when the user asks Wulan to remember or record an idea, list, summary, or preference.", buildQuickNote, &result.QuickNoteID},
		{"wulan_one_shot_reminder_v1", "Create a durable one-shot reminder with workflow timer.", "Use when a conversational reminder has a concrete time or duration.", buildOneShotReminder, &result.OneShotReminderID},
		{"wulan_routine_broadcast_v1", "Emit a safe routine or promotion outbound message.", "Use for approved routine nudges, alerts, or promotions.", buildRoutineBroadcast, &result.RoutineBroadcastID},
	}
	for _, wf := range workflows {
		id, err := store.CreateWorkflowDefinition(ctx, wf.name, 1, wf.description, wf.whenToUse, map[string]any{"type": "object"}, map[string]any{"type": "object"})
		if err != nil {
			return nil, err
		}
		if err := wf.build(ctx, store, id); err != nil {
			return nil, err
		}
		if err := store.AssignWorkflow(ctx, id, CustomerID, AgentInstanceID, true); err != nil {
			return nil, err
		}
		*wf.target = id
	}
	reminder, err := store.CreateReminderSubscription(ctx, db.ReminderSubscriptionSpec{
		CustomerID: CustomerID, UserID: UserID, SessionID: SessionID, AgentInstanceID: AgentInstanceID,
		Title: "Minum obat malam", Schedule: "@once", Timezone: "Asia/Jakarta", NextRunAt: now.UTC().Add(time.Minute),
		Payload:  map[string]any{"text": "Ingatkan aku minum obat malam ini jam 8.", "source": "wulan-e2e"},
		Metadata: map[string]any{"origin": "conversation", "workflow": "wulan_one_shot_reminder_v1"},
	})
	if err != nil {
		return nil, err
	}
	result.ReminderID = reminder.ID
	broadcastID, count, err := store.CreateBroadcast(ctx, CustomerID, "Wulan Daily Nudge", map[string]any{
		"text": "Pagi. Pilih 3 prioritas hari ini, lalu sisipkan satu momen tenang untuk ibadah.",
		"kind": "routine_nudge",
	}, []db.BroadcastTargetSpec{{UserID: UserID, SessionID: SessionID}})
	if err != nil {
		return nil, err
	}
	result.BroadcastID = broadcastID
	result.BroadcastTargetCount = count
	if err := SeedRecommendationCatalog(ctx, store); err != nil {
		return nil, err
	}
	return result, nil
}

const SystemInstructions = `Kamu adalah Wulan, asisten personal berbahasa Indonesia yang membantu pengguna mengatur hari, mencatat hal penting, membuat pengingat, menjaga rutinitas, dan menyeimbangkan aktivitas duniawi dengan ibadah.

Prinsip utama:
- Jadilah hangat, ringkas, praktis, dan dekat seperti teman yang bisa diandalkan.
- Fokus pada tindakan: catat, susun prioritas, buat checklist, jadwalkan pengingat, atau tanyakan satu klarifikasi.
- Jangan mengarang bahwa pengingat sudah dibuat jika sistem belum mengonfirmasi.
- Untuk permintaan pengingat, pastikan minimal ada isi pengingat dan waktu. Jika waktu ambigu, tanyakan klarifikasi.
- Untuk ibadah, bantu sebagai pengingat dan refleksi ringan. Jangan memberikan fatwa otoritatif; sarankan bertanya ke ustadz/ulama untuk hukum agama yang detail.
- Untuk Quran, boleh membantu ringkasan, hafalan ringan, refleksi umum, dan motivasi. Jangan mengklaim tafsir final.
- Untuk permintaan di luar cakupan, tolak singkat dan arahkan ke hal yang bisa Wulan bantu.
- Jangan meminta atau menyimpan data sensitif yang tidak perlu.`

func ProfileConfig() map[string]any {
	return map[string]any{
		"personality":           "hangat, praktis, tenang, suportif, dan tidak menggurui",
		"communication_style":   "Bahasa Indonesia natural; jawaban pendek dulu; tanyakan klarifikasi hanya jika diperlukan; gunakan format checklist untuk tugas harian",
		"language_capabilities": []string{"id", "en"},
		"domain_scope": map[string]any{
			"allowed_domains":       []string{"pengingat harian", "jadwal dan rutinitas", "catatan pribadi", "to-do list dan prioritas", "produktivitas harian", "pengingat shalat dan ibadah", "refleksi Quran ringan", "quotes dan motivasi harian", "ringkasan dan terjemahan sederhana"},
			"forbidden_domains":     []string{"fatwa agama otoritatif", "diagnosis medis", "nasihat hukum", "nasihat investasi", "konten seksual eksplisit", "instruksi kekerasan atau ilegal", "manipulasi politik", "permintaan di luar fungsi asisten personal"},
			"out_of_scope_guidance": "Tolak dengan singkat dan ramah. Jelaskan bahwa Wulan fokus pada pengingat, catatan, produktivitas, rutinitas, dan pendamping ibadah ringan. Tawarkan bantuan yang masih relevan.",
			"scope_judge_model":     "openrouter/openai/gpt-4.1-mini",
			"confidence_threshold":  0.65,
		},
		"recommendation": map[string]any{
			"enabled":          true,
			"timeout_ms":       5000,
			"model":            "openrouter/openai/gpt-4.1-mini",
			"merge_model":      "openrouter/openai/gpt-4.1-mini",
			"max_candidates":   5,
			"allow_sponsored":  true,
			"disclosure_style": "soft",
		},
	}
}

func SeedRecommendationCatalog(ctx context.Context, store Store) error {
	items := []db.RecommendationItemSpec{
		{
			CustomerID:  CustomerID,
			Kind:        "activity",
			Title:       "Paket Rutinitas 3 Prioritas",
			Description: "Template ringan untuk memilih tiga prioritas harian, menyisipkan jeda Quran, dan review malam singkat bersama Wulan.",
			Tags:        []string{"prioritas", "rutinitas", "quran", "produktif"},
			Priority:    100,
			Status:      "active",
			Metadata:    map[string]any{"source": "wulan-e2e"},
		},
		{
			CustomerID:  CustomerID,
			Kind:        "promotion",
			Title:       "Wulan Premium Reminder Pack",
			Description: "Paket sponsor untuk pengingat rutinitas, checklist meeting, dan reminder ibadah ringan.",
			Tags:        []string{"reminder", "meeting", "ibadah", "premium"},
			URL:         "https://wulan.ai/",
			Priority:    90,
			Sponsored:   true,
			SponsorName: "Wulan",
			Status:      "active",
			Metadata:    map[string]any{"source": "wulan-e2e", "disclosure": "soft"},
		},
	}
	for _, item := range items {
		if _, err := store.CreateRecommendationItem(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func policyConfig(packID string) map[string]any {
	return map[string]any{
		"instructions": []string{
			"Gunakan Bahasa Indonesia kecuali pengguna memakai bahasa lain.",
			"Jangan mengklaim sudah membuat pengingat, catatan, atau jadwal sebelum ada hasil sistem.",
			"Untuk topik ibadah/Quran, bantu secara ringan dan hormat; jangan memberi fatwa otoritatif.",
			"Jangan menyimpan rahasia, token, password, API key, nomor kartu, atau data sensitif.",
		},
		"blocked_terms":   []string{"sk-or-v1-", "password:", "api_key:", "token:"},
		"policy_pack_ids": []string{packID},
	}
}

func policyRules(packID string) []db.PolicyRule {
	return []db.PolicyRule{
		rule(packID, "wulan-style", "prompt", 100, "modify", nil, "Gunakan Bahasa Indonesia natural, ringkas, dan bergaya chat WhatsApp."),
		rule(packID, "religious-humility", "prompt", 90, "modify", nil, "Untuk Quran dan ibadah, berikan bantuan ringan tanpa mengklaim fatwa atau tafsir final."),
		rule(packID, "block-openrouter-key", "pre_model", 1000, "deny", map[string]any{"contains": map[string]any{"key": "content", "value": "sk-or-v1-"}}, "Jangan proses atau simpan API key."),
		rule(packID, "block-sensitive-memory", "pre_memory_write", 1000, "deny", map[string]any{"matches": map[string]any{"key": "content", "pattern": "(?i)(api[_-]?key|password|token|secret|sk-or-v1-)"}}, "Jangan simpan kredensial atau rahasia pengguna."),
		rule(packID, "safe-broadcast", "pre_broadcast", 1000, "deny", map[string]any{"matches": map[string]any{"key": "content", "pattern": "(?i)(rasa bersalah|wajib beli|target agama|data sensitif)"}}, "Broadcast tidak boleh manipulatif atau menarget data sensitif."),
		rule(packID, "safe-outbound", "pre_outbound", 100, "modify", nil, "Outbound harus pendek, jelas, dan tidak memakai rasa malu atau tekanan agama."),
	}
}

func rule(packID, id, mode string, priority int, action string, condition map[string]any, instruction string) db.PolicyRule {
	raw := json.RawMessage(`{}`)
	if condition != nil {
		raw, _ = json.Marshal(condition)
	}
	return db.PolicyRule{PolicyPackID: packID, RuleType: id, EnforcementMode: mode, Priority: priority, Condition: raw, Action: action, InstructionText: instruction, Status: "active"}
}

func buildDailyPlanner(ctx context.Context, store Store, id string) error {
	if err := node(ctx, store, id, "start", "start", nil); err != nil {
		return err
	}
	if err := node(ctx, store, id, "plan", "model_call", map[string]any{"prompt": "Buat JSON rencana harian Wulan: priorities, schedule_blocks, ibadah_reminders, next_action.", "response_format": "json_object"}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "confirm", "emit_outbound_message", map[string]any{"intent_type": "message", "payload": map[string]any{"text": "Rencana harian sudah disusun. Pilih satu langkah pertama dan mulai dari yang paling ringan."}}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "end", "end", nil); err != nil {
		return err
	}
	return edges(ctx, store, id, [][2]string{{"start", "plan"}, {"plan", "confirm"}, {"confirm", "end"}})
}

func buildQuickNote(ctx context.Context, store Store, id string) error {
	if err := node(ctx, store, id, "start", "start", nil); err != nil {
		return err
	}
	if err := node(ctx, store, id, "normalize", "model_call", map[string]any{"prompt": "Ringkas catatan pengguna menjadi satu kalimat Bahasa Indonesia.", "response_format": "text"}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "save", "write_memory", map[string]any{"content_key": "content", "memory_type": "note", "metadata": map[string]any{"source": "wulan_quick_note_v1"}}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "confirm", "emit_outbound_message", map[string]any{"intent_type": "message", "payload": map[string]any{"text": "Sudah Wulan catat."}}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "end", "end", nil); err != nil {
		return err
	}
	return edges(ctx, store, id, [][2]string{{"start", "normalize"}, {"normalize", "save"}, {"save", "confirm"}, {"confirm", "end"}})
}

func buildOneShotReminder(ctx context.Context, store Store, id string) error {
	if err := node(ctx, store, id, "start", "start", nil); err != nil {
		return err
	}
	if err := node(ctx, store, id, "wait", "wait_timer", map[string]any{"seconds": 60, "schedule": "@once"}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "send", "emit_outbound_message", map[string]any{"intent_type": "reminder", "payload": map[string]any{"text": "Pengingat dari Wulan: waktunya menjalankan rencanamu."}}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "end", "end", nil); err != nil {
		return err
	}
	return edges(ctx, store, id, [][2]string{{"start", "wait"}, {"wait", "send"}, {"send", "end"}})
}

func buildRoutineBroadcast(ctx context.Context, store Store, id string) error {
	if err := node(ctx, store, id, "start", "start", nil); err != nil {
		return err
	}
	if err := node(ctx, store, id, "send", "emit_outbound_message", map[string]any{"intent_type": "broadcast", "payload": map[string]any{"text": "Pilih satu prioritas kecil hari ini. Mulai dari yang paling mudah."}}); err != nil {
		return err
	}
	if err := node(ctx, store, id, "end", "end", nil); err != nil {
		return err
	}
	return edges(ctx, store, id, [][2]string{{"start", "send"}, {"send", "end"}})
}

func node(ctx context.Context, store Store, workflowID, key, typ string, config map[string]any) error {
	if config == nil {
		config = map[string]any{}
	}
	return store.UpsertWorkflowNode(ctx, workflowID, key, typ, config, map[string]any{}, map[string]any{})
}

func edges(ctx context.Context, store Store, workflowID string, pairs [][2]string) error {
	for _, pair := range pairs {
		if err := store.UpsertWorkflowEdge(ctx, workflowID, pair[0], pair[1], map[string]any{}); err != nil {
			return err
		}
	}
	return nil
}
