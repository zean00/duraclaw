package db

import (
	"strings"
	"testing"
)

func TestInitialMigrationContainsRequiredDurabilitySchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	required := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"embedding vector(768)",
		"UNIQUE (customer_id, session_id, idempotency_key)",
		"lease_owner text",
		"'awaiting_user'",
		"CREATE TABLE IF NOT EXISTS model_calls",
		"CREATE TABLE IF NOT EXISTS tool_calls",
		"CREATE TABLE IF NOT EXISTS processor_calls",
		"CREATE INDEX IF NOT EXISTS model_calls_run_idx",
		"CREATE INDEX IF NOT EXISTS tool_calls_run_idx",
		"CREATE INDEX IF NOT EXISTS mcp_calls_run_idx",
		"CREATE INDEX IF NOT EXISTS scheduler_jobs_claim_idx",
		"CREATE INDEX IF NOT EXISTS async_outbox_claim_idx",
		"CREATE TABLE IF NOT EXISTS workflow_definitions",
		"CREATE TABLE IF NOT EXISTS workflow_runs",
		"CREATE TABLE IF NOT EXISTS workflow_node_runs",
		"CREATE TABLE IF NOT EXISTS workflow_node_states",
		"CREATE TABLE IF NOT EXISTS workflow_edge_activations",
		"CREATE TABLE IF NOT EXISTS memories",
		"CREATE TABLE IF NOT EXISTS knowledge_chunks",
		"embedding vector(768)",
		"CREATE TABLE IF NOT EXISTS outbound_intents",
		"CREATE INDEX IF NOT EXISTS outbound_intents_customer_status_idx",
		"CREATE TABLE IF NOT EXISTS broadcasts",
		"CREATE TABLE IF NOT EXISTS broadcast_targets",
		"CREATE INDEX IF NOT EXISTS observability_events_customer_created_idx",
		"CREATE TABLE IF NOT EXISTS agent_policies",
	}
	for _, want := range required {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestSecondMigrationContainsPolicyAndToolHashSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0002_policy_agent_workflow_v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS policy_packs",
		"CREATE TABLE IF NOT EXISTS policy_rules",
		"CREATE TABLE IF NOT EXISTS policy_assignments",
		"CREATE TABLE IF NOT EXISTS policy_evaluations",
		"ALTER TABLE tool_calls ADD COLUMN IF NOT EXISTS args_hash",
		"tool_calls_nonretry_hash_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestThirdMigrationContainsPreferencesSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0003_preferences.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS preferences",
		"condition jsonb NOT NULL DEFAULT '{}'::jsonb",
		"embedding vector(768)",
		"CREATE INDEX IF NOT EXISTS preferences_customer_user_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestFourthMigrationContainsReminderSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0004_reminders_and_embeddings.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS job_type",
		"CREATE TABLE IF NOT EXISTS reminder_subscriptions",
		"reminder_subscriptions_due_idx",
		"reminder_subscriptions_customer_user_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestFifthMigrationContainsSessionTransferAndReminderFanoutSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0005_session_transfers_and_reminder_fanout.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS session_agent_instance_transfers",
		"FOREIGN KEY (customer_id, session_id) REFERENCES sessions(customer_id, id)",
		"ALTER TABLE reminder_subscriptions ADD COLUMN IF NOT EXISTS lease_owner",
		"ALTER TABLE reminder_subscriptions ADD COLUMN IF NOT EXISTS lease_expires_at",
		"CREATE INDEX IF NOT EXISTS reminder_subscriptions_claim_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestSixthMigrationContainsAgentInstanceVersionSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0006_agent_instance_versions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS agent_instance_versions",
		"UNIQUE (customer_id, agent_instance_id, version)",
		"ALTER TABLE agent_instances ADD COLUMN IF NOT EXISTS current_version_id",
		"ALTER TABLE runs ADD COLUMN IF NOT EXISTS agent_instance_version_id",
		"CREATE INDEX IF NOT EXISTS runs_agent_instance_version_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestSeventhMigrationContainsRuntimeLimitsAndAsyncWrites(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0007_runtime_limits_and_async_writes.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS customer_runtime_limits",
		"CREATE TABLE IF NOT EXISTS agent_instance_runtime_limits",
		"CREATE TABLE IF NOT EXISTS async_write_jobs",
		"CHECK (state IN ('queued','leased','completed','failed','dropped','degraded'))",
		"CREATE INDEX IF NOT EXISTS async_write_jobs_claim_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestFifteenthMigrationContainsModelUsageQuotas(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0015_model_usage_quotas.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"max_daily_tokens",
		"max_weekly_model_cost_micros",
		"max_monthly_model_cost_micros",
		"CREATE TABLE IF NOT EXISTS model_usage_ledger",
		"CREATE INDEX IF NOT EXISTS model_usage_ledger_customer_period_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestSixteenthMigrationContainsMCPToolAccess(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0016_mcp_tool_access.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS mcp_tool_access_rules",
		"allowed_tools jsonb NOT NULL DEFAULT '[]'::jsonb",
		"denied_tools jsonb NOT NULL DEFAULT '[]'::jsonb",
		"PRIMARY KEY (customer_id, agent_instance_id, user_id, server_name)",
		"mcp_tool_access_rules_customer_agent_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestSeventeenthMigrationContainsUserQuotasAndUsage(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0017_user_quotas_usage.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS user_runtime_limits",
		"max_daily_tokens integer",
		"ALTER TABLE model_usage_ledger",
		"ADD COLUMN IF NOT EXISTS user_id",
		"UPDATE model_usage_ledger l",
		"model_usage_ledger_customer_user_period_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTwentiethMigrationContainsBuiltInToolAccess(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0020_builtin_tool_access.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS tool_access_rules",
		"allowed_tools jsonb NOT NULL DEFAULT '[]'::jsonb",
		"denied_tools jsonb NOT NULL DEFAULT '[]'::jsonb",
		"PRIMARY KEY (customer_id, agent_instance_id, user_id)",
		"tool_access_rules_customer_agent_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTwentyFirstMigrationContainsOutboxClaimLeases(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0021_outbox_claim_leases.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ALTER TABLE async_outbox ADD COLUMN IF NOT EXISTS claim_expires_at",
		"SET claim_expires_at=claimed_at + interval '5 minutes'",
		"async_outbox_claim_lease_idx",
		"completed_at, available_at, claim_expires_at",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTwentySecondMigrationContainsGeneratedBroadcasts(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0022_generated_broadcasts.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"generation_mode",
		"generation_request",
		"generation_run_id",
		"generation_failed",
		"broadcast_targets_generation_run_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTwentyThirdMigrationContainsBroadcastChannelControls(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0023_broadcast_recommendation_channel_controls.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"external_broadcast_id",
		"broadcasts_customer_external_id_idx",
		"channel_suppressed",
		"broadcasts_status_check",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestEighthMigrationContainsSummariesAndBackgroundSchema(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0008_summaries_and_background.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS session_summaries",
		"ALTER TABLE runs ADD COLUMN IF NOT EXISTS run_mode",
		"ALTER TABLE runs ADD COLUMN IF NOT EXISTS progress",
		"CREATE INDEX IF NOT EXISTS runs_background_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestNinthMigrationContainsRetrievalIndexes(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0009_retrieval_policy_mcp_depth.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"search_vector tsvector",
		"knowledge_chunks_search_vector_idx",
		"knowledge_chunks_embedding_ivfflat_idx",
		"vector_l2_ops",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTenthMigrationContainsKnowledgeScope(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0010_knowledge_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"knowledge_documents ADD COLUMN IF NOT EXISTS scope",
		"knowledge_chunks ADD COLUMN IF NOT EXISTS scope",
		"scope IN ('customer','shared')",
		"knowledge_documents_scope_idx",
		"knowledge_chunks_scope_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestEleventhMigrationContainsOutboundDeliveryStatuses(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0011_outbound_delivery_statuses.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"outbound_intents_status_check",
		"broadcast_targets_status_check",
		"broadcasts_status_check",
		"sent_to_nexus",
		"delivered",
		"UPDATE outbound_intents SET status='sent_to_nexus' WHERE status='sent'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTwelfthMigrationContainsAgentProfileAndSessionMonitor(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0012_agent_profiles_session_monitor.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"agent_instance_versions ADD COLUMN IF NOT EXISTS profile_config",
		"sessions ADD COLUMN IF NOT EXISTS monitor_lease_owner",
		"sessions ADD COLUMN IF NOT EXISTS monitor_lease_expires_at",
		"sessions ADD COLUMN IF NOT EXISTS last_monitored_at",
		"sessions ADD COLUMN IF NOT EXISTS active_pattern",
		"CREATE INDEX IF NOT EXISTS sessions_monitor_claim_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestThirteenthMigrationContainsSchedulerJobScope(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0013_scheduler_job_scope.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS user_id",
		"ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS agent_instance_id",
		"ALTER TABLE scheduler_jobs ADD COLUMN IF NOT EXISTS session_id",
		"payload->>'user_id'",
		"scheduler_jobs_customer_user_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestFourteenthMigrationContainsRecommendations(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0014_recommendations.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS recommendation_items",
		"CREATE TABLE IF NOT EXISTS recommendation_decisions",
		"CREATE TABLE IF NOT EXISTS recommendation_jobs",
		"recommendation_jobs_claim_idx",
		"recommendation_items_customer_status_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestEighteenthMigrationContainsSharedScheduler(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/0018_shared_scheduler.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS shared_scheduler_jobs",
		"CREATE TABLE IF NOT EXISTS shared_scheduler_subscriptions",
		"CREATE TABLE IF NOT EXISTS shared_scheduler_fires",
		"CREATE TABLE IF NOT EXISTS shared_scheduler_deliveries",
		"shared_scheduler_jobs_claim_idx",
		"shared_scheduler_deliveries_run_idx",
		"CHECK (fanout_action IN ('outbound_intent','durable_run'))",
		"UNIQUE (shared_job_id, scheduled_fire_at, user_id, action)",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}
