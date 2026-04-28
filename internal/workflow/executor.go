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

	"duraclaw/internal/artifacts"
	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"
	"duraclaw/internal/policy"
	"duraclaw/internal/preferences"
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
	ListMemories(ctx context.Context, customerID, userID string, limit int) ([]db.Memory, error)
	AddMemory(ctx context.Context, customerID, userID, sessionID, memoryType, content string, metadata any) (string, error)
	ListPreferences(ctx context.Context, customerID, userID string, limit int) ([]db.Preference, error)
	AddPreference(ctx context.Context, customerID, userID, sessionID, category, content string, condition, metadata any) (string, error)
	ListKnowledgeDocuments(ctx context.Context, customerID string, limit int) ([]db.KnowledgeDocument, error)
	ListKnowledgeChunks(ctx context.Context, documentID string, limit int) ([]db.KnowledgeChunk, error)
	ArtifactsForRun(ctx context.Context, runID string) ([]db.Artifact, error)
	ArtifactRepresentations(ctx context.Context, customerID, artifactID string) ([]db.ArtifactRepresentation, error)
	AttachArtifact(ctx context.Context, runID string, a db.Artifact) error
	InsertArtifactRepresentation(ctx context.Context, artifactID, typ, summary string, metadata any) error
	SetArtifactState(ctx context.Context, artifactID, state string) error
	StartProcessorCall(ctx context.Context, runID, artifactID, processor string, request any) (string, error)
	CompleteProcessorCall(ctx context.Context, callID, runID string, response any, errText *string) error
	CreateOutboundIntent(ctx context.Context, intent db.OutboundIntent) (string, int64, error)
	CreateRun(ctx context.Context, c db.ACPContext, input any) (*db.Run, error)
	CreateSchedulerJob(ctx context.Context, spec db.SchedulerJobSpec) (*db.SchedulerJob, error)
	PolicyRulesForScope(ctx context.Context, customerID, agentInstanceID, enforcementMode string) ([]db.PolicyRule, error)
	RecordPolicyEvaluation(ctx context.Context, ev db.PolicyEvaluation) error
	SetRunState(ctx context.Context, runID, state string, errText *string) error
	AddEvent(ctx context.Context, runID, typ string, payload any) error
}

type Executor struct {
	store       Store
	providers   *providers.Registry
	modelConfig providers.ModelConfig
	tools       *tools.Registry
	mcpManager  *mcp.Manager
	policy      *policy.Engine
	processors  *artifacts.Registry
	counters    *observability.Counters
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

func (e *Executor) WithCounters(counters *observability.Counters) *Executor {
	e.counters = counters
	return e
}

func (e *Executor) WithPolicy(engine *policy.Engine) *Executor {
	e.policy = engine
	return e
}

func (e *Executor) WithProcessors(registry *artifacts.Registry) *Executor {
	e.processors = registry
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
	case "start", "checkpoint", "message", "end", "ask_user", "split", "merge", "switch", "condition", "llm_condition", "tool", "mcp",
		"tool_call", "mcp_call", "model_call", "retrieve_knowledge", "read_memory", "write_memory", "read_preference", "write_preference", "read_artifact", "process_artifact", "write_artifact",
		"branch", "loop", "transform", "wait_timer", "emit_outbound_message", "create_background_job":
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
	if _, err := e.policyEngine().Enforce(ctx, "pre_workflow_node", policy.Context{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID,
		RunID: req.RunID, WorkflowRunID: workflowRunID, WorkflowNodeKey: node.NodeKey,
	}); err != nil {
		msg := err.Error()
		_ = e.store.SetWorkflowNodeState(context.Background(), workflowRunID, node.NodeKey, "failed", nil, &msg)
		return nodeResult{nodeKey: node.NodeKey, err: err}
	}
	if err := e.store.SetWorkflowNodeState(ctx, workflowRunID, node.NodeKey, "running", nil, nil); err != nil {
		return nodeResult{nodeKey: node.NodeKey, err: err}
	}
	nodeRunID, err := e.store.StartWorkflowNodeRun(ctx, workflowRunID, node.NodeKey, map[string]any{"config": json.RawMessage(node.Config), "input": req.Input})
	if err != nil {
		return nodeResult{nodeKey: node.NodeKey, err: err}
	}
	nodeCtx, cancel := nodeContext(ctx, node)
	defer cancel()
	output, nodeState, err := e.executeNode(nodeCtx, req, node, workflowRunID, previous)
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
	if _, err := e.policyEngine().Enforce(ctx, "post_workflow_node", policy.Context{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID,
		RunID: req.RunID, WorkflowRunID: workflowRunID, WorkflowNodeKey: node.NodeKey, Content: mustJSONString(output),
	}); err != nil {
		msg := err.Error()
		_ = e.store.CompleteWorkflowNodeRun(context.Background(), nodeRunID, "failed", output, &msg)
		_ = e.store.SetWorkflowNodeState(context.Background(), workflowRunID, node.NodeKey, "failed", output, &msg)
		return nodeResult{nodeKey: node.NodeKey, output: output, err: err}
	}
	return nodeResult{nodeKey: node.NodeKey, state: nodeState, output: output}
}

func (e *Executor) executeNode(ctx context.Context, req GraphRequest, node db.WorkflowNode, workflowRunID string, previous map[string]any) (map[string]any, string, error) {
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
	case "switch", "branch":
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
	case "model_call":
		return e.executeModelCall(ctx, req, node, config, previous)
	case "tool", "tool_call":
		return e.executeToolNode(ctx, req, node, config)
	case "mcp", "mcp_call":
		return e.executeMCPNode(ctx, req, node, config)
	case "retrieve_knowledge":
		return e.executeRetrieveKnowledge(ctx, req, config, previous)
	case "read_memory":
		return e.executeReadMemory(ctx, req, config)
	case "write_memory":
		return e.executeWriteMemory(ctx, req, config, previous)
	case "read_preference":
		return e.executeReadPreference(ctx, req, config)
	case "write_preference":
		return e.executeWritePreference(ctx, req, config, previous)
	case "read_artifact":
		return e.executeReadArtifact(ctx, req, config)
	case "process_artifact":
		return e.executeProcessArtifact(ctx, req, config)
	case "write_artifact":
		return e.executeWriteArtifact(ctx, req, config)
	case "transform":
		return executeTransform(config, previous), "succeeded", nil
	case "loop":
		return executeLoop(config, previous), "succeeded", nil
	case "emit_outbound_message":
		return e.executeEmitOutbound(ctx, req, config, previous)
	case "create_background_job":
		return e.executeCreateBackgroundJob(ctx, req, config, previous)
	case "wait_timer":
		return e.executeWaitTimer(ctx, req, workflowRunID, node.NodeKey, config)
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

func (e *Executor) executeModelCall(ctx context.Context, req GraphRequest, node db.WorkflowNode, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	if e.providers == nil {
		return nil, "", fmt.Errorf("model_call requires provider registry")
	}
	candidates := providers.ResolveCandidates(e.modelConfig, e.providers.DefaultProvider())
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("no provider candidates configured")
	}
	promptText, _ := config["prompt"].(string)
	if promptText == "" {
		promptText = "Complete the workflow model step."
	}
	strictJSON, _ := config["strict_json"].(bool)
	messages := []providers.Message{{Role: "user", Content: promptText + "\nContext: " + mustJSONString(previous)}}
	var lastErr error
	for _, candidate := range candidates {
		callID, err := e.store.StartModelCall(ctx, req.RunID, candidate.Provider, candidate.Model, map[string]any{"node_key": node.NodeKey, "type": "model_call"})
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
				out := map[string]any{"content": resp.Content, "finish_reason": resp.FinishReason}
				if strictJSON {
					parsed := map[string]any{}
					if jsonErr := json.Unmarshal([]byte(resp.Content), &parsed); jsonErr != nil {
						err = jsonErr
					} else {
						out = parsed
					}
				}
				if err == nil {
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
	result, err := mcp.NewExecutor(e.mcpManager, e.store).WithCounters(e.counters).CallTool(ctx, mcp.ExecutionContext{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID, RequestID: req.RequestID,
	}, serverName, toolName, args)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"server_name": serverName, "tool_name": toolName, "result": result}, "succeeded", nil
}

func (e *Executor) executeReadMemory(ctx context.Context, req GraphRequest, config map[string]any) (map[string]any, string, error) {
	limit := intValue(config["limit"])
	memories, err := e.store.ListMemories(ctx, req.CustomerID, req.UserID, limit)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"memories": memories}, "succeeded", nil
}

func (e *Executor) executeWriteMemory(ctx context.Context, req GraphRequest, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	content, _ := config["content"].(string)
	if key, _ := config["content_key"].(string); key != "" {
		content = fmt.Sprint(valueAt(previous, key))
	}
	if strings.TrimSpace(content) == "" {
		return nil, "", fmt.Errorf("write_memory requires content or content_key")
	}
	if _, err := e.policyEngine().Enforce(ctx, "pre_memory_write", policy.Context{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID, Content: content,
	}); err != nil {
		return nil, "", err
	}
	memoryType, _ := config["memory_type"].(string)
	if memoryType == "" {
		memoryType = "workflow"
	}
	id, err := e.store.AddMemory(ctx, req.CustomerID, req.UserID, req.SessionID, memoryType, content, config["metadata"])
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"memory_id": id, "content": content}, "succeeded", nil
}

func (e *Executor) executeReadPreference(ctx context.Context, req GraphRequest, config map[string]any) (map[string]any, string, error) {
	limit := intValue(config["limit"])
	list, err := e.store.ListPreferences(ctx, req.CustomerID, req.UserID, limit)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"preferences": preferences.Match(list, mapValue(config["context"]))}, "succeeded", nil
}

func (e *Executor) executeWritePreference(ctx context.Context, req GraphRequest, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	content, _ := config["content"].(string)
	if key, _ := config["content_key"].(string); key != "" {
		content = fmt.Sprint(valueAt(previous, key))
	}
	if strings.TrimSpace(content) == "" {
		return nil, "", fmt.Errorf("write_preference requires content or content_key")
	}
	if _, err := e.policyEngine().Enforce(ctx, "pre_memory_write", policy.Context{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID, Content: content,
	}); err != nil {
		return nil, "", err
	}
	id, err := e.store.AddPreference(ctx, req.CustomerID, req.UserID, req.SessionID, stringValue(config["category"], "general"), content, mapValue(config["condition"]), config["metadata"])
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"preference_id": id, "content": content, "condition": mapValue(config["condition"])}, "succeeded", nil
}

func (e *Executor) executeRetrieveKnowledge(ctx context.Context, req GraphRequest, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	query, _ := config["query"].(string)
	if key, _ := config["query_key"].(string); key != "" {
		query = fmt.Sprint(valueAt(previous, key))
	}
	limit := intValue(config["limit"])
	if limit <= 0 {
		limit = 5
	}
	docs, err := e.store.ListKnowledgeDocuments(ctx, req.CustomerID, 50)
	if err != nil {
		return nil, "", err
	}
	query = strings.ToLower(query)
	var chunks []db.KnowledgeChunk
	for _, doc := range docs {
		docChunks, err := e.store.ListKnowledgeChunks(ctx, doc.ID, 100)
		if err != nil {
			return nil, "", err
		}
		for _, chunk := range docChunks {
			if query == "" || strings.Contains(strings.ToLower(chunk.Content), query) {
				chunks = append(chunks, chunk)
				if len(chunks) >= limit {
					return map[string]any{"chunks": chunks}, "succeeded", nil
				}
			}
		}
	}
	return map[string]any{"chunks": chunks}, "succeeded", nil
}

func (e *Executor) executeReadArtifact(ctx context.Context, req GraphRequest, config map[string]any) (map[string]any, string, error) {
	artifactID, _ := config["artifact_id"].(string)
	artifactsForRun, err := e.store.ArtifactsForRun(ctx, req.RunID)
	if err != nil {
		return nil, "", err
	}
	for _, artifact := range artifactsForRun {
		if artifactID != "" && artifact.ID != artifactID {
			continue
		}
		reps, err := e.store.ArtifactRepresentations(ctx, req.CustomerID, artifact.ID)
		if err != nil {
			return nil, "", err
		}
		return map[string]any{"artifact": artifact, "representations": reps}, "succeeded", nil
	}
	return nil, "", fmt.Errorf("artifact not found")
}

func (e *Executor) executeProcessArtifact(ctx context.Context, req GraphRequest, config map[string]any) (map[string]any, string, error) {
	if e.processors == nil {
		e.processors = artifacts.NewRegistry(artifacts.MockProcessor{})
	}
	artifactID, _ := config["artifact_id"].(string)
	artifactsForRun, err := e.store.ArtifactsForRun(ctx, req.RunID)
	if err != nil {
		return nil, "", err
	}
	for _, artifact := range artifactsForRun {
		if artifactID != "" && artifact.ID != artifactID {
			continue
		}
		pa := artifacts.Artifact{ID: artifact.ID, Modality: artifact.Modality, MediaType: artifact.MediaType, StorageRef: artifact.StorageRef, Metadata: artifact.Metadata}
		processor, ok := e.processors.ProcessorFor(pa)
		if !ok {
			return nil, "", fmt.Errorf("no artifact processor for %s", artifact.ID)
		}
		if _, err := e.policyEngine().Enforce(ctx, "pre_artifact_process", policy.Context{
			CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID,
			ArtifactID: artifact.ID, Processor: processor.Name(),
		}); err != nil {
			return nil, "", err
		}
		callID, err := e.store.StartProcessorCall(ctx, req.RunID, artifact.ID, processor.Name(), map[string]any{"modality": artifact.Modality, "media_type": artifact.MediaType})
		if err != nil {
			return nil, "", err
		}
		reps, err := processor.Process(ctx, artifacts.ProcessorContext{
			CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID, RunID: req.RunID,
			ArtifactID: artifact.ID, RequestID: req.RequestID,
		}, pa)
		if err != nil {
			msg := err.Error()
			_ = e.store.CompleteProcessorCall(context.Background(), callID, req.RunID, nil, &msg)
			return nil, "", err
		}
		for _, rep := range reps {
			if err := e.store.InsertArtifactRepresentation(ctx, artifact.ID, rep.Type, rep.Summary, rep.Metadata); err != nil {
				return nil, "", err
			}
		}
		if err := e.store.CompleteProcessorCall(ctx, callID, req.RunID, map[string]any{"representations": len(reps)}, nil); err != nil {
			return nil, "", err
		}
		_ = e.store.SetArtifactState(ctx, artifact.ID, "processed")
		return map[string]any{"artifact_id": artifact.ID, "representations": len(reps)}, "succeeded", nil
	}
	return nil, "", fmt.Errorf("artifact not found")
}

func (e *Executor) executeWriteArtifact(ctx context.Context, req GraphRequest, config map[string]any) (map[string]any, string, error) {
	id, _ := config["artifact_id"].(string)
	if id == "" {
		id = "artifact-" + fmt.Sprint(time.Now().UnixNano())
	}
	storageRef, _ := config["storage_ref"].(string)
	if storageRef == "" {
		return nil, "", fmt.Errorf("write_artifact requires storage_ref")
	}
	artifact := db.Artifact{
		ID: id, Modality: stringValue(config["modality"], "document"), MediaType: stringValue(config["media_type"], "application/octet-stream"),
		Filename: stringValue(config["filename"], ""), StorageRef: storageRef, State: "available", Metadata: mapValue(config["metadata"]),
	}
	if err := e.store.AttachArtifact(ctx, req.RunID, artifact); err != nil {
		return nil, "", err
	}
	return map[string]any{"artifact_id": id}, "succeeded", nil
}

func executeTransform(config map[string]any, previous map[string]any) map[string]any {
	out := map[string]any{}
	if constants, ok := config["set"].(map[string]any); ok {
		for k, v := range constants {
			out[k] = v
		}
	}
	if mappings, ok := config["map"].(map[string]any); ok {
		for outKey, raw := range mappings {
			if inKey, ok := raw.(string); ok {
				out[outKey] = valueAt(previous, inKey)
			}
		}
	}
	return out
}

func executeLoop(config map[string]any, previous map[string]any) map[string]any {
	items, _ := valueAt(previous, stringValue(config["items_key"], "items")).([]any)
	max := intValue(config["max_items"])
	if max <= 0 || max > 100 {
		max = 100
	}
	count := len(items)
	if count > max {
		count = max
	}
	return map[string]any{"iterations": count, "items": items[:count]}
}

func (e *Executor) executeEmitOutbound(ctx context.Context, req GraphRequest, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	payload := mapValue(config["payload"])
	if textKey, _ := config["text_key"].(string); textKey != "" {
		payload["text"] = valueAt(previous, textKey)
	}
	runID := req.RunID
	id, outboxID, err := e.store.CreateOutboundIntent(ctx, db.OutboundIntent{
		CustomerID: req.CustomerID, UserID: req.UserID, SessionID: req.SessionID, RunID: &runID, Type: stringValue(config["intent_type"], "message"),
		Payload: mustRawJSON(payload),
	})
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"outbound_intent_id": id, "outbox_id": outboxID}, "succeeded", nil
}

func (e *Executor) executeCreateBackgroundJob(ctx context.Context, req GraphRequest, config map[string]any, previous map[string]any) (map[string]any, string, error) {
	input := mapValue(config["input"])
	if len(input) == 0 {
		input = previous
	}
	key := stringValue(config["idempotency_key"], "workflow-background:"+req.RunID+":"+fmt.Sprint(time.Now().UnixNano()))
	run, err := e.store.CreateRun(ctx, db.ACPContext{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID,
		RequestID: req.RequestID + ":background", IdempotencyKey: key,
	}, input)
	if err != nil {
		return nil, "", err
	}
	return map[string]any{"run_id": run.ID}, "succeeded", nil
}

func (e *Executor) executeWaitTimer(ctx context.Context, req GraphRequest, workflowRunID, nodeKey string, config map[string]any) (map[string]any, string, error) {
	seconds := intValue(config["seconds"])
	if seconds <= 0 {
		seconds = 60
	}
	resumeAt := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
	schedule := stringValue(config["schedule"], "@once")
	job, err := e.store.CreateSchedulerJob(ctx, db.SchedulerJobSpec{
		CustomerID: req.CustomerID, UserID: req.UserID, AgentInstanceID: req.AgentInstanceID, SessionID: req.SessionID,
		JobType: "workflow_timer", Schedule: schedule, NextRunAt: resumeAt,
		Input:    map[string]any{"text": "Workflow timer fired.", "workflow_definition_id": req.WorkflowDefinitionID},
		Metadata: map[string]any{"workflow_wake": map[string]any{"run_id": req.RunID, "workflow_run_id": workflowRunID, "node_key": nodeKey}},
	})
	if err != nil {
		return nil, "", err
	}
	_ = e.store.AddEvent(ctx, req.RunID, "workflow.wait_timer", map[string]any{"seconds": seconds, "resume_after": resumeAt, "scheduler_job_id": job.ID})
	return map[string]any{"wait_seconds": seconds, "scheduler_job_id": job.ID}, "awaiting_user", nil
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

func (e *Executor) policyEngine() *policy.Engine {
	if e.policy != nil {
		return e.policy
	}
	return policy.NewEngine(e.store)
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

func mustRawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func stringValue(raw any, fallback string) string {
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}

func mapValue(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{}
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
