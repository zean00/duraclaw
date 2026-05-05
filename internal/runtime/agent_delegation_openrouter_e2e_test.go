package runtime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/outbound"
	"duraclaw/internal/providers"
	"duraclaw/internal/tools"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOpenRouterAgentDelegationE2E(t *testing.T) {
	dsn := os.Getenv("DURACLAW_E2E_DATABASE_URL")
	key := os.Getenv("DURACLAW_E2E_OPENROUTER_KEY")
	if dsn == "" || key == "" {
		t.Skip("DURACLAW_E2E_DATABASE_URL and DURACLAW_E2E_OPENROUTER_KEY are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(pool)
	registry := providers.NewRegistry("openrouter")
	registry.Register("openrouter", providers.OpenRouterProvider{
		APIKey:       key,
		DefaultModel: "openai/gpt-4.1-mini",
		Title:        "Duraclaw agent delegation E2E",
	})
	worker := NewWorkerWithProviders(store, registry, providers.ModelConfig{Primary: "openrouter/openai/gpt-4.1-mini"}, "delegation-e2e").
		WithOutbound(outbound.NewService(store)).
		WithRunRefinement(300*time.Millisecond, 2)
	worker.SetToolRegistry(tools.NewRegistry())

	suffix := strings.ReplaceAll(time.Now().Format("20060102150405.000000000"), ".", "")
	customerID := "delegation-e2e-customer-" + suffix
	userID := "delegation-e2e-user"
	sourceAgent := "source-agent"
	targetAgent := "finance-agent"
	parentSession := "parent-session"

	sourceVersion, err := store.CreateAgentInstanceVersion(ctx, db.AgentInstanceVersionSpec{
		CustomerID: customerID, AgentInstanceID: sourceAgent, Name: "Source",
		ModelConfig:        map[string]any{"primary": "openrouter/openai/gpt-4.1-mini"},
		SystemInstructions: "You coordinate work and delegate explicit mentions.",
		ProfileConfig: map[string]any{
			"personality":      "brief coordinator",
			"domain_scope":     map[string]any{"allowed_domains": []string{"general assistance", "coordination", "finance planning"}, "confidence_threshold": 0.1},
			"agent_delegation": map[string]any{"enabled": true, "max_mentions_per_message": 2},
		},
		ActivateImmediately: true,
	})
	if err != nil || sourceVersion == nil {
		t.Fatalf("source version=%#v err=%v", sourceVersion, err)
	}
	if _, err := store.CreateAgentInstanceVersion(ctx, db.AgentInstanceVersionSpec{
		CustomerID: customerID, AgentInstanceID: targetAgent, Name: "Finance",
		ModelConfig:        map[string]any{"primary": "openrouter/openai/gpt-4.1-mini"},
		SystemInstructions: "You are a concise finance specialist. Answer delegated finance tasks directly.",
		ProfileConfig: map[string]any{
			"personality":  "concise finance specialist",
			"domain_scope": map[string]any{"allowed_domains": []string{"finance planning", "budgeting", "general assistance"}, "confidence_threshold": 0.1},
		},
		ActivateImmediately: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentDelegationHandle(ctx, db.AgentDelegationHandle{
		CustomerID: customerID, Handle: "finance", AgentInstanceID: targetAgent, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAgentDelegationAccessRule(ctx, db.AgentDelegationAccessRule{
		CustomerID: customerID, AgentInstanceID: sourceAgent, AllowedAgents: []string{"finance"},
	}); err != nil {
		t.Fatal(err)
	}

	parentRun, err := store.CreateRun(ctx, db.ACPContext{
		CustomerID: customerID, UserID: userID, AgentInstanceID: sourceAgent, SessionID: parentSession, RequestID: "parent-req", IdempotencyKey: "parent-idem",
	}, map[string]any{"text": "@finance Please estimate a simple monthly budget split for a family saving for school fees."})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := worker.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("parent run processed=%v err=%v", ok, err)
	}
	var delegationID, childRunID, childSessionID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, child_run_id::text, child_session_id
		FROM agent_delegations
		WHERE parent_run_id=$1`, parentRun.ID).Scan(&delegationID, &childRunID, &childSessionID); err != nil {
		t.Fatal(err)
	}
	if childSessionID == "" {
		t.Fatal("expected child session id")
	}
	if ok, err := worker.RunOnce(ctx); err != nil || !ok {
		t.Fatalf("child run processed=%v err=%v", ok, err)
	}
	var status, result string
	if err := pool.QueryRow(ctx, `SELECT status, result_text FROM agent_delegations WHERE id=$1`, delegationID).Scan(&status, &result); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || strings.TrimSpace(result) == "" {
		t.Fatalf("delegation status=%s result=%q", status, result)
	}
	var parentDelegatedMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM messages
		WHERE customer_id=$1 AND session_id=$2 AND role='assistant' AND content ? 'agent_delegation'`,
		customerID, parentSession).Scan(&parentDelegatedMessages); err != nil {
		t.Fatal(err)
	}
	if parentDelegatedMessages != 1 {
		t.Fatalf("parent delegated messages=%d", parentDelegatedMessages)
	}
	var payloadRaw []byte
	if err := pool.QueryRow(ctx, `
		SELECT payload FROM outbound_intents
		WHERE customer_id=$1 AND session_id=$2 AND intent_type='message' AND payload->>'source'='agent_delegation'
		ORDER BY created_at DESC LIMIT 1`, customerID, parentSession).Scan(&payloadRaw); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Artifacts) != 1 || payload.Artifacts[0]["session_id"] != childSessionID {
		t.Fatalf("artifact payload=%s child_session=%s", string(payloadRaw), childSessionID)
	}

	refinementRun, err := store.CreateRun(ctx, db.ACPContext{
		CustomerID: customerID, UserID: userID, AgentInstanceID: sourceAgent, SessionID: parentSession, RequestID: "refine-req", IdempotencyKey: "refine-idem",
	}, map[string]any{
		"text":       "@finance Please estimate a simple monthly budget split.\n\nCompleted parent tool artifacts:\n" + string(mustJSONForTest(db.AgentDelegationArtifact(db.AgentDelegation{ID: delegationID, TargetHandle: "finance", TargetAgentInstanceID: targetAgent, ChildSessionID: childSessionID, ChildRunID: childRunID, Status: "completed"}))),
		"refinement": map[string]any{"parent_run_id": parentRun.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	refinementRun.RefinementParentRunID = parentRun.ID
	if shouldScanAgentDelegations(refinementRun, false, scopeJudgement{InScope: true}) {
		t.Fatal("refinement run would scan delegations")
	}
}

func mustJSONForTest(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
