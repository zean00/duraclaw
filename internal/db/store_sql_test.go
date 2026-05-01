package db

import (
	"os"
	"strings"
	"testing"
)

func TestStoreClaimQueriesUseSkipLocked(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	remindersRaw, err := os.ReadFile("reminders.go")
	if err != nil {
		t.Fatal(err)
	}
	sharedRaw, err := os.ReadFile("shared_scheduler.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw) + string(remindersRaw) + string(sharedRaw)
	for _, want := range []string{
		"ClaimDueReminderSubscriptions",
		"ClaimDueSharedSchedulerJobs",
		"ClaimCompletedSharedSchedulerRunDeliveries",
		"FOR UPDATE SKIP LOCKED",
		"lease_expires_at < now()",
		"d.status='processing' AND d.updated_at < now() - interval '5 minutes'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("claim SQL missing %q", want)
		}
	}
	if strings.Count(sql, "FOR UPDATE SKIP LOCKED") < 6 {
		t.Fatalf("expected run, scheduler, outbox, reminder, shared scheduler, and delivery claim queries to use FOR UPDATE SKIP LOCKED")
	}
}

func TestStoreHasQueueStatsForReadiness(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"QueueStats", "runs WHERE state='queued'", "async_outbox WHERE completed_at IS NULL", "async_write_jobs WHERE state='queued'", "scheduler_jobs WHERE enabled=true"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("queue stats missing %q", want)
		}
	}
}

func TestStorePersistsSchedulerJobScopeColumns(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"INSERT INTO scheduler_jobs(customer_id,job_type,schedule,next_run_at,payload,metadata,user_id,agent_instance_id,session_id)",
		"WHERE customer_id=$1 AND user_id=$2",
		"WHERE id=$1 AND customer_id=$2 AND user_id=$3",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("scheduler job scope SQL missing %q", want)
		}
	}
	if strings.Contains(sql, "payload->>'user_id'=$") {
		t.Fatal("user-scoped scheduler management must not rely on payload user_id")
	}
}

func TestEnsureSessionDoesNotImplicitlyReassignAgentInstance(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if strings.Contains(sql, "agent_instance_id=EXCLUDED.agent_instance_id") {
		t.Fatalf("EnsureSession must not reassign sessions without a transfer record")
	}
}

func TestStoreHasSessionTransferQueries(t *testing.T) {
	raw, err := os.ReadFile("sessions.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ReassignSession",
		"FOR UPDATE",
		"session_agent_instance_transfers",
		"LatestSessionTransfer",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("session transfer store missing %q", want)
		}
	}
}

func TestStoreSnapshotsAgentInstanceVersionOnRunCreation(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	agentRaw, err := os.ReadFile("agent_instances.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw) + string(agentRaw)
	for _, want := range []string{
		"currentAgentInstanceVersionID",
		"agent_instance_version_id",
		"COALESCE(agent_instance_version_id::text,'')",
		"ensureDefaultAgentInstanceVersion",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("agent instance version snapshot missing %q", want)
		}
	}
}

func TestCreateRunUsesPersistedSessionAgentInstance(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sessionsRaw, err := os.ReadFile("sessions.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw) + string(sessionsRaw)
	for _, want := range []string{
		"sessionAgentInstanceID",
		"effectiveAgentInstanceID",
		"currentAgentInstanceVersionID(ctx, c.CustomerID, effectiveAgentInstanceID)",
		"c.CustomerID, c.UserID, effectiveAgentInstanceID, versionArg",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("CreateRun must use persisted session agent instance, missing %q", want)
		}
	}
}

func TestCompleteReminderSubscriptionDisablesOneShotSchedules(t *testing.T) {
	raw, err := os.ReadFile("reminders.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"nextRunAt.IsZero()", "enabled=false", "lease_owner=NULL"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("CompleteReminderSubscription should disable one-shot subscriptions, missing %q", want)
		}
	}
}

func TestStoreHasLeaseExtensionOwnershipCheck(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"ExtendRunLease", "lease_owner=$2", "state IN ('leased','running','running_workflow')"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}

func TestStoreCanReleaseOutboxForRetry(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"ReleaseOutbox", "ExtendOutboxClaim", "claim_expires_at=now()+$3::interval", "claimed_at=NULL", "claim_expires_at=NULL", "available_at=$3", "claim_owner=$2", "RowsAffected() == 0"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
	if !strings.Contains(sql, "claim_expires_at < now()") {
		t.Fatal("outbox claim should recover expired claims after worker crashes")
	}
}

func TestEventsPageHasLimit(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "EventsPage") || !strings.Contains(sql, "LIMIT $3") {
		t.Fatalf("events query should be paginated")
	}
}

func TestPolicyRulesCanBePinnedByAgentVersion(t *testing.T) {
	policies, err := os.ReadFile("policies.go")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := os.ReadFile("agent_instances.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(policies) + string(agents)
	for _, want := range []string{
		"PolicyRulesForScopeAndPacks",
		"r.policy_pack_id::text = ANY($2)",
		"JOIN policy_assignments a ON a.policy_pack_id=p.id",
		"a.enabled=true",
		"(a.customer_id=$3 AND a.agent_instance_id=$4)",
		`"policy_pack_ids"`,
		"validatePolicyConfigValues",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("pinned policy pack support missing %q", want)
		}
	}
}

func TestRunByIdempotencyKeyIsCustomerScoped(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "WHERE customer_id=$1 AND session_id=$2 AND idempotency_key=$3") {
		t.Fatalf("RunByIdempotencyKey must include customer_id in lookup")
	}
}

func TestOutboundStatusUpdatesBroadcastTargets(t *testing.T) {
	raw, err := os.ReadFile("outbound.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"NormalizeOutboundIntentStatus", `status == "sent"`, "sent_to_nexus", "delivered", "UPDATE broadcast_targets", "outbound_intent_id=$1", "UPDATE broadcasts b", "INSERT INTO observability_events", "outbound."} {
		if !strings.Contains(sql, want) {
			t.Fatalf("outbound status update missing %q", want)
		}
	}
}

func TestNormalizeOutboundIntentStatus(t *testing.T) {
	got, err := NormalizeOutboundIntentStatus("sent")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sent_to_nexus" {
		t.Fatalf("got %q", got)
	}
	for _, status := range []string{"sent_to_nexus", "delivered", "failed", "cancelled"} {
		got, err := NormalizeOutboundIntentStatus(status)
		if err != nil || got != status {
			t.Fatalf("status=%s got=%q err=%v", status, got, err)
		}
	}
	if _, err := NormalizeOutboundIntentStatus("queued"); err == nil {
		t.Fatalf("expected invalid callback status")
	}
}

func TestBroadcastTargetSelectionQueries(t *testing.T) {
	raw, err := os.ReadFile("broadcasts.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"pgx.BeginFunc",
		"createOutboundIntentTx",
		"ResolveBroadcastTargets",
		"DISTINCT ON (user_id)",
		"agent_instance_id=$2",
		"reminder_subscriptions",
		"BroadcastTargetSelection",
		"resolveBroadcastSegment",
		"user_id_prefix",
		"updated_since",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("broadcast target selection missing %q", want)
		}
	}
}

func TestStoreCanLoadCompletedNonRetryableToolCalls(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"CompletedNonRetryableToolCalls", "retryable=false", "completed_at IS NOT NULL", "args_hash", "ToolCallCount", "ToolExecutionCount", "SELECT count(*) FROM tool_calls WHERE run_id=$1", "SELECT count(*) FROM mcp_calls WHERE run_id=$1"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}

func TestStoreHasRuntimeQuotaQueries(t *testing.T) {
	raw, err := os.ReadFile("runtime_limits.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"UpsertCustomerRuntimeLimits",
		"UpsertAgentInstanceRuntimeLimits",
		"UpsertUserRuntimeLimits",
		"UserRuntimeLimits",
		"EnforceRunQuota",
		"EnforceRunStartQuota",
		"EnforceWorkflowQuota",
		"EnforceBackgroundQuota",
		"QuotaExceededError",
		"state = ANY($3)",
		"workflow_runs wr",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("runtime limit store missing %q", want)
		}
	}
}

func TestStoreHasAsyncWriteClaimQueries(t *testing.T) {
	raw, err := os.ReadFile("async_writes.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"EnqueueAsyncWrite",
		"ClaimAsyncWriteJobs",
		"FOR UPDATE SKIP LOCKED",
		"CompleteAsyncWriteJob",
		"ReleaseAsyncWriteJob",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("async write store missing %q", want)
		}
	}
}

func TestStoreHasSessionSummaryBackgroundAndTraceQueries(t *testing.T) {
	summaryRaw, err := os.ReadFile("session_summaries.go")
	if err != nil {
		t.Fatal(err)
	}
	storeRaw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	knowledgeRaw, err := os.ReadFile("memory_knowledge.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(summaryRaw) + string(storeRaw) + string(knowledgeRaw)
	for _, want := range []string{
		"UpsertSessionSummary",
		"SessionSummary",
		"CreateBackgroundRun",
		"pg_advisory_xact_lock",
		"enforceBackgroundQuotaTx",
		"'background'",
		"MarkRunBackground",
		"BackgroundRuns",
		"withTraceCheckpoint",
		"RunTraceContext",
		"trace_id",
		"traceparent",
		"SearchKnowledgeText",
		"SearchKnowledgeHybrid",
		"ListKnowledgeDocumentsByScope",
		"scope='shared'",
		"OR scope='shared'",
		"to_tsvector('simple', content)",
		"plainto_tsquery('simple', $2)",
		"ILIKE '%' || $2 || '%'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}

func TestStoreWritesDurableObservabilityForExecutionCalls(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		`AddObservabilityEvent(ctx, "", runID, "model.started"`,
		`AddObservabilityEvent(ctx, "", runID, "tool.started"`,
		`AddObservabilityEvent(ctx, "", runID, "mcp.started"`,
		`AddObservabilityEvent(ctx, "", runID, "processor.started"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing durable observability hook %q", want)
		}
	}
}

func TestCompleteSchedulerJobDisablesOneShotJobs(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"nextRunAt.IsZero()", "enabled=false", "lease_owner=NULL"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("CompleteSchedulerJob should disable one-shot jobs, missing %q", want)
		}
	}
}

func TestStableArgsHashIsOrderIndependent(t *testing.T) {
	a := StableArgsHash("tool", map[string]any{"a": float64(1), "b": "x"})
	b := StableArgsHash("tool", map[string]any{"b": "x", "a": float64(1)})
	if a == "" || a != b {
		t.Fatalf("hashes differ: %q %q", a, b)
	}
}
