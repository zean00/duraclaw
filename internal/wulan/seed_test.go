package wulan

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/policy"
)

func TestProfileConfigMatchesDuraclawShape(t *testing.T) {
	raw, err := json.Marshal(ProfileConfig())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Personality          string   `json:"personality"`
		CommunicationStyle   string   `json:"communication_style"`
		LanguageCapabilities []string `json:"language_capabilities"`
		DomainScope          struct {
			AllowedDomains      []string `json:"allowed_domains"`
			ForbiddenDomains    []string `json:"forbidden_domains"`
			OutOfScopeGuidance  string   `json:"out_of_scope_guidance"`
			ScopeJudgeModel     string   `json:"scope_judge_model"`
			ConfidenceThreshold float64  `json:"confidence_threshold"`
		} `json:"domain_scope"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Personality == "" || cfg.CommunicationStyle == "" || len(cfg.LanguageCapabilities) != 2 {
		t.Fatalf("profile not populated: %#v", cfg)
	}
	if len(cfg.DomainScope.AllowedDomains) == 0 || len(cfg.DomainScope.ForbiddenDomains) == 0 {
		t.Fatalf("domain scope not populated: %#v", cfg.DomainScope)
	}
	if cfg.DomainScope.ConfidenceThreshold != 0.65 {
		t.Fatalf("threshold=%v", cfg.DomainScope.ConfidenceThreshold)
	}
}

func TestWulanPolicyRulesDenySecretsAndModifyStyle(t *testing.T) {
	store := &ruleStore{rules: policyRules("pack-1")}
	engine := policy.NewEngine(store)
	decision, err := engine.Evaluate(context.Background(), "pre_model", policy.Context{Content: "tolong simpan sk-or-v1-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "deny" {
		t.Fatalf("decision=%#v", decision)
	}
	instructions, err := engine.PromptInstructions(context.Background(), policy.Context{Content: "buatkan checklist"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instructions) < 2 || !strings.Contains(strings.Join(instructions, "\n"), "Bahasa Indonesia") {
		t.Fatalf("instructions=%#v", instructions)
	}
	decision, err = engine.Evaluate(context.Background(), "pre_memory_write", policy.Context{Content: "token: abc"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != "deny" {
		t.Fatalf("memory decision=%#v", decision)
	}
}

func TestSeedAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	truncateAll(t, ctx, pool)
	store := db.NewStore(pool)
	result, err := Seed(ctx, store, time.Date(2026, 4, 29, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentVersionID == "" || result.PolicyPackID == "" || result.DailyPlannerID == "" || result.QuickNoteID == "" || result.OneShotReminderID == "" || result.RoutineBroadcastID == "" {
		t.Fatalf("missing seed ids: %#v", result)
	}
	if result.ReminderID == "" || result.BroadcastID == "" || result.BroadcastTargetCount != 1 {
		t.Fatalf("missing reminder/broadcast ids: %#v", result)
	}
	versions, err := store.ListAgentInstanceVersions(ctx, CustomerID, AgentInstanceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].ActivatedAt == nil {
		t.Fatalf("versions=%#v", versions)
	}
	var profile map[string]any
	if err := json.Unmarshal(versions[0].ProfileConfig, &profile); err != nil {
		t.Fatal(err)
	}
	if profile["personality"] == "" {
		t.Fatalf("profile=%#v", profile)
	}
	rules, err := store.ListPolicyRules(ctx, result.PolicyPackID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) < 6 {
		t.Fatalf("rules=%#v", rules)
	}
	nodes, err := store.WorkflowNodes(ctx, result.OneShotReminderID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNode(nodes, "wait", "wait_timer") || !hasNode(nodes, "send", "emit_outbound_message") {
		t.Fatalf("reminder nodes=%#v", nodes)
	}
	subs, err := store.ListReminderSubscriptions(ctx, CustomerID, UserID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Timezone != "Asia/Jakarta" {
		t.Fatalf("subscriptions=%#v", subs)
	}
	broadcasts, err := store.ListBroadcasts(ctx, CustomerID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(broadcasts) != 1 || broadcasts[0].Title != "Wulan Daily Nudge" {
		t.Fatalf("broadcasts=%#v", broadcasts)
	}
}

type ruleStore struct {
	rules []db.PolicyRule
	evals []db.PolicyEvaluation
}

func (s *ruleStore) PolicyRulesForScope(_ context.Context, _, _, mode string) ([]db.PolicyRule, error) {
	var out []db.PolicyRule
	for _, rule := range s.rules {
		if rule.EnforcementMode == mode && rule.Status == "active" {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s *ruleStore) RecordPolicyEvaluation(_ context.Context, ev db.PolicyEvaluation) error {
	s.evals = append(s.evals, ev)
	return nil
}

func hasNode(nodes []db.WorkflowNode, key, typ string) bool {
	for _, node := range nodes {
		if node.NodeKey == key && node.NodeType == typ {
			return true
		}
	}
	return false
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
