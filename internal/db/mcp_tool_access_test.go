package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

func mcpToolAccessRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{"customer_id", "agent_instance_id", "user_id", "server_name", "allowed_tools", "denied_tools", "metadata", "updated_at"})
}

func TestMCPToolAllowed(t *testing.T) {
	if !MCPToolAllowed(EffectiveMCPToolAccess{}, "search") {
		t.Fatal("missing rule should allow non-empty tool")
	}
	if MCPToolAllowed(EffectiveMCPToolAccess{AllowedTools: []string{"search"}}, "delete") {
		t.Fatal("allow list should deny tools not listed")
	}
	if MCPToolAllowed(EffectiveMCPToolAccess{AllowedTools: []string{"search"}, DeniedTools: []string{"search"}}, "search") {
		t.Fatal("deny list should win over allow list")
	}
	if MCPToolAllowed(EffectiveMCPToolAccess{}, " ") {
		t.Fatal("empty tool names should be denied")
	}
}

func TestStoreMCPToolAccessRuleEffectiveAndCheck(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO agent_instances").WithArgs("c1", "a1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO users").WithArgs("c1", "u1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO mcp_tool_access_rules").
		WithArgs("c1", "a1", "u1", "srv", []byte(`["search"]`), []byte(`["delete"]`), []byte(`{}`)).
		WillReturnRows(mcpToolAccessRows().AddRow("c1", "a1", "u1", "srv", []byte(`["search"]`), []byte(`["delete"]`), []byte(`{}`), now))
	rule, err := store.UpsertMCPToolAccessRule(ctx, MCPToolAccessRule{
		CustomerID: "c1", AgentInstanceID: "a1", UserID: "u1", ServerName: "srv",
		AllowedTools: []string{" search ", "search"}, DeniedTools: []string{"delete"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rule.AllowedTools) != 1 || rule.AllowedTools[0] != "search" {
		t.Fatalf("rule=%#v", rule)
	}

	mock.ExpectQuery("FROM mcp_tool_access_rules").
		WithArgs("c1", "a1", "u1", "srv").
		WillReturnRows(mcpToolAccessRows().AddRow("c1", "a1", "u1", "srv", []byte(`["search"]`), []byte(`["delete"]`), []byte(`{}`), now))
	access, err := store.EffectiveMCPToolAccess(ctx, "c1", "a1", "u1", "srv")
	if err != nil {
		t.Fatal(err)
	}
	if access.Source != "user" || !MCPToolAllowed(access, "search") || MCPToolAllowed(access, "delete") {
		t.Fatalf("access=%#v", access)
	}
	if err := store.CheckMCPToolAccess(ctx, "c1", "a1", "u1", "srv", "delete"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected access denial, got %v", err)
	}

	mock.ExpectExec("DELETE FROM mcp_tool_access_rules").WithArgs("c1", "a1", "u1", "srv").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeleteMCPToolAccessRule(ctx, "c1", "a1", "u1", "srv"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCheckMCPToolAccessRequiresContext(t *testing.T) {
	store, mock := newMockStore(t)
	err := store.CheckMCPToolAccess(context.Background(), "", "a1", "u1", "srv", "search")
	if err == nil || !strings.Contains(err.Error(), "customer_id") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreMCPToolAccessRuleRejectsMalformedToolLists(t *testing.T) {
	store, mock := newMockStore(t)
	now := time.Now().UTC()
	mock.ExpectQuery("FROM mcp_tool_access_rules").
		WithArgs("c1", "a1", "u1", "srv").
		WillReturnRows(mcpToolAccessRows().AddRow("c1", "a1", "u1", "srv", []byte(`{"bad":true}`), []byte(`[]`), []byte(`{}`), now))
	_, err := store.MCPToolAccessRule(context.Background(), " c1 ", " a1 ", " u1 ", " srv ")
	if err == nil || !strings.Contains(err.Error(), "allowed tools") {
		t.Fatalf("expected decode error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
