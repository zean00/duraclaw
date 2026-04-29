package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func agentVersionRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"id", "customer_id", "agent_instance_id", "version", "name", "model_config", "system_instructions", "tool_config", "mcp_config", "workflow_config", "policy_config", "profile_config", "metadata", "created_at", "activated_at"})
}

func TestStoreAgentInstanceVersionMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO agent_instances").WithArgs("c1", "a1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("SELECT COALESCE").WithArgs("c1", "a1").WillReturnRows(pgxmock.NewRows([]string{"version"}).AddRow(2))
	mock.ExpectQuery("INSERT INTO agent_instance_versions").
		WithArgs("c1", "a1", 2, "v2", []byte(`{"model":"gpt"}`), "system", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), true).
		WillReturnRows(agentVersionRows().AddRow("ver-2", "c1", "a1", 2, "v2", []byte(`{"model":"gpt"}`), "system", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), now, &now))
	mock.ExpectExec("UPDATE agent_instances").WithArgs("c1", "a1", "ver-2").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	created, err := store.CreateAgentInstanceVersion(ctx, AgentInstanceVersionSpec{
		CustomerID: "c1", AgentInstanceID: "a1", Name: "v2", ModelConfig: map[string]any{"model": "gpt"}, SystemInstructions: "system", ActivateImmediately: true,
	})
	if err != nil || created.ID != "ver-2" || created.Version != 2 {
		t.Fatalf("created=%#v err=%v", created, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "a1", 100).
		WillReturnRows(agentVersionRows().AddRow("ver-2", "c1", "a1", 2, "v2", []byte(`{}`), "system", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), now, &now))
	versions, err := store.ListAgentInstanceVersions(ctx, "c1", "a1", 0)
	if err != nil || len(versions) != 1 || versions[0].ID != "ver-2" {
		t.Fatalf("versions=%#v err=%v", versions, err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("UPDATE agent_instance_versions").
		WithArgs("ver-2", "c1", "a1").
		WillReturnRows(agentVersionRows().AddRow("ver-2", "c1", "a1", 2, "v2", []byte(`{}`), "system", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), now, &now))
	mock.ExpectExec("UPDATE agent_instances").WithArgs("c1", "a1", "ver-2").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	activated, err := store.ActivateAgentInstanceVersion(ctx, "c1", "a1", "ver-2")
	if err != nil || activated.ID != "ver-2" {
		t.Fatalf("activated=%#v err=%v", activated, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("ver-2").
		WillReturnRows(agentVersionRows().AddRow("ver-2", "c1", "a1", 2, "v2", []byte(`{}`), "system", []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), []byte(`{}`), now, &now))
	got, err := store.AgentInstanceVersion(ctx, "ver-2")
	if err != nil || got.ID != "ver-2" {
		t.Fatalf("version=%#v err=%v", got, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("missing").WillReturnError(pgx.ErrNoRows)
	missing, err := store.AgentInstanceVersion(ctx, "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
	if empty, err := store.AgentInstanceVersion(ctx, ""); err != nil || empty != nil {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}

	mock.ExpectQuery("SELECT COALESCE").WithArgs("c1", "a1").WillReturnRows(pgxmock.NewRows([]string{"version_id"}).AddRow("ver-2"))
	versionID, err := store.currentAgentInstanceVersionID(ctx, "c1", "a1")
	if err != nil || versionID != "ver-2" {
		t.Fatalf("versionID=%q err=%v", versionID, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
