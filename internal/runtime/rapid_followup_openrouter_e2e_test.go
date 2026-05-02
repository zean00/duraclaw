package runtime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/outbound"
	"duraclaw/internal/providers"
	"duraclaw/internal/tools"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOpenRouterRapidFollowupRefinementE2E(t *testing.T) {
	dsn := os.Getenv("DURACLAW_E2E_DATABASE_URL")
	key := os.Getenv("DURACLAW_E2E_OPENROUTER_KEY")
	if dsn == "" || key == "" {
		t.Skip("DURACLAW_E2E_DATABASE_URL and DURACLAW_E2E_OPENROUTER_KEY are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
		Title:        "Duraclaw rapid follow-up E2E",
	})
	worker := NewWorkerWithProviders(store, registry, providers.ModelConfig{Primary: "openrouter/openai/gpt-4.1-mini"}, "rapid-e2e").
		WithOutbound(outbound.NewService(store)).
		WithRunRefinement(10*time.Second, 2)
	worker.SetToolRegistry(tools.NewRegistry())

	acp := db.ACPContext{
		CustomerID: "rapid-e2e-customer", UserID: "rapid-e2e-user", AgentInstanceID: "rapid-e2e-agent",
		SessionID: "rapid-e2e-session", RequestID: "req-a", IdempotencyKey: "idem-a",
	}
	runA, err := store.CreateRun(ctx, acp, map[string]any{
		"text": "Draft a concise five point plan for preparing a family trip to Kyoto. Include timing and packing details.",
	})
	if err != nil {
		t.Fatal(err)
	}
	doneA := make(chan error, 1)
	go func() {
		processed, err := worker.RunOnce(ctx)
		if err == nil && !processed {
			err = context.Canceled
		}
		doneA <- err
	}()
	waitForInterruptWindow(t, ctx, pool, runA.ID)

	ack, err := store.DeferRunIfActive(ctx, db.ACPContext{
		CustomerID: acp.CustomerID, UserID: acp.UserID, AgentInstanceID: acp.AgentInstanceID,
		SessionID: acp.SessionID, RequestID: "req-b", IdempotencyKey: "idem-b",
	}, map[string]any{"text": "Also adjust the advice for traveling with a 5 year old and avoid making it too long."}, 10*time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ack == nil || ack.State != "deferred" || ack.ActiveRunID != runA.ID || ack.DeferredMessageID == "" {
		t.Fatalf("unexpected deferred ack: %#v", ack)
	}
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}

	var suppressed bool
	var depth int
	var state string
	if err := pool.QueryRow(ctx, `SELECT suppress_direct_outbound, refinement_depth, state FROM runs WHERE id=$1`, runA.ID).Scan(&suppressed, &depth, &state); err != nil {
		t.Fatal(err)
	}
	if !suppressed || depth != 0 || state != "completed" {
		t.Fatalf("run A state=%s suppressed=%v depth=%d", state, suppressed, depth)
	}
	var parentFinalMessageID *string
	var parentAssistantMessages int
	if err := pool.QueryRow(ctx, `SELECT final_message_id::text FROM runs WHERE id=$1`, runA.ID).Scan(&parentFinalMessageID); err != nil {
		t.Fatal(err)
	}
	if parentFinalMessageID != nil {
		t.Fatalf("suppressed parent run should not have final_message_id, got %q", *parentFinalMessageID)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE run_id=$1 AND role='assistant'`, runA.ID).Scan(&parentAssistantMessages); err != nil {
		t.Fatal(err)
	}
	if parentAssistantMessages != 0 {
		t.Fatalf("suppressed parent draft leaked into message history: %d assistant messages", parentAssistantMessages)
	}

	refinementID := waitForRefinementRun(t, ctx, pool, runA.ID)
	processed, err := worker.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("refinement processed=%v err=%v", processed, err)
	}

	var messageOutbox, typingOutbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_intents WHERE intent_type='message'`).Scan(&messageOutbox); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_intents WHERE intent_type='typing'`).Scan(&typingOutbox); err != nil {
		t.Fatal(err)
	}
	if messageOutbox != 1 || typingOutbox != 1 {
		t.Fatalf("message_outbox=%d typing_outbox=%d", messageOutbox, typingOutbox)
	}

	var finalPayload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM outbound_intents WHERE intent_type='message' LIMIT 1`).Scan(&finalPayload); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(finalPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Parts) == 0 || payload.Parts[0].Text == "" {
		t.Fatalf("empty final outbound payload: %s", string(finalPayload))
	}
	var totalAssistantMessages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE customer_id=$1 AND session_id=$2 AND role='assistant'`, acp.CustomerID, acp.SessionID).Scan(&totalAssistantMessages); err != nil {
		t.Fatal(err)
	}
	if totalAssistantMessages != 1 {
		t.Fatalf("expected only final visible assistant response in history, got %d", totalAssistantMessages)
	}
	t.Logf("run_a=%s refinement_run=%s deferred=%s final_chars=%d", runA.ID, refinementID, ack.DeferredMessageID, len(payload.Parts[0].Text))
}

func waitForInterruptWindow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var started *time.Time
		_ = pool.QueryRow(ctx, `SELECT interrupt_window_started_at FROM runs WHERE id=$1`, runID).Scan(&started)
		if started != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s did not start interrupt window", runID)
}

func waitForRefinementRun(t *testing.T, ctx context.Context, pool *pgxpool.Pool, parentRunID string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var id string
		err := pool.QueryRow(ctx, `SELECT id::text FROM runs WHERE refinement_parent_run_id=$1 LIMIT 1`, parentRunID).Scan(&id)
		if err == nil && id != "" {
			return id
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("refinement run was not created for %s", parentRunID)
	return ""
}
