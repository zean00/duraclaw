package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/providers"
	"duraclaw/internal/tools"
)

const defaultConcurrency = 4
const maxNodeExecutions = 100

type Store interface {
	WorkflowDefinition(ctx context.Context, workflowDefinitionID string) (*db.WorkflowDefinition, error)
	WorkflowNodes(ctx context.Context, workflowDefinitionID string) ([]db.WorkflowNode, error)
	WorkflowEdges(ctx context.Context, workflowDefinitionID string) ([]db.WorkflowEdge, error)
	CurrentWorkflowRun(ctx context.Context, runID, workflowDefinitionID string) (*db.WorkflowRun, error)
	StartWorkflowRun(ctx context.Context, runID, workflowDefinitionID string, version int, currentNode string, input any) (string, error)
	SetWorkflowRunState(ctx context.Context, workflowRunID, runID, state, currentNode string, output any, errText *string) error
	InitializeWorkflowNodeStates(ctx context.Context, workflowRunID string, nodes []db.WorkflowNode) error
	WorkflowNodeStates(ctx context.Context, workflowRunID string) ([]db.WorkflowNodeState, error)
	SetWorkflowNodeState(ctx context.Context, workflowRunID, nodeKey, status string, output any, errText *string) error
	ActivateWorkflowEdge(ctx context.Context, workflowRunID, fromNodeKey, toNodeKey string) error
	WorkflowEdgeActivations(ctx context.Context, workflowRunID string) ([]db.WorkflowEdgeActivation, error)
	StartWorkflowNodeRun(ctx context.Context, workflowRunID, nodeKey string, input any) (string, error)
	CompleteWorkflowNodeRun(ctx context.Context, nodeRunID, state string, output any, errText *string) error
	StartModelCall(ctx context.Context, runID, provider, model string, request any) (string, error)
	CompleteModelCall(ctx context.Context, callID, runID string, response any, errText *string) error
	StartToolCall(ctx context.Context, runID, toolName string, args any, retryable bool) (string, error)
	CompleteToolCall(ctx context.Context, callID, runID string, result any, errText *string) error
	StartMCPCall(ctx context.Context, runID, serverName, toolName string, request any) (string, error)
	CompleteMCPCall(ctx context.Context, callID, runID string, response any, errText *string) error
	SetRunState(ctx context.Context, runID, state string, errText *string) error
	AddEvent(ctx context.Context, runID, typ string, payload any) error
}

type Executor struct {
	store       Store
	providers   *providers.Registry
	modelConfig providers.ModelConfig
	tools       *tools.Registry
	mcpManager  *mcp.Manager
	concurrency int
}

func NewExecutor(store Store) *Executor {
	return &Executor{store: store, concurrency: defaultConcurrency}
}

func (e *Executor) WithProviders(registry *providers.Registry, cfg providers.ModelConfig) *Executor {
	e.providers = registry
	e.modelConfig = cfg
	return e
}

func (e *Executor) WithTools(registry *tools.Registry) *Executor {
	e.tools = registry
	return e
}

func (e *Executor) WithMCP(manager *mcp.Manager) *Executor {
	e.mcpManager = manager
	return e
}

type GraphRequest struct {
	RunID                string         `json:"run_id"`
	CustomerID           string         `json:"customer_id,omitempty"`
	UserID               string         `json:"user_id,omitempty"`
	AgentInstanceID      string         `json:"agent_instance_id,omitempty"`
	SessionID            string         `json:"session_id,omitempty"`
	RequestID            string         `json:"request_id,omitempty"`
	WorkflowDefinitionID string         `json:"workflow_definition_id"`
	Input                map[string]any `json:"input,omitempty"`
}

type GraphResult struct {
	WorkflowRunID string         `json:"workflow_run_id"`
	State         string         `json:"state"`
	CurrentNode   string         `json:"current_node"`
	Output        map[string]any `json:"output,omitempty"`
}

type graphData struct {
	def       *db.WorkflowDefinition
	nodes     []db.WorkflowNode
	edges     []db.WorkflowEdge
	nodeByKey map[string]db.WorkflowNode
	incoming  map[string][]db.WorkflowEdge
	outgoing  map[string][]db.WorkflowEdge
}

func (e *Executor) ExecuteGraph(ctx context.Context, req GraphRequest) (*GraphResult, error) {
	if e.store == nil {
		return nil, fmt.Errorf("workflow executor store is nil")
	}
	if req.RunID == "" || req.WorkflowDefinitionID == "" {
		return nil, fmt.Errorf("run_id and workflow_definition_id are required")
	}
	graph, err := e.loadGraph(ctx, req.WorkflowDefinitionID)
	if err != nil {
		return nil, err
	}
	start, err := validateGraph(graph)
	if err != nil {
		return nil, err
	}
	workflowRunID, err := e.ensureWorkflowRun(ctx, req, graph, start)
	if err != nil {
		return nil, err
	}
	if err := e.store.InitializeWorkflowNodeStates(ctx, workflowRunID, graph.nodes); err != nil {
		return nil, err
	}
	if err := e.queueStartIfFresh(ctx, workflowRunID, start); err != nil {
		return nil, err
	}
	return e.runReadyQueue(ctx, req, graph, workflowRunID)
}

func (e *Executor) loadGraph(ctx context.Context, workflowDefinitionID string) (*graphData, error) {
	def, err := e.store.WorkflowDefinition(ctx, workflowDefinitionID)
	if err != nil {
		return nil, err
	}
	if def.Status != "active" {
		return nil, fmt.Errorf("workflow definition %s is %s", def.ID, def.Status)
	}
	nodes, err := e.store.WorkflowNodes(ctx, def.ID)
	if err != nil {
		return nil, err
	}
	edges, err := e.store.WorkflowEdges(ctx, def.ID)
	if err != nil {
		return nil, err
	}
	g := &graphData{
		def:       def,
		nodes:     nodes,
		edges:     edges,
		nodeByKey: map[string]db.WorkflowNode{},
		incoming:  map[string][]db.WorkflowEdge{},
		outgoing:  map[string][]db.WorkflowEdge{},
	}
	for _, node := range nodes {
		g.nodeByKey[node.NodeKey] = node
	}
	for _, edge := range edges {
		g.incoming[edge.ToNodeKey] = append(g.incoming[edge.ToNodeKey], edge)
		g.outgoing[edge.FromNodeKey] = append(g.outgoing[edge.FromNodeKey], edge)
	}
	for key := range g.incoming {
		sort.Slice(g.incoming[key], func(i, j int) bool { return g.incoming[key][i].FromNodeKey < g.incoming[key][j].FromNodeKey })
	}
	for key := range g.outgoing {
		sort.Slice(g.outgoing[key], func(i, j int) bool { return g.outgoing[key][i].ToNodeKey < g.outgoing[key][j].ToNodeKey })
	}
	return g, nil
}

func validateGraph(g *graphData) (string, error) {
	if len(g.nodes) == 0 {
		return "", fmt.Errorf("workflow definition has no nodes")
	}
	var start string
	for _, node := range g.nodes {
		if !supportedNodeType(node.NodeType) {
			return "", fmt.Errorf("unsupported workflow node_type %q", node.NodeType)
		}
		if node.NodeType == "start" {
			if start != "" {
				return "", fmt.Errorf("workflow graph has multiple start nodes")
			}
			start = node.NodeKey
		}
	}
	if start == "" {
		start = firstRootNode(g.nodes, g.edges)
	}
	if start == "" {
		return "", fmt.Errorf("workflow graph has no starting node")
	}
	for _, edge := range g.edges {
		if _, ok := g.nodeByKey[edge.FromNodeKey]; !ok {
			return "", fmt.Errorf("workflow edge starts from missing node %q", edge.FromNodeKey)
		}
		if _, ok := g.nodeByKey[edge.ToNodeKey]; !ok {
			return "", fmt.Errorf("workflow edge points to missing node %q", edge.ToNodeKey)
		}
	}
	if hasCycle(g) {
		return "", fmt.Errorf("workflow graph contains a cycle")
	}
	reachable := reachableFrom(start, g)
	for _, node := range g.nodes {
		if !reachable[node.NodeKey] {
			return "", fmt.Errorf("workflow node %q is unreachable", node.NodeKey)
		}
		if !canReachTerminal(node.NodeKey, g, map[string]bool{}) {
			return "", fmt.Errorf("workflow node %q cannot reach a terminal path", node.NodeKey)
		}
	}
	return start, nil
}

func supportedNodeType(typ string) bool {
	switch typ {
	case "start", "checkpoint", "message", "end", "ask_user", "split", "merge", "switch", "condition", "llm_condition", "tool", "mcp":
		return true
	default:
		return false
	}
}

func (e *Executor) ensureWorkflowRun(ctx context.Context, req GraphRequest, g *graphData, start string) (string, error) {
	existing, err := e.store.CurrentWorkflowRun(ctx, req.RunID, g.def.ID)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return existing.ID, nil
	}
	return e.store.StartWorkflowRun(ctx, req.RunID, g.def.ID, g.def.Version, start, req.Input)
}

func (e *Executor) queueStartIfFresh(ctx context.Context, workflowRunID, start string) error {
	states, err := e.store.WorkflowNodeStates(ctx, workflowRunID)
	if err != nil {
		return err
	}
	for _, state := range states {
		if state.NodeKey == start && state.Status != "pending" {
			return nil
		}
	}
	return e.store.SetWorkflowNodeState(ctx, workflowRunID, start, "queued", nil, nil)
}

func (e *Executor) runReadyQueue(ctx context.Context, req GraphRequest, g *graphData, workflowRunID string) (*GraphResult, error) {
	output := map[string]any{}
	executions := 0
	for {
		states, err := e.stateMap(ctx, workflowRunID)
		if err != nil {
			return nil, err
		}
		for key, state := range states {
			if state.Status == "succeeded" && len(state.Output) > 0 {
				var nodeOut map[string]any
				_ = json.Unmarshal(state.Output, &nodeOut)
				if nodeOut != nil {
					output[key] = nodeOut
					for k, v := range nodeOut {
						output[k] = v
					}
					if err := e.activateOutgoing(ctx, workflowRunID, g, key, nodeOut); err != nil {
						return nil, err
					}
				}
			}
		}
		if awaiting := firstState(states, "awaiting_user"); awaiting != "" {
			return &GraphResult{WorkflowRunID: workflowRunID, State: "awaiting_user", CurrentNode: awaiting, Output: output}, nil
		}
		ready := queuedNodes(states)
		if len(ready) == 0 {
			if e.hasPendingMergeReady(ctx, workflowRunID, g, states) {
				continue
			}
			if hasFailed(states) {
				err := fmt.Errorf("workflow graph failed")
				msg := err.Error()
				_ = e.store.SetWorkflowRunState(context.Background(), workflowRunID, req.RunID, "failed", "", output, &msg)
				return nil, err
			}
			if allTerminal(states) {
				activations, _ := e.activationMap(ctx, workflowRunID)
				if err := e.markInactivePendingSkipped(ctx, workflowRunID, states); err != nil {
					return nil, err
				}
				current := terminalSucceeded(g, states, activations)
				if err := e.store.SetWorkflowRunState(ctx, workflowRunID, req.RunID, "succeeded", current, output, nil); err != nil {
					return nil, err
				}
				return &GraphResult{WorkflowRunID: workflowRunID, State: "succeeded", CurrentNode: current, Output: output}, nil
			}
			return nil, fmt.Errorf("workflow graph stalled with pending nodes")
		}
		if len(ready) > e.concurrency {
			ready = ready[:e.concurrency]
		}
		executions += len(ready)
		if executions > maxNodeExecutions {
			err := fmt.Errorf("workflow graph exceeded %d node executions", maxNodeExecutions)
			msg := err.Error()
			_ = e.store.SetWorkflowRunState(context.Background(), workflowRunID, req.RunID, "failed", "", output, &msg)
			return nil, err
		}
		results := e.executeBatch(ctx, req, g, workflowRunID, ready, states, output)
		for _, result := range results {
			if result.err != nil {
				if result.retry {
					continue
				}
				msg := result.err.Error()
				_ = e.store.SetWorkflowRunState(context.Background(), workflowRunID, req.RunID, "failed", result.nodeKey, result.output, &msg)
				_ = e.store.SetRunState(context.Background(), req.RunID, "failed", &msg)
				return nil, result.err
			}
			if result.state == "awaiting_user" {
				_ = e.store.SetWorkflowRunState(ctx, workflowRunID, req.RunID, "awaiting_user", result.nodeKey, result.output, nil)
				_ = e.store.SetRunState(ctx, req.RunID, "awaiting_user", nil)
				_ = e.store.AddEvent(ctx, req.RunID, "run.awaiting_user", map[string]any{"workflow_run_id": workflowRunID, "node_key": result.nodeKey})
				return &GraphResult{WorkflowRunID: workflowRunID, State: "awaiting_user", CurrentNode: result.nodeKey, Output: result.output}, nil
			}
			if err := e.activateOutgoing(ctx, workflowRunID, g, result.nodeKey, result.output); err != nil {
				return nil, err
			}
		}
	}
}

type nodeResult struct {
	nodeKey string
	state   string
	output  map[string]any
	retry   bool
	err     error
}

func (e *Executor) executeBatch(ctx context.Context, req GraphRequest, g *graphData, workflowRunID string, ready []string, states map[string]db.WorkflowNodeState, output map[string]any) []nodeResult {
	results := make([]nodeResult, len(ready))
	var wg sync.WaitGroup
	for i, nodeKey := range ready {
		i, nodeKey := i, nodeKey
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = e.executeOne(ctx, req, g.nodeByKey[nodeKey], workflowRunID, states[nodeKey], output)
		}()
	}
	wg.Wait()
	return results
}

func (e *Executor) executeOne(ctx context.Context, req GraphRequest, node db.WorkflowNode, workflowRunID string, state db.WorkflowNodeState, previous map[string]any) nodeResult {
	if err := e.store.SetWorkflowNodeState(ctx, workflowRunID, node.NodeKey, "running", nil, nil); err != nil {
		return nodeResult{nodeKey: node.NodeKey, err: err}
	}
	nodeRunID, err := e.store.StartWorkflowNodeRun(ctx, workflowRunID, node.NodeKey, map[string]any{"config": json.RawMessage(node.Config), "input": req.Input})
	if err != nil {
		return nodeResult{nodeKey: node.NodeKey, err: err}
	}
	nodeCtx, cancel := nodeContext(ctx, node)
	defer cancel()
	output, nodeState, err := e.executeNode(nodeCtx, req, node, previous)
	if err != nil {
		msg := err.Error()
		_ = e.store.CompleteWorkflowNodeRun(context.Background(), nodeRunID, "failed", output, &msg)
		if state.Attempts+1 < maxAttempts(node) {
			_ = e.store.SetWorkflowNodeState(context.Background(), workflowRunID, node.NodeKey, "queued", output, &msg)
			return nodeResult{nodeKey: node.NodeKey, output: output, retry: true, err: err}
		}
		_ = e.store.SetWorkflowNodeState(context.Background(), workflowRunID, node.NodeKey, "failed", output, &msg)
		return nodeResult{nodeKey: node.NodeKey, output: output, err: err}
	}
	if err := e.store.CompleteWorkflowNodeRun(ctx, nodeRunID, nodeState, output, nil); err != nil {
		return nodeResult{nodeKey: node.NodeKey, output: output, err: err}
	}
	if err := e.store.SetWorkflowNodeState(ctx, workflowRunID, node.NodeKey, nodeState, output, nil); err != nil {
		return nodeResult{nodeKey: node.NodeKey, output: output, err: err}
	}
	return nodeResult{nodeKey: node.NodeKey, state: nodeState, output: output}
}

func (e *Executor) executeNode(ctx context.Context, req GraphRequest, node db.WorkflowNode, previous map[string]any) (map[string]any, string, error) {
	config, err := configMap(node)
	if err != nil {
		return nil, "", err
	}
	switch node.NodeType {
	case "start", "checkpoint", "message", "end", "split", "merge":
		out := map[string]any{"node_key": node.NodeKey, "node_type": node.NodeType}
		if text, _ := config["text"].(string); text != "" {
			out["text"] = text
		}
		return out, "succeeded", nil
	case "ask_user":
		question, _ := config["question"].(string)
		if question == "" {
			question = "Additional input is required."
		}
		return map[string]any{"question": question}, "awaiting_user", nil
	case "switch":
		key, _ := config["key"].(string)
		return map[string]any{"route": valueAt(previous, key)}, "succeeded", nil
	case "condition":
		route := "false"
		if conditionMatches(config, previous) {
			route = "true"
		}
		return map[string]any{"route": route}, "succeeded", nil
	case "llm_condition":
		return e.executeLLMCondition(ctx, req, node, config, previous)
	case "tool":
		return e.executeToolNode(ctx, req, node, config)
	case "mcp":
		return e.executeMCPNode(ctx, req, node, config)
	default:
		return nil, "", fmt.Errorf("unsupported workflow node_type %q", node.NodeType)
	}
}

func (e *Executor) executeLLMCondition(ctx context.Context, req GraphRequest, node db.WorkflowNode, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	if e.providers == nil {
		return nil, "", fmt.Errorf("llm_condition requires provider registry")
	}
	candidates := providers.ResolveCandidates(e.modelConfig, e.providers.DefaultProvider())
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("no provider candidates configured")
	}
	prompt, _ := config["prompt"].(string)
	if prompt == "" {
		prompt = "Choose a route. Return JSON: {\"route\":\"...\",\"reason\":\"...\"}."
	}
	messages := []providers.Message{{Role: "user", Content: prompt + "\nContext: " + mustJSONString(previous)}}
	var lastErr error
	for _, candidate := range candidates {
		callID, err := e.store.StartModelCall(ctx, req.RunID, candidate.Provider, candidate.Model, map[string]any{"node_key": node.NodeKey, "type": "llm_condition"})
		if err != nil {
			return nil, "", err
		}
		provider, ok := e.providers.Get(candidate.Provider)
		if !ok {
			err = fmt.Errorf("provider %q is not registered", candidate.Provider)
		} else {
			var resp *providers.LLMResponse
			resp, err = provider.Chat(ctx, messages, nil, candidate.Model, nil)
			if err == nil {
				out := map[string]any{}
				if jsonErr := json.Unmarshal([]byte(resp.Content), &out); jsonErr != nil {
					err = jsonErr
				} else if strings.TrimSpace(fmt.Sprint(out["route"])) == "" {
					err = fmt.Errorf("llm_condition response missing route")
				} else {
					_ = e.store.CompleteModelCall(ctx, callID, req.RunID, map[string]any{"finish_reason": resp.FinishReason, "content_length": len(resp.Content)}, nil)
					return out, "succeeded", nil
				}
			}
		}
		lastErr = err
		msg := err.Error()
		_ = e.store.CompleteModelCall(context.Background(), callID, req.RunID, nil, &msg)
	}
	return nil, "", lastErr
}

func (e *Executor) executeToolNode(ctx context.Context, req GraphRequest, node db.WorkflowNode, config map[string]any) (map[string]any, string, error) {
	if e.tools == nil {
		return nil, "", fmt.Errorf("tool node requires tool registry")
	}
	name, _ := config["tool_name"].(string)
	if name == "" {
		name, _ = config["name"].(string)
	}
	args, _ := config["arguments"].(map[string]any)
	retryable := e.tools.Retryable(name)
	callID, err := e.store.StartToolCall(ctx, req.RunID, name, args, retryable)
	if err != nil {
		return nil, "", err
	}
	result := e.tools.Execute(ctx, tools.ExecutionContext{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID, ToolCallID: callID, RequestID: req.RequestID,
	}, name, args)
	var errText *string
	if result.IsError {
		msg := result.ForLLM
		errText = &msg
	}
	if err := e.store.CompleteToolCall(ctx, callID, req.RunID, map[string]any{"for_llm": result.ForLLM, "for_user": result.ForUser, "is_error": result.IsError}, errText); err != nil {
		return nil, "", err
	}
	if result.IsError {
		return nil, "", errors.New(result.ForLLM)
	}
	return map[string]any{"tool_name": name, "result": result.ForLLM}, "succeeded", nil
}

func (e *Executor) executeMCPNode(ctx context.Context, req GraphRequest, node db.WorkflowNode, config map[string]any) (map[string]any, string, error) {
	serverName, _ := config["server_name"].(string)
	toolName, _ := config["tool_name"].(string)
	args, _ := config["arguments"].(map[string]any)
	result, err := mcp.NewExecutor(e.mcpManager, e.store).CallTool(ctx, mcp.ExecutionContext{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID, RequestID: req.RequestID,
	}, serverName, toolName, args)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"server_name": serverName, "tool_name": toolName, "result": result}, "succeeded", nil
}

func (e *Executor) activateOutgoing(ctx context.Context, workflowRunID string, g *graphData, nodeKey string, output map[string]any) error {
	for _, edge := range g.outgoing[nodeKey] {
		if edgeMatches(edge, output) {
			if err := e.store.ActivateWorkflowEdge(ctx, workflowRunID, edge.FromNodeKey, edge.ToNodeKey); err != nil {
				return err
			}
		}
	}
	states, err := e.stateMap(ctx, workflowRunID)
	if err != nil {
		return err
	}
	activations, err := e.activationMap(ctx, workflowRunID)
	if err != nil {
		return err
	}
	for _, edge := range g.outgoing[nodeKey] {
		target := edge.ToNodeKey
		if states[target].Status != "pending" {
			continue
		}
		if e.parentsSatisfied(g, states, activations, target) {
			if err := e.store.SetWorkflowNodeState(ctx, workflowRunID, target, "queued", nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) hasPendingMergeReady(ctx context.Context, workflowRunID string, g *graphData, states map[string]db.WorkflowNodeState) bool {
	activations, err := e.activationMap(ctx, workflowRunID)
	if err != nil {
		return false
	}
	for key, state := range states {
		if state.Status == "pending" && e.parentsSatisfied(g, states, activations, key) {
			_ = e.store.SetWorkflowNodeState(ctx, workflowRunID, key, "queued", nil, nil)
			return true
		}
	}
	return false
}

func (e *Executor) parentsSatisfied(g *graphData, states map[string]db.WorkflowNodeState, activations map[string]bool, nodeKey string) bool {
	incoming := g.incoming[nodeKey]
	if len(incoming) == 0 {
		return false
	}
	activeParents := 0
	for _, edge := range incoming {
		if !activations[edge.FromNodeKey+"->"+edge.ToNodeKey] {
			continue
		}
		activeParents++
		if states[edge.FromNodeKey].Status != "succeeded" {
			return false
		}
	}
	return activeParents > 0
}

func (e *Executor) markInactivePendingSkipped(ctx context.Context, workflowRunID string, states map[string]db.WorkflowNodeState) error {
	for key, state := range states {
		if state.Status == "pending" {
			if err := e.store.SetWorkflowNodeState(ctx, workflowRunID, key, "skipped", nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Executor) stateMap(ctx context.Context, workflowRunID string) (map[string]db.WorkflowNodeState, error) {
	states, err := e.store.WorkflowNodeStates(ctx, workflowRunID)
	if err != nil {
		return nil, err
	}
	out := map[string]db.WorkflowNodeState{}
	for _, state := range states {
		out[state.NodeKey] = state
	}
	return out, nil
}

func (e *Executor) activationMap(ctx context.Context, workflowRunID string) (map[string]bool, error) {
	activations, err := e.store.WorkflowEdgeActivations(ctx, workflowRunID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, activation := range activations {
		if activation.Active {
			out[activation.FromNodeKey+"->"+activation.ToNodeKey] = true
		}
	}
	return out, nil
}

func queuedNodes(states map[string]db.WorkflowNodeState) []string {
	var out []string
	for key, state := range states {
		if state.Status == "queued" {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func allTerminal(states map[string]db.WorkflowNodeState) bool {
	for _, state := range states {
		switch state.Status {
		case "succeeded", "skipped", "pending":
		default:
			return false
		}
	}
	return true
}

func hasFailed(states map[string]db.WorkflowNodeState) bool {
	for _, state := range states {
		if state.Status == "failed" {
			return true
		}
	}
	return false
}

func firstState(states map[string]db.WorkflowNodeState, status string) string {
	var keys []string
	for key, state := range states {
		if state.Status == status {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func terminalSucceeded(g *graphData, states map[string]db.WorkflowNodeState, activations map[string]bool) string {
	var keys []string
	for key, state := range states {
		if state.Status != "succeeded" {
			continue
		}
		hasActiveOutgoing := false
		for _, edge := range g.outgoing[key] {
			if activations[edge.FromNodeKey+"->"+edge.ToNodeKey] {
				hasActiveOutgoing = true
				break
			}
		}
		if !hasActiveOutgoing {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return firstState(states, "succeeded")
	}
	return keys[len(keys)-1]
}

func edgeMatches(edge db.WorkflowEdge, output map[string]any) bool {
	if len(edge.Condition) == 0 || string(edge.Condition) == "{}" {
		return true
	}
	var condition struct {
		Equals    map[string]any `json:"equals"`
		NotEquals map[string]any `json:"not_equals"`
	}
	if err := json.Unmarshal(edge.Condition, &condition); err != nil {
		return false
	}
	if len(condition.Equals) > 0 {
		key, _ := condition.Equals["key"].(string)
		return key != "" && fmt.Sprint(valueAt(output, key)) == fmt.Sprint(condition.Equals["value"])
	}
	if len(condition.NotEquals) > 0 {
		key, _ := condition.NotEquals["key"].(string)
		return key != "" && fmt.Sprint(valueAt(output, key)) != fmt.Sprint(condition.NotEquals["value"])
	}
	return true
}

func conditionMatches(config, output map[string]any) bool {
	if raw, ok := config["equals"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return key != "" && fmt.Sprint(valueAt(output, key)) == fmt.Sprint(raw["value"])
	}
	if raw, ok := config["not_equals"].(map[string]any); ok {
		key, _ := raw["key"].(string)
		return key != "" && fmt.Sprint(valueAt(output, key)) != fmt.Sprint(raw["value"])
	}
	return false
}

func valueAt(values map[string]any, key string) any {
	if key == "" {
		return nil
	}
	if v, ok := values[key]; ok {
		return v
	}
	parts := strings.Split(key, ".")
	var cur any = values
	for _, part := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}

func configMap(node db.WorkflowNode) (map[string]any, error) {
	var config map[string]any
	if len(node.Config) > 0 {
		if err := json.Unmarshal(node.Config, &config); err != nil {
			return nil, err
		}
	}
	if config == nil {
		config = map[string]any{}
	}
	return config, nil
}

func maxAttempts(node db.WorkflowNode) int {
	var policy map[string]any
	_ = json.Unmarshal(node.RetryPolicy, &policy)
	n := intValue(policy["max_attempts"])
	if n <= 0 {
		n = intValue(policy["attempts"])
	}
	if n <= 0 {
		return 1
	}
	return n
}

func nodeContext(ctx context.Context, node db.WorkflowNode) (context.Context, context.CancelFunc) {
	var policy map[string]any
	_ = json.Unmarshal(node.TimeoutPolicy, &policy)
	seconds := intValue(policy["seconds"])
	if seconds <= 0 {
		seconds = intValue(policy["timeout_seconds"])
	}
	if seconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
}

func intValue(raw any) int {
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

func mustJSONString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func firstRootNode(nodes []db.WorkflowNode, edges []db.WorkflowEdge) string {
	incoming := map[string]bool{}
	for _, edge := range edges {
		incoming[edge.ToNodeKey] = true
	}
	var keys []string
	for _, node := range nodes {
		if !incoming[node.NodeKey] {
			keys = append(keys, node.NodeKey)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func hasCycle(g *graphData) bool {
	visited := map[string]int{}
	var visit func(string) bool
	visit = func(key string) bool {
		if visited[key] == 1 {
			return true
		}
		if visited[key] == 2 {
			return false
		}
		visited[key] = 1
		for _, edge := range g.outgoing[key] {
			if visit(edge.ToNodeKey) {
				return true
			}
		}
		visited[key] = 2
		return false
	}
	for key := range g.nodeByKey {
		if visit(key) {
			return true
		}
	}
	return false
}

func reachableFrom(start string, g *graphData) map[string]bool {
	reachable := map[string]bool{}
	var walk func(string)
	walk = func(key string) {
		if reachable[key] {
			return
		}
		reachable[key] = true
		for _, edge := range g.outgoing[key] {
			walk(edge.ToNodeKey)
		}
	}
	walk(start)
	return reachable
}

func canReachTerminal(key string, g *graphData, seen map[string]bool) bool {
	if seen[key] {
		return false
	}
	seen[key] = true
	node := g.nodeByKey[key]
	if node.NodeType == "end" || len(g.outgoing[key]) == 0 {
		return true
	}
	for _, edge := range g.outgoing[key] {
		if canReachTerminal(edge.ToNodeKey, g, cloneBoolMap(seen)) {
			return true
		}
	}
	return false
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type AskUserRequest struct {
	RunID                string         `json:"run_id"`
	WorkflowDefinitionID string         `json:"workflow_definition_id"`
	WorkflowVersion      int            `json:"workflow_version"`
	NodeKey              string         `json:"node_key"`
	Question             string         `json:"question"`
	Input                map[string]any `json:"input,omitempty"`
}

func (e *Executor) StartAskUser(ctx context.Context, req AskUserRequest) (string, error) {
	if e.store == nil {
		return "", fmt.Errorf("workflow executor store is nil")
	}
	if req.RunID == "" || req.WorkflowDefinitionID == "" || req.NodeKey == "" || req.Question == "" {
		return "", fmt.Errorf("run_id, workflow_definition_id, node_key, and question are required")
	}
	workflowRunID, err := e.store.StartWorkflowRun(ctx, req.RunID, req.WorkflowDefinitionID, req.WorkflowVersion, req.NodeKey, req.Input)
	if err != nil {
		return "", err
	}
	nodeRunID, err := e.store.StartWorkflowNodeRun(ctx, workflowRunID, req.NodeKey, map[string]any{"question": req.Question})
	if err != nil {
		return "", err
	}
	if err := e.store.CompleteWorkflowNodeRun(ctx, nodeRunID, "awaiting_user", map[string]any{"question": req.Question}, nil); err != nil {
		return "", err
	}
	if err := e.store.SetWorkflowRunState(ctx, workflowRunID, req.RunID, "awaiting_user", req.NodeKey, map[string]any{"question": req.Question}, nil); err != nil {
		return "", err
	}
	if err := e.store.SetRunState(ctx, req.RunID, "awaiting_user", nil); err != nil {
		return "", err
	}
	if err := e.store.AddEvent(ctx, req.RunID, "run.awaiting_user", map[string]any{
		"workflow_run_id": workflowRunID,
		"node_key":        req.NodeKey,
		"question":        req.Question,
	}); err != nil {
		return "", err
	}
	return workflowRunID, nil
}

type ResumeRequest struct {
	RunID         string         `json:"run_id"`
	WorkflowRunID string         `json:"workflow_run_id"`
	NodeKey       string         `json:"node_key"`
	Response      map[string]any `json:"response"`
}

func (e *Executor) ResumeAwaitingUser(ctx context.Context, req ResumeRequest) error {
	if e.store == nil {
		return fmt.Errorf("workflow executor store is nil")
	}
	if req.RunID == "" || req.WorkflowRunID == "" || req.NodeKey == "" {
		return fmt.Errorf("run_id, workflow_run_id, and node_key are required")
	}
	if err := e.store.SetWorkflowNodeState(ctx, req.WorkflowRunID, req.NodeKey, "succeeded", map[string]any{"response": req.Response}, nil); err != nil {
		return err
	}
	if err := e.store.SetWorkflowRunState(ctx, req.WorkflowRunID, req.RunID, "running", req.NodeKey, map[string]any{"response": req.Response}, nil); err != nil {
		return err
	}
	if err := e.store.SetRunState(ctx, req.RunID, "queued", nil); err != nil {
		return err
	}
	return e.store.AddEvent(ctx, req.RunID, "workflow.resumed", map[string]any{"workflow_run_id": req.WorkflowRunID, "node_key": req.NodeKey})
}

var _ Store = (*db.Store)(nil)
