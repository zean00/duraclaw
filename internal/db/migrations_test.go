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
