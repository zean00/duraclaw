package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreWorkflowDefinitionMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("INSERT INTO workflow_definitions").WithArgs("triage", 1, "desc", "when", []byte(`{}`), []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("wf-1"))
	id, err := store.CreateWorkflowDefinition(ctx, "triage", 1, "desc", "when", map[string]any{}, map[string]any{})
	if err != nil || id != "wf-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs(100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "description", "when_to_use", "input_schema", "output_schema", "owner_scope"}).
			AddRow("wf-1", "triage", 1, "active", "desc", "when", []byte(`{}`), []byte(`{}`), "customer"))
	defs, err := store.ListWorkflowDefinitions(ctx, 0)
	if err != nil || len(defs) != 1 {
		t.Fatalf("defs=%#v err=%v", defs, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("wf-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "status", "description", "when_to_use", "input_schema", "output_schema", "owner_scope"}).
			AddRow("wf-1", "triage", 1, "active", "desc", "when", []byte(`{}`), []byte(`{}`), "customer"))
	def, err := store.WorkflowDefinition(ctx, "wf-1")
	if err != nil || def.ID != "wf-1" {
		t.Fatalf("def=%#v err=%v", def, err)
	}
	mock.ExpectExec("INSERT INTO workflow_assignments").WithArgs("wf-1", "c1", "a1", true).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.AssignWorkflow(ctx, "wf-1", "c1", "a1", true); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("INSERT INTO workflow_nodes").WithArgs("wf-1", "start", "model", []byte(`{}`), []byte(`null`), []byte(`null`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.UpsertWorkflowNode(ctx, "wf-1", "start", "model", map[string]any{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("INSERT INTO workflow_edges").WithArgs("wf-1", "start", "end", []byte(`{}`)).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.UpsertWorkflowEdge(ctx, "wf-1", "start", "end", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT workflow_definition_id").WithArgs("wf-1").
		WillReturnRows(pgxmock.NewRows([]string{"workflow_definition_id", "node_key", "node_type", "config", "retry_policy", "timeout_policy"}).
			AddRow("wf-1", "start", "model", []byte(`{}`), []byte(`null`), []byte(`null`)))
	nodes, err := store.WorkflowNodes(ctx, "wf-1")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%#v err=%v", nodes, err)
	}
	mock.ExpectQuery("SELECT workflow_definition_id").WithArgs("wf-1").
		WillReturnRows(pgxmock.NewRows([]string{"workflow_definition_id", "from_node_key", "to_node_key", "condition"}).
			AddRow("wf-1", "start", "end", []byte(`{}`)))
	edges, err := store.WorkflowEdges(ctx, "wf-1")
	if err != nil || len(edges) != 1 {
		t.Fatalf("edges=%#v err=%v", edges, err)
	}
	mock.ExpectQuery("SELECT d.id").WithArgs("c1", "a1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "version", "description", "when_to_use", "input_schema", "output_schema"}).
			AddRow("wf-1", "triage", 1, "desc", "when", []byte(`{}`), []byte(`{}`)))
	manifests, err := store.WorkflowManifests(ctx, "c1", "a1")
	if err != nil || len(manifests) != 1 || manifests[0].ID != "wf-1" {
		t.Fatalf("manifests=%#v err=%v", manifests, err)
	}
	_ = now
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreWorkflowRunMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mock.ExpectQuery("SELECT id").WithArgs("run-1", "wf-1").
		WillReturnRows(pgxmock.NewRows([]string{"id", "run_id", "workflow_definition_id", "workflow_version", "status", "current_node_key", "input", "output", "error"}).
			AddRow("wfr-1", "run-1", "wf-1", 1, "running", "start", []byte(`{}`), []byte(`{}`), nil))
	current, err := store.CurrentWorkflowRun(ctx, "run-1", "wf-1")
	if err != nil || current.ID != "wfr-1" {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	mock.ExpectQuery("SELECT id").WithArgs("run-1", "missing").WillReturnError(pgx.ErrNoRows)
	missing, err := store.CurrentWorkflowRun(ctx, "run-1", "missing")
	if err != nil || missing != nil {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}

	mock.ExpectExec("UPDATE workflow_runs").WithArgs("wfr-1", "succeeded", "end", []byte(`{"ok":true}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO run_events").WithArgs("run-1", "workflow.succeeded", pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.SetWorkflowRunState(ctx, "wfr-1", "run-1", "succeeded", "end", map[string]bool{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkflowRunState(ctx, "wfr-1", "run-1", "bogus", "", nil, nil); err == nil {
		t.Fatal("expected invalid state")
	}

	mock.ExpectExec("INSERT INTO workflow_node_states").WithArgs("wfr-1", "start").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.InitializeWorkflowNodeStates(ctx, "wfr-1", []WorkflowNode{{NodeKey: "start"}}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT workflow_run_id").WithArgs("wfr-1").
		WillReturnRows(pgxmock.NewRows([]string{"workflow_run_id", "node_key", "status", "output", "error", "attempts"}).
			AddRow("wfr-1", "start", "pending", []byte(`{}`), nil, 0))
	states, err := store.WorkflowNodeStates(ctx, "wfr-1")
	if err != nil || len(states) != 1 {
		t.Fatalf("states=%#v err=%v", states, err)
	}
	mock.ExpectExec("UPDATE workflow_node_states").WithArgs("wfr-1", "start", "running", []byte(`{}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetWorkflowNodeState(ctx, "wfr-1", "start", "running", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("INSERT INTO workflow_edge_activations").WithArgs("wfr-1", "start", "end").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	if err := store.ActivateWorkflowEdge(ctx, "wfr-1", "start", "end"); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT workflow_run_id").WithArgs("wfr-1").
		WillReturnRows(pgxmock.NewRows([]string{"workflow_run_id", "from_node_key", "to_node_key", "active"}).
			AddRow("wfr-1", "start", "end", true))
	activations, err := store.WorkflowEdgeActivations(ctx, "wfr-1")
	if err != nil || len(activations) != 1 {
		t.Fatalf("activations=%#v err=%v", activations, err)
	}
	mock.ExpectQuery("INSERT INTO workflow_node_runs").WithArgs("wfr-1", "start", []byte(`{"input":true}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("node-run-1"))
	nodeRunID, err := store.StartWorkflowNodeRun(ctx, "wfr-1", "start", map[string]bool{"input": true})
	if err != nil || nodeRunID != "node-run-1" {
		t.Fatalf("nodeRunID=%q err=%v", nodeRunID, err)
	}
	mock.ExpectExec("UPDATE workflow_node_runs").WithArgs("node-run-1", "succeeded", []byte(`{"ok":true}`), pgxmock.AnyArg()).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.CompleteWorkflowNodeRun(ctx, "node-run-1", "succeeded", map[string]bool{"ok": true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteWorkflowNodeRun(ctx, "node-run-1", "bogus", nil, nil); err == nil {
		t.Fatal("expected invalid node state")
	}

	_ = now
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
