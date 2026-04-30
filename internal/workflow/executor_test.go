package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/providers"
	"duraclaw/internal/tools"
)

type fakeWorkflowStore struct {
	runStates       []string
	workflowStates  []string
	nodeState       string
	events          []string
	nodes           []db.WorkflowNode
	edges           []db.WorkflowEdge
	current         *db.WorkflowRun
	states          map[string]db.WorkflowNodeState
	activations     []db.WorkflowEdgeActivation
	memories        []db.Memory
	preferences     []db.Preference
	knowledge       []db.KnowledgeChunk
	artifacts       []db.Artifact
	reps            []db.ArtifactRepresentation
	policyRules     []db.PolicyRule
	backgroundInput any
	outboundIntent  db.OutboundIntent
}

type testTool struct{}

func (testTool) Name() string               { return "test_tool" }
func (testTool) Description() string        { return "test tool" }
func (testTool) Parameters() map[string]any { return map[string]any{"additionalProperties": true} }
func (testTool) Execute(context.Context, tools.ExecutionContext, map[string]any) *tools.Result {
	return tools.NewResult("tool ok")
}

type routeProvider struct{}

func (routeProvider) GetDefaultModel() string { return "mock/route" }
func (routeProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: `{"route":"approved","reason":"ok"}`, FinishReason: "stop"}, nil
}

type workflowImageProvider struct{}

func (workflowImageProvider) GetDefaultModel() string { return "fake/image" }
func (workflowImageProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}
func (workflowImageProvider) GenerateImage(context.Context, providers.ImageGenerationRequest) (*providers.ImageGenerationResult, error) {
	return &providers.ImageGenerationResult{Images: []providers.GeneratedImage{{URL: "https://example.test/generated.png", RevisedPrompt: "drawn"}}}, nil
}

type testMCPClient struct{}

func (testMCPClient) CallTool(context.Context, mcp.ExecutionContext, string, string, map[string]any) (map[string]any, error) {
	return map[string]any{"mcp": "ok"}, nil
}

func (testMCPClient) ListResources(context.Context, mcp.ExecutionContext, string) ([]mcp.ResourceInfo, error) {
	return []mcp.ResourceInfo{{URI: "file://doc", Name: "doc"}}, nil
}

func (testMCPClient) ReadResource(context.Context, mcp.ExecutionContext, string, string) (*mcp.ResourceContent, error) {
	return &mcp.ResourceContent{URI: "file://doc", Text: "hello"}, nil
}

func (testMCPClient) SubscribeResource(context.Context, mcp.ExecutionContext, string, string) error {
	return nil
}

func (testMCPClient) UnsubscribeResource(context.Context, mcp.ExecutionContext, string, string) error {
	return nil
}

func (testMCPClient) ListPrompts(context.Context, mcp.ExecutionContext, string) ([]mcp.PromptInfo, error) {
	return []mcp.PromptInfo{{Name: "summarize"}}, nil
}

func (testMCPClient) GetPrompt(context.Context, mcp.ExecutionContext, string, string, map[string]any) (*mcp.PromptContent, error) {
	return &mcp.PromptContent{Name: "summarize", Messages: []mcp.PromptMessage{{Role: "user", Content: map[string]any{"type": "text", "text": "Summarize"}}}}, nil
}

func (s *fakeWorkflowStore) WorkflowDefinition(context.Context, string) (*db.WorkflowDefinition, error) {
	return &db.WorkflowDefinition{ID: "wf-1", Version: 2, Status: "active"}, nil
}

func (s *fakeWorkflowStore) WorkflowNodes(context.Context, string) ([]db.WorkflowNode, error) {
	return s.nodes, nil
}

func (s *fakeWorkflowStore) WorkflowEdges(context.Context, string) ([]db.WorkflowEdge, error) {
	return s.edges, nil
}

func (s *fakeWorkflowStore) CurrentWorkflowRun(context.Context, string, string) (*db.WorkflowRun, error) {
	return s.current, nil
}

func (s *fakeWorkflowStore) StartWorkflowRun(context.Context, string, string, int, string, any) (string, error) {
	return "wf-run-1", nil
}

func (s *fakeWorkflowStore) SetWorkflowRunState(_ context.Context, _ string, _ string, state, _ string, _ any, _ *string) error {
	s.workflowStates = append(s.workflowStates, state)
	return nil
}

func (s *fakeWorkflowStore) InitializeWorkflowNodeStates(_ context.Context, workflowRunID string, nodes []db.WorkflowNode) error {
	if s.states == nil {
		s.states = map[string]db.WorkflowNodeState{}
	}
	for _, node := range nodes {
		if _, ok := s.states[node.NodeKey]; !ok {
			s.states[node.NodeKey] = db.WorkflowNodeState{WorkflowRunID: workflowRunID, NodeKey: node.NodeKey, Status: "pending"}
		}
	}
	return nil
}

func (s *fakeWorkflowStore) WorkflowNodeStates(context.Context, string) ([]db.WorkflowNodeState, error) {
	var out []db.WorkflowNodeState
	for _, state := range s.states {
		out = append(out, state)
	}
	return out, nil
}

func (s *fakeWorkflowStore) SetWorkflowNodeState(_ context.Context, workflowRunID, nodeKey, status string, output any, errText *string) error {
	if s.states == nil {
		s.states = map[string]db.WorkflowNodeState{}
	}
	attempts := s.states[nodeKey].Attempts
	if status == "running" {
		attempts++
	}
	b, _ := json.Marshal(output)
	s.states[nodeKey] = db.WorkflowNodeState{WorkflowRunID: workflowRunID, NodeKey: nodeKey, Status: status, Output: b, Error: errText, Attempts: attempts}
	return nil
}

func (s *fakeWorkflowStore) ActivateWorkflowEdge(_ context.Context, workflowRunID, fromNodeKey, toNodeKey string) error {
	s.activations = append(s.activations, db.WorkflowEdgeActivation{WorkflowRunID: workflowRunID, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey, Active: true})
	return nil
}

func (s *fakeWorkflowStore) WorkflowEdgeActivations(context.Context, string) ([]db.WorkflowEdgeActivation, error) {
	return s.activations, nil
}

func (s *fakeWorkflowStore) StartWorkflowNodeRun(context.Context, string, string, any) (string, error) {
	return "node-run-1", nil
}

func (s *fakeWorkflowStore) CompleteWorkflowNodeRun(_ context.Context, _ string, state string, _ any, _ *string) error {
	s.nodeState = state
	return nil
}

func (s *fakeWorkflowStore) SetRunState(_ context.Context, _ string, state string, _ *string) error {
	s.runStates = append(s.runStates, state)
	return nil
}

func (s *fakeWorkflowStore) AddEvent(_ context.Context, _ string, typ string, _ any) error {
	s.events = append(s.events, typ)
	return nil
}

func (s *fakeWorkflowStore) EnforceModelUsageQuota(context.Context, string, string, string) error {
	return nil
}

func (s *fakeWorkflowStore) StartModelCall(context.Context, string, string, string, any) (string, error) {
	return "model-call-1", nil
}

func (s *fakeWorkflowStore) CompleteModelCall(context.Context, string, string, any, *string) error {
	return nil
}

func (s *fakeWorkflowStore) RecordModelUsage(context.Context, db.ModelUsage) error {
	return nil
}

func (s *fakeWorkflowStore) StartToolCall(context.Context, string, string, any, bool) (string, error) {
	return "tool-call-1", nil
}

func (s *fakeWorkflowStore) CompleteToolCall(context.Context, string, string, any, *string) error {
	return nil
}

func (s *fakeWorkflowStore) StartMCPCall(context.Context, string, string, string, any) (string, error) {
	return "mcp-call-1", nil
}

func (s *fakeWorkflowStore) CheckMCPToolAccess(context.Context, string, string, string, string, string) error {
	return nil
}

func (s *fakeWorkflowStore) CompleteMCPCall(context.Context, string, string, any, *string) error {
	return nil
}

func (s *fakeWorkflowStore) ListMemories(context.Context, string, string, int) ([]db.Memory, error) {
	return s.memories, nil
}

func (s *fakeWorkflowStore) AddMemory(context.Context, string, string, string, string, string, any) (string, error) {
	return "memory-1", nil
}

func (s *fakeWorkflowStore) ListPreferences(context.Context, string, string, int) ([]db.Preference, error) {
	return s.preferences, nil
}

func (s *fakeWorkflowStore) AddPreference(context.Context, string, string, string, string, string, any, any) (string, error) {
	return "preference-1", nil
}

func (s *fakeWorkflowStore) ListKnowledgeDocuments(context.Context, string, int) ([]db.KnowledgeDocument, error) {
	return []db.KnowledgeDocument{{ID: "doc-1"}}, nil
}

func (s *fakeWorkflowStore) ListKnowledgeChunks(context.Context, string, int) ([]db.KnowledgeChunk, error) {
	return s.knowledge, nil
}

func (s *fakeWorkflowStore) SearchKnowledgeText(context.Context, string, string, int) ([]db.KnowledgeChunk, error) {
	return nil, nil
}

func (s *fakeWorkflowStore) SearchKnowledgeHybrid(context.Context, string, string, []float32, int) ([]db.KnowledgeChunk, error) {
	return nil, nil
}

func (s *fakeWorkflowStore) ArtifactsForRun(context.Context, string) ([]db.Artifact, error) {
	return s.artifacts, nil
}

func (s *fakeWorkflowStore) ArtifactRepresentations(context.Context, string, string) ([]db.ArtifactRepresentation, error) {
	return s.reps, nil
}

func (s *fakeWorkflowStore) AttachArtifact(_ context.Context, _ string, a db.Artifact) error {
	s.artifacts = append(s.artifacts, a)
	return nil
}

func (s *fakeWorkflowStore) InsertArtifactRepresentation(context.Context, string, string, string, any) error {
	return nil
}

func (s *fakeWorkflowStore) SetArtifactState(context.Context, string, string) error {
	return nil
}

func (s *fakeWorkflowStore) StartProcessorCall(context.Context, string, string, string, any) (string, error) {
	return "processor-call-1", nil
}

func (s *fakeWorkflowStore) CompleteProcessorCall(context.Context, string, string, any, *string) error {
	return nil
}

func (s *fakeWorkflowStore) CreateOutboundIntent(_ context.Context, intent db.OutboundIntent) (string, int64, error) {
	s.outboundIntent = intent
	return "outbound-1", 1, nil
}

func (s *fakeWorkflowStore) CreateRun(context.Context, db.ACPContext, any) (*db.Run, error) {
	return &db.Run{ID: "run-background"}, nil
}

func (s *fakeWorkflowStore) CreateBackgroundRun(_ context.Context, _ db.ACPContext, input any, _ any) (*db.Run, error) {
	s.backgroundInput = input
	return &db.Run{ID: "run-background"}, nil
}

func (s *fakeWorkflowStore) EnforceBackgroundQuota(context.Context, string, string) error {
	return nil
}

func (s *fakeWorkflowStore) MarkRunBackground(context.Context, string) error {
	return nil
}

func (s *fakeWorkflowStore) SetRunProgress(context.Context, string, any) error {
	return nil
}

func (s *fakeWorkflowStore) CreateSchedulerJob(context.Context, db.SchedulerJobSpec) (*db.SchedulerJob, error) {
	return &db.SchedulerJob{ID: "scheduler-1"}, nil
}

func (s *fakeWorkflowStore) PolicyRulesForScope(_ context.Context, _, _, enforcementMode string) ([]db.PolicyRule, error) {
	var out []db.PolicyRule
	for _, rule := range s.policyRules {
		if rule.EnforcementMode == enforcementMode {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s *fakeWorkflowStore) RecordPolicyEvaluation(context.Context, db.PolicyEvaluation) error {
	return nil
}

func TestStartAskUserSetsAwaitingStates(t *testing.T) {
	store := &fakeWorkflowStore{}
	id, err := NewExecutor(store).StartAskUser(context.Background(), AskUserRequest{
		RunID: "run-1", WorkflowDefinitionID: "wf-1", WorkflowVersion: 1, NodeKey: "ask", Question: "Need details?",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "wf-run-1" || store.nodeState != "awaiting_user" {
		t.Fatalf("id=%s store=%#v", id, store)
	}
	if store.workflowStates[len(store.workflowStates)-1] != "awaiting_user" || store.runStates[len(store.runStates)-1] != "awaiting_user" {
		t.Fatalf("store=%#v", store)
	}
}

func TestResumeAwaitingUserQueuesRun(t *testing.T) {
	store := &fakeWorkflowStore{}
	err := NewExecutor(store).ResumeAwaitingUser(context.Background(), ResumeRequest{
		RunID: "run-1", WorkflowRunID: "wf-run-1", NodeKey: "ask", Response: map[string]any{"text": "ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.workflowStates[0] != "running" || store.runStates[0] != "queued" {
		t.Fatalf("store=%#v", store)
	}
}

func TestExecuteGraphRunsDeterministicNodes(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "start", NodeType: "start"},
			{NodeKey: "msg", NodeType: "message", Config: json.RawMessage(`{"text":"hello"}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "start", ToNodeKey: "msg"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.CurrentNode != "msg" || store.nodeState != "succeeded" {
		t.Fatalf("got=%#v store=%#v", got, store)
	}
}

func TestExecuteGraphAskUserPausesRun(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "ask", NodeType: "ask_user", Config: json.RawMessage(`{"question":"Continue?"}`)}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "awaiting_user" || store.nodeState != "awaiting_user" || store.runStates[len(store.runStates)-1] != "awaiting_user" {
		t.Fatalf("got=%#v store=%#v", got, store)
	}
}

func TestExecuteGraphResumesAfterAwaitingNode(t *testing.T) {
	store := &fakeWorkflowStore{
		current: &db.WorkflowRun{ID: "wf-run-existing", Status: "running", CurrentNodeKey: "ask"},
		states: map[string]db.WorkflowNodeState{
			"ask": {WorkflowRunID: "wf-run-existing", NodeKey: "ask", Status: "succeeded", Output: json.RawMessage(`{"response":{"text":"ok"}}`)},
		},
		nodes: []db.WorkflowNode{
			{NodeKey: "ask", NodeType: "ask_user"},
			{NodeKey: "done", NodeType: "end"},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "ask", ToNodeKey: "done"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowRunID != "wf-run-existing" || got.State != "succeeded" || got.CurrentNode != "done" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphUsesConditionalEdges(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "start", NodeType: "message", Config: json.RawMessage(`{"text":"go"}`)},
			{NodeKey: "a", NodeType: "end"},
			{NodeKey: "b", NodeType: "end"},
		},
		edges: []db.WorkflowEdge{
			{FromNodeKey: "start", ToNodeKey: "a", Condition: json.RawMessage(`{"equals":{"key":"text","value":"stop"}}`)},
			{FromNodeKey: "start", ToNodeKey: "b", Condition: json.RawMessage(`{"equals":{"key":"text","value":"go"}}`)},
		},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentNode != "b" {
		t.Fatalf("got=%#v", got)
	}
}

func TestWorkflowConditionAndValueHelpers(t *testing.T) {
	output := map[string]any{"status": "ok", "nested": map[string]any{"value": "deep"}}
	if !edgeMatches(db.WorkflowEdge{Condition: json.RawMessage(`{"not_equals":{"key":"status","value":"bad"}}`)}, output) {
		t.Fatalf("expected not_equals edge match")
	}
	if edgeMatches(db.WorkflowEdge{Condition: json.RawMessage(`{bad`)}, output) {
		t.Fatalf("invalid edge condition should not match")
	}
	if !conditionMatches(map[string]any{"equals": map[string]any{"key": "nested.value", "value": "deep"}}, output) {
		t.Fatalf("expected nested equals match")
	}
	if conditionMatches(map[string]any{"not_equals": map[string]any{"key": "nested.value", "value": "deep"}}, output) {
		t.Fatalf("expected nested not_equals mismatch")
	}
	if got := valueAt(output, "nested.value"); got != "deep" {
		t.Fatalf("valueAt=%#v", got)
	}
	if got := valueAt(output, "nested.missing.value"); got != nil {
		t.Fatalf("valueAt missing=%#v", got)
	}
}

func TestWorkflowPolicyHelpers(t *testing.T) {
	if got := maxAttempts(db.WorkflowNode{RetryPolicy: json.RawMessage(`{"attempts":3}`)}); got != 3 {
		t.Fatalf("maxAttempts=%d", got)
	}
	if got := intValue(json.Number("7")); got != 7 {
		t.Fatalf("intValue=%d", got)
	}
	ctx, cancel := nodeContext(context.Background(), db.WorkflowNode{TimeoutPolicy: json.RawMessage(`{"timeout_seconds":1}`)})
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatalf("expected deadline")
	}
	if _, err := configMap(db.WorkflowNode{Config: json.RawMessage(`{bad`)}); err == nil {
		t.Fatalf("expected invalid config error")
	}
}

func TestExecuteGraphLimitsCycles(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "loop", NodeType: "checkpoint"}},
		edges: []db.WorkflowEdge{{FromNodeKey: "loop", ToNodeKey: "loop"}},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected cycle limit error")
	}
}

func TestExecuteGraphRejectsUnreachableNode(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "start", NodeType: "start"},
			{NodeKey: "orphan", NodeType: "end"},
		},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected unreachable node error")
	}
}

func TestExecuteGraphSplitMerge(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "start", NodeType: "start"},
			{NodeKey: "split", NodeType: "split"},
			{NodeKey: "a", NodeType: "message", Config: json.RawMessage(`{"text":"a"}`)},
			{NodeKey: "b", NodeType: "message", Config: json.RawMessage(`{"text":"b"}`)},
			{NodeKey: "merge", NodeType: "merge"},
		},
		edges: []db.WorkflowEdge{
			{FromNodeKey: "start", ToNodeKey: "split"},
			{FromNodeKey: "split", ToNodeKey: "a"},
			{FromNodeKey: "split", ToNodeKey: "b"},
			{FromNodeKey: "a", ToNodeKey: "merge"},
			{FromNodeKey: "b", ToNodeKey: "merge"},
		},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.CurrentNode != "merge" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphToolNode(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(testTool{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "tool", NodeType: "tool", Config: json.RawMessage(`{"tool_name":"test_tool","arguments":{"x":"y"}}`)}},
	}
	got, err := NewExecutor(store).WithTools(registry).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["result"] != "tool ok" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphGenerateImageNodeAttachesArtifact(t *testing.T) {
	registry := providers.NewRegistry("fake")
	registry.Register("fake", workflowImageProvider{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "image", NodeType: "generate_image", Config: json.RawMessage(`{"prompt":"draw","artifact_id":"img-1"}`)}},
	}
	got, err := NewExecutor(store).WithProviders(registry, providers.ModelConfig{Primary: "fake/image"}).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.Output["artifact_id"] != "img-1" {
		t.Fatalf("got=%#v", got)
	}
	if len(store.artifacts) != 1 || store.artifacts[0].ID != "img-1" || store.artifacts[0].StorageRef != "https://example.test/generated.png" {
		t.Fatalf("artifacts=%#v", store.artifacts)
	}
	if len(store.events) == 0 || store.events[len(store.events)-1] != "artifact.generated" {
		t.Fatalf("events=%#v", store.events)
	}
}

func TestExecuteGraphMCPNode(t *testing.T) {
	manager := mcp.NewManager()
	manager.Register("srv", testMCPClient{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "mcp", NodeType: "mcp", Config: json.RawMessage(`{"server_name":"srv","tool_name":"lookup","arguments":{"x":"y"}}`)}},
	}
	got, err := NewExecutor(store).WithMCP(manager).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphMCPResourceNodes(t *testing.T) {
	manager := mcp.NewManager()
	manager.Register("srv", testMCPClient{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "resources", NodeType: "mcp_list_resources", Config: json.RawMessage(`{"server_name":"srv"}`)}, {NodeKey: "read", NodeType: "mcp_read_resource", Config: json.RawMessage(`{"server_name":"srv","uri":"file://doc"}`)}},
		edges: []db.WorkflowEdge{{FromNodeKey: "resources", ToNodeKey: "read", Condition: json.RawMessage(`{}`)}},
	}
	got, err := NewExecutor(store).WithMCP(manager).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.Output["resource"] == nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphMCPResourceSubscriptionNodes(t *testing.T) {
	manager := mcp.NewManager()
	manager.Register("srv", testMCPClient{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "subscribe", NodeType: "mcp_subscribe_resource", Config: json.RawMessage(`{"server_name":"srv","uri":"file://doc"}`)},
			{NodeKey: "unsubscribe", NodeType: "mcp_unsubscribe_resource", Config: json.RawMessage(`{"server_name":"srv","uri":"file://doc"}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "subscribe", ToNodeKey: "unsubscribe", Condition: json.RawMessage(`{}`)}},
	}
	got, err := NewExecutor(store).WithMCP(manager).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.Output["subscribed"] != false {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphMCPPromptNodes(t *testing.T) {
	manager := mcp.NewManager()
	manager.Register("srv", testMCPClient{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "prompts", NodeType: "mcp_list_prompts", Config: json.RawMessage(`{"server_name":"srv"}`)}, {NodeKey: "get", NodeType: "mcp_get_prompt", Config: json.RawMessage(`{"server_name":"srv","name":"summarize"}`)}},
		edges: []db.WorkflowEdge{{FromNodeKey: "prompts", ToNodeKey: "get", Condition: json.RawMessage(`{}`)}},
	}
	got, err := NewExecutor(store).WithMCP(manager).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "succeeded" || got.Output["prompt"] == nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphLLMConditionRoutesByJSON(t *testing.T) {
	registry := providers.NewRegistry("mock")
	registry.Register("mock", routeProvider{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "llm", NodeType: "llm_condition", Config: json.RawMessage(`{"prompt":"route"}`)},
			{NodeKey: "approved", NodeType: "end"},
			{NodeKey: "rejected", NodeType: "end"},
		},
		edges: []db.WorkflowEdge{
			{FromNodeKey: "llm", ToNodeKey: "approved", Condition: json.RawMessage(`{"equals":{"key":"route","value":"approved"}}`)},
			{FromNodeKey: "llm", ToNodeKey: "rejected", Condition: json.RawMessage(`{"equals":{"key":"route","value":"rejected"}}`)},
		},
	}
	got, err := NewExecutor(store).WithProviders(registry, providers.ModelConfig{Primary: "mock/route"}).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentNode != "approved" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphModelCallStrictJSON(t *testing.T) {
	registry := providers.NewRegistry("mock")
	registry.Register("mock", routeProvider{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "model", NodeType: "model_call", Config: json.RawMessage(`{"prompt":"route","strict_json":true}`)}},
	}
	got, err := NewExecutor(store).WithProviders(registry, providers.ModelConfig{Primary: "mock/route"}).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["route"] != "approved" || got.Output["reason"] != "ok" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphModelCallRequiresProviderRegistry(t *testing.T) {
	store := &fakeWorkflowStore{nodes: []db.WorkflowNode{{NodeKey: "model", NodeType: "model_call"}}}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected provider registry error")
	}
}

func TestExecuteGraphRetriesThenFails(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "tool", NodeType: "tool", RetryPolicy: json.RawMessage(`{"max_attempts":2}`)}},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected failure")
	}
	if store.states["tool"].Attempts != 2 || store.workflowStates[len(store.workflowStates)-1] != "failed" {
		t.Fatalf("states=%#v workflow=%#v", store.states, store.workflowStates)
	}
}

func TestExecuteGraphWriteArtifactNode(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "write", NodeType: "write_artifact", Config: json.RawMessage(`{"artifact_id":"art-1","storage_ref":"object://bucket/doc.txt","media_type":"text/plain","filename":"doc.txt"}`)}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["artifact_id"] != "art-1" || len(store.artifacts) != 1 || store.artifacts[0].StorageRef != "object://bucket/doc.txt" {
		t.Fatalf("got=%#v artifacts=%#v", got, store.artifacts)
	}
}

func TestExecuteGraphWriteArtifactRequiresStorageRef(t *testing.T) {
	store := &fakeWorkflowStore{nodes: []db.WorkflowNode{{NodeKey: "write", NodeType: "write_artifact", Config: json.RawMessage(`{"artifact_id":"art-1"}`)}}}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected storage_ref error")
	}
}

func TestExecuteGraphArchitectureNodeAliases(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(testTool{})
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "tool", NodeType: "tool_call", Config: json.RawMessage(`{"tool_name":"test_tool","arguments":{"x":"y"}}`)}},
	}
	got, err := NewExecutor(store).WithTools(registry).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Output["result"] != "tool ok" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphMemoryAndKnowledgeNodes(t *testing.T) {
	store := &fakeWorkflowStore{
		memories:  []db.Memory{{ID: "m1", Content: "remembered"}},
		knowledge: []db.KnowledgeChunk{{ID: "k1", Content: "needle in haystack"}},
		nodes: []db.WorkflowNode{
			{NodeKey: "memory", NodeType: "read_memory"},
			{NodeKey: "knowledge", NodeType: "retrieve_knowledge", Config: json.RawMessage(`{"query":"needle"}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "memory", ToNodeKey: "knowledge"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentNode != "knowledge" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphPreferenceNodes(t *testing.T) {
	store := &fakeWorkflowStore{
		preferences: []db.Preference{{ID: "p1", Content: "ice cream", Condition: json.RawMessage(`{"season":"summer"}`)}},
		nodes: []db.WorkflowNode{
			{NodeKey: "write", NodeType: "write_preference", Config: json.RawMessage(`{"content":"hot chocolate","condition":{"season":"winter"}}`)},
			{NodeKey: "read", NodeType: "read_preference"},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "write", ToNodeKey: "read"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentNode != "read" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphTransformAndOutboundNodes(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "transform", NodeType: "transform", Config: json.RawMessage(`{"set":{"text":"hello"}}`)},
			{NodeKey: "outbound", NodeType: "emit_outbound_message", Config: json.RawMessage(`{"text_key":"text"}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "transform", ToNodeKey: "outbound"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", SessionID: "s", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentNode != "outbound" {
		t.Fatalf("got=%#v", got)
	}
	if store.outboundIntent.Type != "message" || store.outboundIntent.RunID == nil || *store.outboundIntent.RunID != "run-1" {
		t.Fatalf("intent=%#v", store.outboundIntent)
	}
}

func TestExecuteGraphLoopNodeLimitsItems(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "transform", NodeType: "transform", Config: json.RawMessage(`{"set":{"items":[1,2,3]}}`)},
			{NodeKey: "loop", NodeType: "loop", Config: json.RawMessage(`{"items_key":"items","max_items":2}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "transform", ToNodeKey: "loop"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := got.Output["items"].([]any)
	if fmt.Sprint(got.Output["iterations"]) != "2" || len(items) != 2 {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphCreateBackgroundJobNode(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{
			{NodeKey: "transform", NodeType: "transform", Config: json.RawMessage(`{"set":{"text":"background"}}`)},
			{NodeKey: "background", NodeType: "create_background_job", Config: json.RawMessage(`{"idempotency_key":"fixed-key"}`)},
		},
		edges: []db.WorkflowEdge{{FromNodeKey: "transform", ToNodeKey: "background"}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", RequestID: "req", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := store.backgroundInput.(map[string]any)
	if got.Output["run_id"] != "run-background" || input["text"] != "background" {
		t.Fatalf("got=%#v backgroundInput=%#v", got, store.backgroundInput)
	}
}

func TestExecuteGraphWaitTimerPausesWithSchedulerJob(t *testing.T) {
	store := &fakeWorkflowStore{
		nodes: []db.WorkflowNode{{NodeKey: "wait", NodeType: "wait_timer", Config: json.RawMessage(`{"seconds":5}`)}},
	}
	got, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", WorkflowDefinitionID: "wf-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "awaiting_user" || got.Output["scheduler_job_id"] != "scheduler-1" {
		t.Fatalf("got=%#v", got)
	}
}

func TestExecuteGraphProcessArtifactPostPolicyDenialMarksNodeFailed(t *testing.T) {
	store := &fakeWorkflowStore{
		policyRules: []db.PolicyRule{{
			ID: "rule-1", PolicyPackID: "pack-1", RuleType: "deny", EnforcementMode: "post_artifact_process", Action: "deny", InstructionText: "blocked representation",
		}},
		nodes:     []db.WorkflowNode{{NodeKey: "process", NodeType: "process_artifact", Config: json.RawMessage(`{"artifact_id":"art-1"}`)}},
		artifacts: []db.Artifact{{ID: "art-1", Modality: "image", MediaType: "image/png", State: "available"}},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{
		RunID: "run-1", CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", WorkflowDefinitionID: "wf-1",
	})
	if err == nil {
		t.Fatalf("expected policy denial")
	}
	if store.states["process"].Status != "failed" || store.nodeState != "failed" {
		t.Fatalf("states=%#v nodeState=%s", store.states, store.nodeState)
	}
}

func TestExecuteGraphReadArtifactPrePolicyDenialMarksNodeFailed(t *testing.T) {
	store := &fakeWorkflowStore{
		policyRules: []db.PolicyRule{{
			ID: "rule-1", PolicyPackID: "pack-1", RuleType: "deny", EnforcementMode: "pre_artifact_read", Action: "deny", InstructionText: "blocked artifact read",
		}},
		nodes:     []db.WorkflowNode{{NodeKey: "read", NodeType: "read_artifact", Config: json.RawMessage(`{"artifact_id":"art-1"}`)}},
		artifacts: []db.Artifact{{ID: "art-1", Modality: "document", MediaType: "application/pdf", State: "processed"}},
		reps:      []db.ArtifactRepresentation{{ArtifactID: "art-1", Type: "document_text", Summary: "text"}},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{
		RunID: "run-1", CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", WorkflowDefinitionID: "wf-1",
	})
	if err == nil {
		t.Fatalf("expected policy denial")
	}
	if store.states["read"].Status != "failed" || store.nodeState != "failed" {
		t.Fatalf("states=%#v nodeState=%s", store.states, store.nodeState)
	}
}

func TestExecuteGraphPostNodePolicyDenialMarksNodeFailed(t *testing.T) {
	store := &fakeWorkflowStore{
		policyRules: []db.PolicyRule{{
			ID: "rule-1", PolicyPackID: "pack-1", RuleType: "deny", EnforcementMode: "post_workflow_node", Action: "deny", InstructionText: "blocked",
		}},
		nodes: []db.WorkflowNode{{NodeKey: "msg", NodeType: "message", Config: json.RawMessage(`{"text":"hello"}`)}},
	}
	_, err := NewExecutor(store).ExecuteGraph(context.Background(), GraphRequest{RunID: "run-1", CustomerID: "c", UserID: "u", WorkflowDefinitionID: "wf-1"})
	if err == nil {
		t.Fatalf("expected policy denial")
	}
	if store.states["msg"].Status != "failed" || store.nodeState != "failed" {
		t.Fatalf("states=%#v nodeState=%s", store.states, store.nodeState)
	}
}
