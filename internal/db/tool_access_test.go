package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func toolAccessRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "user_id", "allowed_tools", "denied_tools", "metadata", "updated_at"})
}

func TestToolAllowed(t *testing.T) {
	if !ToolAllowed(EffectiveToolAccess{}, "remember") {
		t.Fatal("missing rule should allow non-empty tool")
	}
	if ToolAllowed(EffectiveToolAccess{AllowedTools: []string{"remember"}}, "echo") {
		t.Fatal("allow list should deny tools not listed")
	}
	if ToolAllowed(EffectiveToolAccess{AllowedTools: []string{"remember"}, DeniedTools: []string{"remember"}}, "remember") {
		t.Fatal("deny list should win over allow list")
	}
	if ToolAllowed(EffectiveToolAccess{}, " ") {
		t.Fatal("empty tool names should be denied")
	}
}

func TestStoreToolAccessRuleEffectiveAndCheck(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO agent_instances").WithArgs("c1", "a1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO users").WithArgs("c1", "u1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO tool_access_rules").
		WithArgs("c1", "a1", "u1", []byte(`["remember"]`), []byte(`["echo"]`), []byte(`{}`)).
		WillReturnRows(toolAccessRows().AddRow("c1", "a1", "u1", []byte(`["remember"]`), []byte(`["echo"]`), []byte(`{}`), now))
	rule, err := store.UpsertToolAccessRule(ctx, ToolAccessRule{
		CustomerID: "c1", AgentInstanceID: "a1", UserID: "u1",
		AllowedTools: []string{" remember ", "remember"}, DeniedTools: []string{"echo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.AllowedTools) != 1 || rule.AllowedTools[0] != "remember" {
		t.Fatalf("rule=%#v", rule)
	}

	mock.ExpectQuery("FROM tool_access_rules").
		WithArgs("c1", "a1", "u1").
		WillReturnRows(toolAccessRows().AddRow("c1", "a1", "u1", []byte(`["remember"]`), []byte(`["echo"]`), []byte(`{}`), now))
	access, err := store.EffectiveToolAccess(ctx, "c1", "a1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if access.Source != "user" || !ToolAllowed(access, "remember") || ToolAllowed(access, "echo") {
		t.Fatalf("access=%#v", access)
	}
	if err := store.CheckToolAccess(ctx, "c1", "a1", "u1", "echo"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected access denial, got %v", err)
	}

	mock.ExpectExec("DELETE FROM tool_access_rules").WithArgs("c1", "a1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeleteToolAccessRule(ctx, "c1", "a1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCheckToolAccessRequiresContext(t *testing.T) {
	store, mock := newMockStore(t)
	err := store.CheckToolAccess(context.Background(), "", "a1", "u1", "remember")
	if err == nil || !strings.Contains(err.Error(), "customer_id") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
