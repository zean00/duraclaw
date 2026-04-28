package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var workflowRunStates = map[string]bool{
	"queued": true, "running": true, "awaiting_user": true, "succeeded": true, "failed": true, "cancelled": true, "expired": true,
}

func ValidWorkflowRunState(state string) bool { return workflowRunStates[state] }

type WorkflowDefinition struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      int             `json:"version"`
	Status       string          `json:"status"`
	Description  string          `json:"description"`
	WhenToUse    string          `json:"when_to_use"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	OwnerScope   string          `json:"owner_scope"`
}

type WorkflowRun struct {
	ID                   string          `json:"id"`
	RunID                string          `json:"run_id"`
	WorkflowDefinitionID string          `json:"workflow_definition_id"`
	WorkflowVersion      int             `json:"workflow_version"`
	Status               string          `json:"status"`
	CurrentNodeKey       string          `json:"current_node_key"`
	Input                json.RawMessage `json:"input"`
	Output               json.RawMessage `json:"output"`
	Error                *string         `json:"error,omitempty"`
}

type WorkflowManifest struct {
	ID           string          `json:"workflow_id"`
	Name         string          `json:"name"`
	Version      int             `json:"version"`
	Description  string          `json:"description"`
	WhenToUse    string          `json:"when_to_use"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

type WorkflowNode struct {
	WorkflowDefinitionID string          `json:"workflow_definition_id"`
	NodeKey              string          `json:"node_key"`
	NodeType             string          `json:"node_type"`
	Config               json.RawMessage `json:"config"`
	RetryPolicy          json.RawMessage `json:"retry_policy"`
	TimeoutPolicy        json.RawMessage `json:"timeout_policy"`
}

type WorkflowEdge struct {
	WorkflowDefinitionID string          `json:"workflow_definition_id"`
	FromNodeKey          string          `json:"from_node_key"`
	ToNodeKey            string          `json:"to_node_key"`
	Condition            json.RawMessage `json:"condition"`
}

type WorkflowNodeState struct {
	WorkflowRunID string          `json:"workflow_run_id"`
	NodeKey       string          `json:"node_key"`
	Status        string          `json:"status"`
	Output        json.RawMessage `json:"output"`
	Error         *string         `json:"error,omitempty"`
	Attempts      int             `json:"attempts"`
}

type WorkflowEdgeActivation struct {
	WorkflowRunID string `json:"workflow_run_id"`
	FromNodeKey   string `json:"from_node_key"`
	ToNodeKey     string `json:"to_node_key"`
	Active        bool   `json:"active"`
}

func (s *Store) CreateWorkflowDefinition(ctx context.Context, name string, version int, description, whenToUse string, inputSchema, outputSchema any) (string, error) {
	inputJSON, _ := json.Marshal(inputSchema)
	outputJSON, _ := json.Marshal(outputSchema)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflow_definitions(name,version,status,description,when_to_use,input_schema,output_schema)
		VALUES($1,$2,'active',$3,$4,$5,$6)
		ON CONFLICT (name, version) DO UPDATE SET description=EXCLUDED.description, when_to_use=EXCLUDED.when_to_use
		RETURNING id::text`, name, version, description, whenToUse, inputJSON, outputJSON).Scan(&id)
	return id, err
}

func (s *Store) ListWorkflowDefinitions(ctx context.Context, limit int) ([]WorkflowDefinition, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, version, status, description, when_to_use, input_schema, output_schema, owner_scope
		FROM workflow_definitions
		ORDER BY name, version DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowDefinition
	for rows.Next() {
		var def WorkflowDefinition
		if err := rows.Scan(&def.ID, &def.Name, &def.Version, &def.Status, &def.Description, &def.WhenToUse, &def.InputSchema, &def.OutputSchema, &def.OwnerScope); err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, rows.Err()
}

func (s *Store) WorkflowDefinition(ctx context.Context, workflowDefinitionID string) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, name, version, status, description, when_to_use, input_schema, output_schema, owner_scope
		FROM workflow_definitions
		WHERE id=$1`, workflowDefinitionID).
		Scan(&def.ID, &def.Name, &def.Version, &def.Status, &def.Description, &def.WhenToUse, &def.InputSchema, &def.OutputSchema, &def.OwnerScope)
	if err != nil {
		return nil, err
	}
	return &def, nil
}

func (s *Store) AssignWorkflow(ctx context.Context, workflowDefinitionID, customerID, agentInstanceID string, enabled bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_assignments(workflow_definition_id,customer_id,agent_instance_id,enabled)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (workflow_definition_id, customer_id, agent_instance_id)
		DO UPDATE SET enabled=EXCLUDED.enabled`,
		workflowDefinitionID, customerID, agentInstanceID, enabled)
	return err
}

func (s *Store) UpsertWorkflowNode(ctx context.Context, workflowDefinitionID, nodeKey, nodeType string, config, retryPolicy, timeoutPolicy any) error {
	configJSON, _ := json.Marshal(config)
	retryJSON, _ := json.Marshal(retryPolicy)
	timeoutJSON, _ := json.Marshal(timeoutPolicy)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_nodes(workflow_definition_id,node_key,node_type,config,retry_policy,timeout_policy)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workflow_definition_id, node_key)
		DO UPDATE SET node_type=EXCLUDED.node_type, config=EXCLUDED.config, retry_policy=EXCLUDED.retry_policy, timeout_policy=EXCLUDED.timeout_policy`,
		workflowDefinitionID, nodeKey, nodeType, configJSON, retryJSON, timeoutJSON)
	return err
}

func (s *Store) UpsertWorkflowEdge(ctx context.Context, workflowDefinitionID, fromNodeKey, toNodeKey string, condition any) error {
	conditionJSON, _ := json.Marshal(condition)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_edges(workflow_definition_id,from_node_key,to_node_key,condition)
		VALUES($1,$2,$3,$4)
		ON CONFLICT (workflow_definition_id, from_node_key, to_node_key)
		DO UPDATE SET condition=EXCLUDED.condition`,
		workflowDefinitionID, fromNodeKey, toNodeKey, conditionJSON)
	return err
}

func (s *Store) WorkflowNodes(ctx context.Context, workflowDefinitionID string) ([]WorkflowNode, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT workflow_definition_id::text, node_key, node_type, config, retry_policy, timeout_policy
		FROM workflow_nodes
		WHERE workflow_definition_id=$1
		ORDER BY node_key`, workflowDefinitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowNode
	for rows.Next() {
		var node WorkflowNode
		if err := rows.Scan(&node.WorkflowDefinitionID, &node.NodeKey, &node.NodeType, &node.Config, &node.RetryPolicy, &node.TimeoutPolicy); err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *Store) WorkflowEdges(ctx context.Context, workflowDefinitionID string) ([]WorkflowEdge, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT workflow_definition_id::text, from_node_key, to_node_key, condition
		FROM workflow_edges
		WHERE workflow_definition_id=$1
		ORDER BY from_node_key, to_node_key`, workflowDefinitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowEdge
	for rows.Next() {
		var edge WorkflowEdge
		if err := rows.Scan(&edge.WorkflowDefinitionID, &edge.FromNodeKey, &edge.ToNodeKey, &edge.Condition); err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

func (s *Store) WorkflowManifests(ctx context.Context, customerID, agentInstanceID string) ([]WorkflowManifest, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id::text, d.name, d.version, d.description, d.when_to_use, d.input_schema, d.output_schema
		FROM workflow_definitions d
		JOIN workflow_assignments a ON a.workflow_definition_id=d.id
		WHERE a.customer_id=$1
		AND a.agent_instance_id=$2
		AND a.enabled=true
		AND d.status='active'
		ORDER BY d.name, d.version DESC`, customerID, agentInstanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowManifest
	for rows.Next() {
		var m WorkflowManifest
		if err := rows.Scan(&m.ID, &m.Name, &m.Version, &m.Description, &m.WhenToUse, &m.InputSchema, &m.OutputSchema); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) StartWorkflowRun(ctx context.Context, runID, workflowDefinitionID string, version int, currentNode string, input any) (string, error) {
	b, _ := json.Marshal(input)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflow_runs(run_id,workflow_definition_id,workflow_version,status,current_node_key,input)
		VALUES($1,$2,$3,'running',$4,$5)
		RETURNING id::text`, runID, workflowDefinitionID, version, currentNode, b).Scan(&id)
	if err == nil {
		_ = s.SetRunState(ctx, runID, "running_workflow", nil)
		_ = s.AddEvent(ctx, runID, "workflow.running", map[string]any{"workflow_run_id": id})
	}
	return id, err
}

func (s *Store) CurrentWorkflowRun(ctx context.Context, runID, workflowDefinitionID string) (*WorkflowRun, error) {
	var run WorkflowRun
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, run_id::text, workflow_definition_id::text, workflow_version, status, current_node_key, input, output, error
		FROM workflow_runs
		WHERE run_id=$1
		AND workflow_definition_id=$2
		AND status IN ('running','awaiting_user')
		ORDER BY created_at DESC
		LIMIT 1`, runID, workflowDefinitionID).
		Scan(&run.ID, &run.RunID, &run.WorkflowDefinitionID, &run.WorkflowVersion, &run.Status, &run.CurrentNodeKey, &run.Input, &run.Output, &run.Error)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *Store) SetWorkflowRunState(ctx context.Context, workflowRunID, runID, state, currentNode string, output any, errText *string) error {
	if !ValidWorkflowRunState(state) {
		return fmt.Errorf("invalid workflow run state %q", state)
	}
	completed := ""
	if state == "succeeded" || state == "failed" || state == "cancelled" || state == "expired" {
		completed = ", completed_at=now()"
	}
	b, _ := json.Marshal(output)
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow_runs
		SET status=$2, current_node_key=$3, output=$4, error=$5`+completed+`
		WHERE id=$1`, workflowRunID, state, currentNode, b, errText)
	if err == nil {
		_ = s.AddEvent(ctx, runID, "workflow."+state, map[string]any{"workflow_run_id": workflowRunID, "current_node_key": currentNode})
	}
	return err
}

func (s *Store) InitializeWorkflowNodeStates(ctx context.Context, workflowRunID string, nodes []WorkflowNode) error {
	for _, node := range nodes {
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO workflow_node_states(workflow_run_id,node_key,status)
			VALUES($1,$2,'pending')
			ON CONFLICT (workflow_run_id, node_key) DO NOTHING`, workflowRunID, node.NodeKey); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) WorkflowNodeStates(ctx context.Context, workflowRunID string) ([]WorkflowNodeState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT workflow_run_id::text, node_key, status, output, error, attempts
		FROM workflow_node_states
		WHERE workflow_run_id=$1`, workflowRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []WorkflowNodeState
	for rows.Next() {
		var state WorkflowNodeState
		if err := rows.Scan(&state.WorkflowRunID, &state.NodeKey, &state.Status, &state.Output, &state.Error, &state.Attempts); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

func (s *Store) SetWorkflowNodeState(ctx context.Context, workflowRunID, nodeKey, status string, output any, errText *string) error {
	b, _ := json.Marshal(output)
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow_node_states
		SET status=$3, output=$4, error=$5, attempts=attempts+CASE WHEN $3='running' THEN 1 ELSE 0 END, updated_at=now()
		WHERE workflow_run_id=$1 AND node_key=$2`, workflowRunID, nodeKey, status, b, errText)
	return err
}

func (s *Store) ActivateWorkflowEdge(ctx context.Context, workflowRunID, fromNodeKey, toNodeKey string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_edge_activations(workflow_run_id,from_node_key,to_node_key,active)
		VALUES($1,$2,$3,true)
		ON CONFLICT (workflow_run_id, from_node_key, to_node_key)
		DO UPDATE SET active=true`, workflowRunID, fromNodeKey, toNodeKey)
	return err
}

func (s *Store) WorkflowEdgeActivations(ctx context.Context, workflowRunID string) ([]WorkflowEdgeActivation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT workflow_run_id::text, from_node_key, to_node_key, active
		FROM workflow_edge_activations
		WHERE workflow_run_id=$1`, workflowRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowEdgeActivation
	for rows.Next() {
		var activation WorkflowEdgeActivation
		if err := rows.Scan(&activation.WorkflowRunID, &activation.FromNodeKey, &activation.ToNodeKey, &activation.Active); err != nil {
			return nil, err
		}
		out = append(out, activation)
	}
	return out, rows.Err()
}

func (s *Store) StartWorkflowNodeRun(ctx context.Context, workflowRunID, nodeKey string, input any) (string, error) {
	b, _ := json.Marshal(input)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO workflow_node_runs(workflow_run_id,node_key,status,attempts,input_ref,started_at)
		VALUES($1,$2,'running',1,$3,now())
		RETURNING id::text`, workflowRunID, nodeKey, b).Scan(&id)
	return id, err
}

func (s *Store) CompleteWorkflowNodeRun(ctx context.Context, nodeRunID, state string, output any, errText *string) error {
	if !ValidWorkflowRunState(state) {
		return fmt.Errorf("invalid workflow node run state %q", state)
	}
	completed := ""
	if state != "awaiting_user" {
		completed = ", completed_at=now()"
	}
	b, _ := json.Marshal(output)
	_, err := s.pool.Exec(ctx, `
		UPDATE workflow_node_runs
		SET status=$2, output_ref=$3, error=$4`+completed+`
		WHERE id=$1`, nodeRunID, state, b, errText)
	return err
}
