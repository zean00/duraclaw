package db

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func integrationStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return NewStore(pool), pool.Close
}

func TestStoreDurableRunHappyPathPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	acpCtx := ACPContext{
		CustomerID: "c-" + suffix, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "i",
	}
	run, err := store.CreateRun(ctx, acpCtx, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimRun(ctx, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claimed=%#v run=%#v", claimed, run)
	}
	stepID, err := store.StartRunStep(ctx, run.ID, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunStep(ctx, run.ID, stepID, "succeeded", map[string]any{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	msgID, err := store.InsertMessage(ctx, run.CustomerID, run.SessionID, run.ID, "assistant", map[string]any{"text": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunWithMessage(ctx, run.ID, msgID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "completed" {
		t.Fatalf("state=%s", got.State)
	}
}

func TestStoreSessionReassignmentAndVersionSnapshotPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-reassign-" + suffix
	base := ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "agent-a", SessionID: "s", RequestID: "r1", IdempotencyKey: "i1"}
	first, err := store.CreateRun(ctx, base, map[string]any{"text": "first"})
	if err != nil {
		t.Fatal(err)
	}
	if first.AgentInstanceID != "agent-a" || first.AgentInstanceVersionID == "" {
		t.Fatalf("first run=%#v", first)
	}
	v2, err := store.CreateAgentInstanceVersion(ctx, AgentInstanceVersionSpec{
		CustomerID: customerID, AgentInstanceID: "agent-b", Name: "b", SystemInstructions: "version b", ActivateImmediately: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	withoutTransfer := base
	withoutTransfer.AgentInstanceID = "agent-b"
	withoutTransfer.RequestID = "r2"
	withoutTransfer.IdempotencyKey = "i2"
	second, err := store.CreateRun(ctx, withoutTransfer, map[string]any{"text": "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.AgentInstanceID != "agent-a" {
		t.Fatalf("run used request agent without transfer: %#v", second)
	}
	if _, err := store.ReassignSession(ctx, customerID, "s", "agent-b", "upgrade", nil); err != nil {
		t.Fatal(err)
	}
	afterTransfer := withoutTransfer
	afterTransfer.RequestID = "r3"
	afterTransfer.IdempotencyKey = "i3"
	third, err := store.CreateRun(ctx, afterTransfer, map[string]any{"text": "third"})
	if err != nil {
		t.Fatal(err)
	}
	if third.AgentInstanceID != "agent-b" || third.AgentInstanceVersionID != v2.ID {
		t.Fatalf("run did not snapshot reassigned agent version: run=%#v version=%#v", third, v2)
	}
}

func TestStoreReminderSubscriptionLeaseCompletionPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-reminder-" + suffix
	nextRunAt := time.Now().UTC().Add(-time.Minute)
	sub, err := store.CreateReminderSubscription(ctx, ReminderSubscriptionSpec{
		CustomerID: customerID, UserID: "u", SessionID: "s", AgentInstanceID: "a", Title: "once", Schedule: "@once", NextRunAt: nextRunAt,
		Payload: map[string]any{"text": "wake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDueReminderSubscriptions(ctx, "test", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].ID != sub.ID || claimed[0].LeaseOwner == nil || *claimed[0].LeaseOwner != "test" {
		t.Fatalf("claimed=%#v sub=%#v", claimed, sub)
	}
	if err := store.CompleteReminderSubscription(ctx, sub.ID, nextRunAt, time.Time{}); err != nil {
		t.Fatal(err)
	}
	subs, err := store.ListReminderSubscriptions(ctx, customerID, "u", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Enabled {
		t.Fatalf("subscription should be disabled after one-shot completion: %#v", subs)
	}
}

func TestStoreLeaseRecoveryPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	run, err := store.CreateRun(ctx, ACPContext{CustomerID: "c-lease-" + suffix, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "i"}, map[string]any{"text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimRun(ctx, "owner", -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != run.ID {
		t.Fatalf("claimed=%#v run=%#v", claimed, run)
	}
	recovered, err := store.RecoverExpiredLeases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == 0 {
		t.Fatalf("expected expired lease recovery")
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "queued" {
		t.Fatalf("state=%s", got.State)
	}
}

func TestStoreOutboxRetryPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	id, err := store.EnqueueOutbox(ctx, "topic", map[string]any{"ok": true}, time.Now().UTC().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ClaimOutbox(ctx, "owner", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || items[0].ID != id || items[0].ClaimOwner == nil || *items[0].ClaimOwner != "owner" {
		t.Fatalf("items=%#v id=%d", items, id)
	}
	if err := store.ReleaseOutbox(ctx, id, -time.Second); err != nil {
		t.Fatal(err)
	}
	items, err = store.ClaimOutbox(ctx, "owner-2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || items[0].ID != id || items[0].ClaimOwner == nil || *items[0].ClaimOwner != "owner-2" {
		t.Fatalf("items after release=%#v", items)
	}
}

func TestKnowledgeVectorSearchPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-vector-" + suffix
	docID, err := store.CreateKnowledgeDocument(ctx, customerID, "vectors", "test://vectors", nil)
	if err != nil {
		t.Fatal(err)
	}
	nearID, err := store.AddKnowledgeChunk(ctx, docID, customerID, 0, "near vector chunk", nil)
	if err != nil {
		t.Fatal(err)
	}
	farID, err := store.AddKnowledgeChunk(ctx, docID, customerID, 1, "far vector chunk", nil)
	if err != nil {
		t.Fatal(err)
	}
	near := make([]float32, 768)
	far := make([]float32, 768)
	query := make([]float32, 768)
	near[0] = 0.01
	far[0] = 10
	if err := store.SetKnowledgeChunkEmbedding(ctx, nearID, customerID, near); err != nil {
		t.Fatal(err)
	}
	if err := store.SetKnowledgeChunkEmbedding(ctx, farID, customerID, far); err != nil {
		t.Fatal(err)
	}
	got, err := store.SearchKnowledgeChunks(ctx, customerID, query, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != nearID || got[1].ID != farID {
		t.Fatalf("unexpected vector order: %#v", got)
	}
	hybrid, err := store.SearchKnowledgeHybrid(ctx, customerID, "far", query, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hybrid) == 0 || hybrid[0].ID != nearID {
		t.Fatalf("hybrid should prefer vector result first: %#v", hybrid)
	}
}

func TestStoreWorkflowTimerResumePostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-timer-" + suffix
	run, err := store.CreateRun(ctx, ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "i"}, map[string]any{"text": "timer"})
	if err != nil {
		t.Fatal(err)
	}
	workflowID, err := store.CreateWorkflowDefinition(ctx, "timer", 1, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	workflowRunID, err := store.StartWorkflowRun(ctx, run.ID, workflowID, 1, "wait", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeWorkflowNodeStates(ctx, workflowRunID, []WorkflowNode{{NodeKey: "wait"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowRunState(ctx, workflowRunID, run.ID, "awaiting_user", "wait", map[string]any{"timer": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunState(ctx, run.ID, "awaiting_user", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ResumeWorkflowTimer(ctx, run.ID, workflowRunID, "wait", map[string]any{"scheduler_job_id": "job-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "queued" {
		t.Fatalf("state=%s", got.State)
	}
	states, err := store.WorkflowNodeStates(ctx, workflowRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Status != "succeeded" {
		t.Fatalf("states=%#v", states)
	}
}

func TestStoreSameSessionClaimSerializationPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-serialize-" + suffix
	first, err := store.CreateRun(ctx, ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r1", IdempotencyKey: "i1"}, map[string]any{"text": "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r2", IdempotencyKey: "i2"}, map[string]any{"text": "two"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimRun(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != first.ID {
		t.Fatalf("first claim=%#v want=%s second=%s", claimed, first.ID, second.ID)
	}
	blocked, err := store.ClaimRun(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if blocked != nil {
		t.Fatalf("same-session second run should not be claimed while first active: %#v", blocked)
	}
	msgID, err := store.InsertMessage(ctx, first.CustomerID, first.SessionID, first.ID, "assistant", map[string]any{"text": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRunWithMessage(ctx, first.ID, msgID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimRun(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != second.ID {
		t.Fatalf("second claim=%#v want=%s", claimed, second.ID)
	}
}

func TestStoreCreateBackgroundRunQuotaIsAtomicPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-bg-quota-" + suffix
	one := 1
	if _, err := store.UpsertAgentInstanceRuntimeLimits(ctx, RuntimeLimits{
		CustomerID: customerID, AgentInstanceID: "a", MaxBackgroundRuns: &one,
	}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.CreateBackgroundRun(ctx, ACPContext{
				CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "bg-" + string(rune('0'+i)),
			}, map[string]any{"text": "background"}, map[string]any{"state": "queued"})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	quotaFailures := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if IsQuotaExceeded(err) {
			quotaFailures++
			continue
		}
		t.Fatalf("unexpected error: %v", err)
	}
	if successes != 1 || quotaFailures != 1 {
		t.Fatalf("successes=%d quotaFailures=%d", successes, quotaFailures)
	}
}

func TestStoreQueueStatsPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	before, err := store.QueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-queue-" + suffix
	run, err := store.CreateRun(ctx, ACPContext{CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "r", IdempotencyKey: "i"}, map[string]any{"text": "queued"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueOutbox(ctx, "topic", map[string]any{"run_id": run.ID}, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueAsyncWrite(ctx, AsyncWriteSpec{CustomerID: customerID, RunID: run.ID, JobType: "observability_event", Payload: map[string]any{"event_type": "queued"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSchedulerJob(ctx, SchedulerJobSpec{
		CustomerID: customerID, UserID: "u", AgentInstanceID: "a", SessionID: "s", JobType: "cron", Schedule: "@once", NextRunAt: time.Now().UTC().Add(-time.Second),
		Input: map[string]any{"text": "wake"},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := store.QueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.RunsQueued < before.RunsQueued+1 || after.OutboxPending < before.OutboxPending+1 || after.AsyncWriteQueued < before.AsyncWriteQueued+1 || after.SchedulerDue < before.SchedulerDue+1 {
		t.Fatalf("before=%#v after=%#v", before, after)
	}
}

func TestStoreKnowledgeScopeRetrievalPostgres(t *testing.T) {
	store, cleanup := integrationStore(t)
	defer cleanup()
	ctx := context.Background()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	customerID := "c-knowledge-" + suffix
	otherCustomerID := "c-knowledge-other-" + suffix
	customerDoc, err := store.CreateKnowledgeDocumentWithScope(ctx, customerID, "customer", "customer doc", "src", nil)
	if err != nil {
		t.Fatal(err)
	}
	sharedDoc, err := store.CreateKnowledgeDocumentWithScope(ctx, otherCustomerID, "shared", "shared doc", "src", nil)
	if err != nil {
		t.Fatal(err)
	}
	privateOtherDoc, err := store.CreateKnowledgeDocumentWithScope(ctx, otherCustomerID, "customer", "private other", "src", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddKnowledgeChunk(ctx, customerDoc, customerID, 0, "duraclaw customer scoped durable fact", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddKnowledgeChunk(ctx, sharedDoc, otherCustomerID, 0, "duraclaw shared durable fact", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddKnowledgeChunk(ctx, privateOtherDoc, otherCustomerID, 0, "duraclaw private other fact", nil); err != nil {
		t.Fatal(err)
	}
	chunks, err := store.SearchKnowledgeText(ctx, customerID, "duraclaw", 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawCustomer, sawShared, sawPrivateOther bool
	for _, chunk := range chunks {
		if chunk.CustomerID == customerID && chunk.Scope == "customer" {
			sawCustomer = true
		}
		if chunk.CustomerID == otherCustomerID && chunk.Scope == "shared" {
			sawShared = true
		}
		if chunk.CustomerID == otherCustomerID && chunk.Scope == "customer" {
			sawPrivateOther = true
		}
	}
	if !sawCustomer || !sawShared || sawPrivateOther {
		t.Fatalf("chunks=%#v", chunks)
	}
	docs, err := store.ListKnowledgeDocumentsByScope(ctx, customerID, "all", 10)
	if err != nil {
		t.Fatal(err)
	}
	var sawCustomerDoc, sawSharedDoc bool
	for _, doc := range docs {
		if doc.ID == customerDoc && doc.Scope == "customer" {
			sawCustomerDoc = true
		}
		if doc.ID == sharedDoc && doc.Scope == "shared" {
			sawSharedDoc = true
		}
	}
	if !sawCustomerDoc || !sawSharedDoc {
		t.Fatalf("docs=%#v", docs)
	}
}
