package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreRunTraceWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	runID := "run-1"
	selectedItemID := "item-1"

	mock.ExpectQuery("FROM run_steps").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "kind", "state", "input", "output", "error", "started_at", "completed_at", "created_at"}).
			AddRow("step-1", "run-1", "model", "succeeded", []byte(`{}`), []byte(`{}`), nil, &now, &now, now))
	mock.ExpectQuery("FROM model_calls").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "provider", "model", "state", "request_summary", "response_summary", "error", "created_at", "completed_at"}).
			AddRow("model-1", "openai", "gpt", "succeeded", []byte(`{}`), []byte(`{}`), nil, now, &now))
	mock.ExpectQuery("FROM tool_calls").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "tool_name", "state", "arguments", "result", "retryable", "args_hash", "error"}).
			AddRow("tool-1", "run-1", "lookup", "succeeded", []byte(`{}`), []byte(`{}`), false, "hash", nil))
	mock.ExpectQuery("FROM processor_calls").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "artifact_id", "processor", "state", "request_summary", "response_summary", "error", "created_at", "completed_at"}).
			AddRow("proc-1", "art-1", "ocr", "succeeded", []byte(`{}`), []byte(`{}`), nil, now, &now))
	mock.ExpectQuery("FROM mcp_calls").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "server_name", "tool_name", "state", "request_summary", "response_summary", "error", "created_at", "completed_at"}).
			AddRow("mcp-1", "srv", "tool", "succeeded", []byte(`{}`), []byte(`{}`), nil, now, &now))
	mock.ExpectQuery("FROM tool_evaluations").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "customer_id", "user_id", "agent_instance_id", "session_id", "status", "category", "confidence", "expected_tools", "actual_tools", "reason", "repair_action", "repair_status", "finding", "suspicious_signals", "lease_owner", "lease_expires_at", "completed_at", "created_at", "updated_at"}))
	mock.ExpectQuery("FROM run_events").WithArgs("run-1", int64(0), 1000).
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "event_type", "payload", "created_at"}).
			AddRow(int64(1), "run-1", "scope.judged", []byte(`{"in_scope":true}`), now))
	mock.ExpectQuery("FROM recommendation_decisions").WithArgs("run-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "run_id", "user_id", "session_id", "scope_intent", "context_mode", "candidate_item_ids", "selected_item_id", "recommendation_text", "reason", "delivery_status", "error", "metadata", "created_at"}).
			AddRow("rec-1", "c1", &runID, "u1", "s1", "direct", "current", []byte(`["item-1"]`), &selectedItemID, "Recommended", "helpful", "inline_merged", "", []byte(`{}`), now))

	trace, err := store.RunTrace(ctx, "run-1")
	if err != nil || len(trace.Steps) != 1 || len(trace.ModelCalls) != 1 || len(trace.ToolCalls) != 1 || len(trace.ProcessorCalls) != 1 || len(trace.MCPCalls) != 1 || len(trace.Events) != 1 || len(trace.RecommendationDecisions) != 1 {
		t.Fatalf("trace=%#v err=%v", trace, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreUserMetadataWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := store.User(ctx, "", "u1"); err == nil {
		t.Fatal("expected missing user scope")
	}
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "u1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "id", "metadata", "created_at"}).AddRow("c1", "u1", []byte(`{"tier":"gold"}`), now))
	user, err := store.User(ctx, "c1", "u1")
	if err != nil || user.ID != "u1" {
		t.Fatalf("user=%#v err=%v", user, err)
	}
	mock.ExpectQuery("SELECT customer_id").WithArgs("c1", "u1").
		WillReturnRows(pgxmock.NewRows([]string{"customer_id", "id", "metadata", "created_at"}).AddRow("c1", "u1", []byte(`{"tier":"gold"}`), now))
	metadata, err := store.UserMetadata(ctx, "c1", "u1")
	if err != nil || metadata["tier"] != "gold" {
		t.Fatalf("metadata=%#v err=%v", metadata, err)
	}
	mock.ExpectExec("UPDATE users").WithArgs("c1", "u1", []byte(`{"tier":"silver"}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.UpdateUserMetadata(ctx, "c1", "u1", map[string]string{"tier": "silver"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE users").WithArgs("c1", "missing", []byte(`{}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := store.UpdateUserMetadata(ctx, "c1", "missing", nil); err == nil {
		t.Fatal("expected user not found")
	}
	mock.ExpectExec("UPDATE users").WithArgs("c1", "u1", []byte(`{"tier":"bronze"}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.MergeUserMetadata(ctx, "c1", "u1", map[string]string{"tier": "bronze"}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WithArgs("c1", "u1", []byte(`{"recommendation":{"blocked_channels":["whatsapp"]}}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE sessions").WithArgs("c1", "u1", []byte(`{"recommendation":{"blocked_channels":["whatsapp"]}}`), "u1").WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()
	mock.ExpectRollback()
	if err := store.UpsertUserRecommendationPolicy(ctx, "c1", "u1", SessionRecommendationPolicy{BlockedChannels: []string{"whatsapp", "whatsapp"}}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertUserRecommendationPolicyRollsBackWhenSessionUpdateFails(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WithArgs("c1", "u1", []byte(`{"recommendation":{"blocked_channels":["whatsapp"]}}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE sessions").WithArgs("c1", "u1", []byte(`{"recommendation":{"blocked_channels":["whatsapp"]}}`), "u1").WillReturnError(errors.New("session update failed"))
	mock.ExpectRollback()
	if err := store.UpsertUserRecommendationPolicy(ctx, "c1", "u1", SessionRecommendationPolicy{BlockedChannels: []string{"whatsapp"}}); err == nil {
		t.Fatal("expected session update error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
