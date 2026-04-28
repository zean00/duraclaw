package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"duraclaw/internal/artifacts"
	"duraclaw/internal/db"
	"duraclaw/internal/mcp"
	"duraclaw/internal/observability"
	"duraclaw/internal/outbound"
	"duraclaw/internal/policy"
	"duraclaw/internal/preferences"
	"duraclaw/internal/prompt"
	"duraclaw/internal/providers"
	"duraclaw/internal/tools"
	"duraclaw/internal/workflow"
)

const defaultMaxIterations = 6

type Worker struct {
	store         *db.Store
	providers     *providers.Registry
	modelConfig   providers.ModelConfig
	processors    *artifacts.Registry
	tools         *tools.Registry
	mcpManager    *mcp.Manager
	counters      *observability.Counters
	outbound      *outbound.Service
	policy        *policy.Engine
	owner         string
	leaseFor      time.Duration
	maxIterations int
}

func NewWorker(store *db.Store, provider providers.LLMProvider, owner string) *Worker {
	if provider == nil {
		provider = providers.MockProvider{}
	}
	registry := providers.NewRegistry("mock")
	registry.Register("mock", provider)
	defaultModel := provider.GetDefaultModel()
	if ref := providers.ParseModelRef(defaultModel, "mock"); ref != nil {
		registry.Register(ref.Provider, provider)
	}
	return NewWorkerWithProviders(store, registry, providers.ModelConfig{Primary: defaultModel}, owner)
}

func NewWorkerWithProviders(store *db.Store, registry *providers.Registry, modelConfig providers.ModelConfig, owner string) *Worker {
	if registry == nil {
		registry = providers.NewRegistry("mock")
		registry.Register("mock", providers.MockProvider{})
	}
	if modelConfig.Primary == "" {
		modelConfig.Primary = "mock/duraclaw"
	}
	if owner == "" {
		owner = "duraclaw-worker"
	}
	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(echoTool{})
	if store != nil {
		toolRegistry.Register(tools.RememberTool{Store: store})
		toolRegistry.Register(tools.ListMemoriesTool{Store: store})
		toolRegistry.Register(tools.SavePreferenceTool{Store: store})
		toolRegistry.Register(tools.ListPreferencesTool{Store: store})
	}
	return &Worker{store: store, providers: registry, modelConfig: modelConfig, processors: artifacts.NewRegistry(artifacts.MockProcessor{}), tools: toolRegistry, policy: policy.NewEngine(store), owner: owner, leaseFor: 2 * time.Minute, maxIterations: defaultMaxIterations}
}

func (w *Worker) WithCounters(counters *observability.Counters) *Worker {
	w.counters = counters
	return w
}

func (w *Worker) WithOutbound(service *outbound.Service) *Worker {
	w.outbound = service
	return w
}

func (w *Worker) SetToolRegistry(registry *tools.Registry) {
	if registry == nil {
		registry = tools.NewRegistry()
	}
	w.tools = registry
}

func (w *Worker) SetMCPManager(manager *mcp.Manager) {
	w.mcpManager = manager
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if _, err := w.store.RecoverExpiredLeases(ctx); err != nil {
		return false, err
	}
	run, err := w.store.ClaimRun(ctx, w.owner, w.leaseFor)
	if err != nil || run == nil {
		return false, err
	}
	if err := w.process(ctx, run); err != nil {
		w.inc("worker_runs_failed")
		var stopped runStoppedError
		if !errors.As(err, &stopped) {
			msg := err.Error()
			_ = w.store.SetRunState(context.Background(), run.ID, "failed", &msg)
		}
		return true, err
	}
	w.inc("worker_runs_completed")
	return true, nil
}

func (w *Worker) inc(name string) {
	if w.counters != nil {
		w.counters.Inc(name)
	}
}

func (w *Worker) Loop(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		_, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (w *Worker) process(ctx context.Context, run *db.Run) error {
	if err := w.store.SetRunState(ctx, run.ID, "running", nil); err != nil {
		return err
	}
	if err := w.ensureRunnable(ctx, run.ID); err != nil {
		return err
	}
	if err := w.store.Checkpoint(ctx, run.ID, "started", map[string]any{"state": "running"}); err != nil {
		return err
	}
	workflowContext := ""
	workflowID := workflowIDFromInput(run.Input)
	if workflowID != "" {
		modelConfig, err := w.modelConfigForRun(ctx, run)
		if err != nil {
			return err
		}
		stepID, err := w.store.StartRunStep(ctx, run.ID, "workflow_graph", map[string]any{"workflow_id": workflowID})
		if err != nil {
			return err
		}
		result, err := workflow.NewExecutor(w.store).
			WithProviders(w.providers, modelConfig).
			WithTools(w.tools).
			WithMCP(w.mcpManager).
			WithCounters(w.counters).
			WithPolicy(w.policyEngine()).
			WithProcessors(w.processors).
			ExecuteGraph(ctx, workflow.GraphRequest{
				RunID:                run.ID,
				CustomerID:           run.CustomerID,
				UserID:               run.UserID,
				AgentInstanceID:      run.AgentInstanceID,
				SessionID:            run.SessionID,
				RequestID:            run.RequestID,
				WorkflowDefinitionID: workflowID,
				Input:                inputMap(run.Input),
			})
		if err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return err
		}
		if err := w.store.CompleteRunStep(ctx, run.ID, stepID, "succeeded", result, nil); err != nil {
			return err
		}
		if result.State == "awaiting_user" {
			return runStoppedError{runID: run.ID, state: "awaiting_user"}
		}
		if err := w.store.Checkpoint(ctx, run.ID, "workflow_graph_complete", result); err != nil {
			return err
		}
		workflowContext = workflowOutputText(result.Output)
	}
	contextStepID, err := w.store.StartRunStep(ctx, run.ID, "build_context", map[string]any{"input_bytes": len(run.Input)})
	if err != nil {
		return err
	}
	reprs, err := w.processArtifacts(ctx, run)
	if err != nil {
		msg := err.Error()
		_ = w.store.CompleteRunStep(context.Background(), run.ID, contextStepID, "failed", nil, &msg)
		return err
	}
	text := extractText(run.Input)
	if workflowContext != "" {
		text = text + "\n\nWorkflow context:\n" + workflowContext
	}
	if len(reprs) > 0 {
		text = text + "\n\nArtifact context:\n" + strings.Join(reprs, "\n")
	}
	if err := w.store.CompleteRunStep(ctx, run.ID, contextStepID, "succeeded", map[string]any{"artifact_representations": len(reprs), "context_chars": len(text)}, nil); err != nil {
		return err
	}
	if err := w.ensureRunnable(ctx, run.ID); err != nil {
		return err
	}
	if _, err := w.policyEngine().Enforce(ctx, "pre_run", w.policyContext(run, "", "", text)); err != nil {
		return err
	}
	messages, err := w.providerMessages(ctx, run, text)
	if err != nil {
		return err
	}
	toolDefs := w.toolDefinitions()
	var resp *providers.LLMResponse
	for iteration := 1; iteration <= w.maxIterations; iteration++ {
		modelStepID, err := w.store.StartRunStep(ctx, run.ID, "model_call", map[string]any{"messages": len(messages), "tools": len(toolDefs), "iteration": iteration})
		if err != nil {
			return err
		}
		if _, err := w.policyEngine().Enforce(ctx, "pre_model", w.policyContext(run, modelStepID, "", text)); err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, modelStepID, "failed", nil, &msg)
			return err
		}
		resp, err = w.chat(ctx, run, messages, toolDefs, map[string]any{"messages": len(messages), "tools": len(toolDefs), "iteration": iteration})
		if err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, modelStepID, "failed", nil, &msg)
			return err
		}
		if _, err := w.policyEngine().Enforce(ctx, "post_model", w.policyContext(run, modelStepID, "", resp.Content)); err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, modelStepID, "failed", nil, &msg)
			return err
		}
		if err := w.store.CompleteRunStep(ctx, run.ID, modelStepID, "succeeded", map[string]any{"finish_reason": resp.FinishReason, "tool_calls": len(resp.ToolCalls), "iteration": iteration}, nil); err != nil {
			return err
		}
		if err := w.ensureRunnable(ctx, run.ID); err != nil {
			return err
		}
		if len(resp.ToolCalls) == 0 {
			break
		}
		toolStepID, err := w.store.StartRunStep(ctx, run.ID, "tool_execution", map[string]any{"tool_calls": len(resp.ToolCalls), "iteration": iteration})
		if err != nil {
			return err
		}
		messages = append(messages, providers.Message{Role: "assistant", Content: "Tool calls requested."})
		toolResults, err := w.executeToolCalls(ctx, run, toolStepID, resp.ToolCalls)
		if err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, toolStepID, "failed", nil, &msg)
			return err
		}
		if err := w.store.CompleteRunStep(ctx, run.ID, toolStepID, "succeeded", map[string]any{"tool_results": len(toolResults), "iteration": iteration}, nil); err != nil {
			return err
		}
		messages = append(messages, providers.Message{Role: "tool", Content: strings.Join(toolResults, "\n")})
		if err := w.store.Checkpoint(ctx, run.ID, "tools_complete", map[string]any{"tool_calls": len(toolResults)}); err != nil {
			return err
		}
		if iteration == w.maxIterations {
			return fmt.Errorf("agent loop exceeded %d iterations", w.maxIterations)
		}
	}
	if err := w.store.Checkpoint(ctx, run.ID, "provider_complete", map[string]any{"finish_reason": resp.FinishReason}); err != nil {
		return err
	}
	msgID, err := w.store.InsertMessage(ctx, run.CustomerID, run.SessionID, run.ID, "assistant", map[string]any{
		"parts": []map[string]any{{"type": "text", "text": resp.Content}},
	})
	if err != nil {
		return err
	}
	if err := w.emitFinalOutbound(ctx, run, msgID, resp.Content); err != nil {
		return err
	}
	return w.store.CompleteRunWithMessage(ctx, run.ID, msgID)
}

func (w *Worker) emitFinalOutbound(ctx context.Context, run *db.Run, messageID, text string) error {
	if w.outbound == nil {
		return nil
	}
	_, _, err := w.outbound.Emit(ctx, outbound.Intent{
		CustomerID: run.CustomerID,
		UserID:     run.UserID,
		SessionID:  run.SessionID,
		RunID:      run.ID,
		Type:       "message",
		Payload: map[string]any{
			"message_id": messageID,
			"parts":      []map[string]any{{"type": "text", "text": text}},
		},
	})
	return err
}

func (w *Worker) chat(ctx context.Context, run *db.Run, messages []providers.Message, toolDefs []providers.ToolDefinition, summary map[string]any) (*providers.LLMResponse, error) {
	modelConfig, err := w.modelConfigForRun(ctx, run)
	if err != nil {
		return nil, err
	}
	candidates := providers.ResolveCandidates(modelConfig, w.providers.DefaultProvider())
	if len(candidates) == 0 {
		candidates = []providers.FallbackCandidate{{Provider: "mock", Model: "duraclaw"}}
	}
	var lastErr error
	for _, candidate := range candidates {
		callID, err := w.store.StartModelCall(ctx, run.ID, candidate.Provider, candidate.Model, summary)
		if err != nil {
			return nil, err
		}
		provider, ok := w.providers.Get(candidate.Provider)
		if !ok {
			err = fmt.Errorf("provider %q is not registered", candidate.Provider)
		} else {
			var resp *providers.LLMResponse
			resp, err = provider.Chat(ctx, messages, toolDefs, candidate.Model, nil)
			if err == nil {
				if completeErr := w.store.CompleteModelCall(ctx, callID, run.ID, map[string]any{"finish_reason": resp.FinishReason, "content_length": len(resp.Content)}, nil); completeErr != nil {
					return nil, completeErr
				}
				return resp, nil
			}
		}
		lastErr = err
		msg := err.Error()
		_ = w.store.CompleteModelCall(context.Background(), callID, run.ID, map[string]any{}, &msg)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no provider candidates configured")
	}
	return nil, lastErr
}

func (w *Worker) ensureRunnable(ctx context.Context, runID string) error {
	state, err := w.store.RunState(ctx, runID)
	if err != nil {
		return err
	}
	if state == "cancelled" || state == "expired" {
		return runStoppedError{runID: runID, state: state}
	}
	ok, err := w.store.ExtendRunLease(ctx, runID, w.owner, w.leaseFor)
	if err != nil {
		return err
	}
	if !ok {
		return runStoppedError{runID: runID, state: "lease_lost"}
	}
	return nil
}

type runStoppedError struct {
	runID string
	state string
}

func (e runStoppedError) Error() string {
	return fmt.Sprintf("run %s stopped: %s", e.runID, e.state)
}

func (w *Worker) providerMessages(ctx context.Context, run *db.Run, currentText string) ([]providers.Message, error) {
	history, err := w.store.RecentMessages(ctx, run.CustomerID, run.SessionID, 8)
	if err != nil {
		return nil, err
	}
	promptMessages := make([]prompt.Message, 0, len(history)+1)
	versionInstructions, err := w.agentInstanceVersionInstructions(ctx, run)
	if err != nil {
		return nil, err
	}
	if versionInstructions != "" {
		promptMessages = append(promptMessages, prompt.Message{Role: "system", Content: versionInstructions})
	}
	workflowManifest, err := workflow.PromptManifest(ctx, w.store, run.CustomerID, run.AgentInstanceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(workflowManifest) != "" {
		promptMessages = append(promptMessages, prompt.Message{Role: "system", Content: workflowManifest})
	}
	transferNote, err := w.sessionTransferNote(ctx, run)
	if err != nil {
		return nil, err
	}
	if transferNote != "" {
		promptMessages = append(promptMessages, prompt.Message{Role: "system", Content: transferNote})
	}
	userProfile, err := w.userProfileContext(ctx, run)
	if err != nil {
		return nil, err
	}
	if userProfile != "" {
		promptMessages = append(promptMessages, prompt.Message{Role: "system", Content: userProfile})
	}
	instructions, err := w.policyEngine().PromptInstructions(ctx, w.policyContext(run, "", "", currentText))
	if err != nil {
		return nil, err
	}
	if len(instructions) > 0 {
		promptMessages = append(promptMessages, prompt.Message{Role: "system", Content: strings.Join(instructions, "\n")})
	}
	for _, msg := range history {
		content := messageText(msg.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		promptMessages = append(promptMessages, prompt.Message{Role: msg.Role, Content: content})
	}
	promptMessages = append(promptMessages, prompt.Message{Role: "user", Content: currentText})
	compacted := prompt.CompactMessages(promptMessages, 24000)
	messages := make([]providers.Message, 0, len(compacted.Messages))
	for _, msg := range compacted.Messages {
		messages = append(messages, providers.Message{Role: msg.Role, Content: msg.Content})
	}
	return messages, nil
}

func (w *Worker) agentInstanceVersionInstructions(ctx context.Context, run *db.Run) (string, error) {
	version, err := w.store.AgentInstanceVersion(ctx, run.AgentInstanceVersionID)
	if err != nil || version == nil {
		return "", err
	}
	return strings.TrimSpace(version.SystemInstructions), nil
}

func (w *Worker) modelConfigForRun(ctx context.Context, run *db.Run) (providers.ModelConfig, error) {
	modelConfig := w.modelConfig
	version, err := w.store.AgentInstanceVersion(ctx, run.AgentInstanceVersionID)
	if err != nil || version == nil || len(version.ModelConfig) == 0 {
		return modelConfig, err
	}
	var payload struct {
		Primary   string   `json:"primary"`
		Model     string   `json:"model"`
		Fallbacks []string `json:"fallbacks"`
	}
	if err := json.Unmarshal(version.ModelConfig, &payload); err != nil {
		return modelConfig, err
	}
	if strings.TrimSpace(payload.Primary) != "" {
		modelConfig.Primary = payload.Primary
	} else if strings.TrimSpace(payload.Model) != "" {
		modelConfig.Primary = payload.Model
	}
	if payload.Fallbacks != nil {
		modelConfig.Fallbacks = payload.Fallbacks
	}
	return modelConfig, nil
}

func (w *Worker) sessionTransferNote(ctx context.Context, run *db.Run) (string, error) {
	transfer, err := w.store.LatestSessionTransfer(ctx, run.CustomerID, run.SessionID)
	if err != nil || transfer == nil {
		return "", err
	}
	if transfer.ToAgentInstanceID != run.AgentInstanceID {
		return "", nil
	}
	reason := strings.TrimSpace(transfer.Reason)
	if reason == "" {
		reason = "agent instance reassigned"
	}
	return fmt.Sprintf("Session was reassigned from agent instance %s to %s. Transfer note: %s", transfer.FromAgentInstanceID, transfer.ToAgentInstanceID, reason), nil
}

func (w *Worker) policyEngine() *policy.Engine {
	if w.policy != nil {
		return w.policy
	}
	return policy.NewEngine(w.store)
}

func (w *Worker) policyContext(run *db.Run, stepID, subject, content string) policy.Context {
	pc := policy.Context{
		CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID,
		SessionID: run.SessionID, RunID: run.ID, StepID: stepID, Content: content,
	}
	if strings.HasPrefix(subject, "duraclaw.") || subject == "echo" || subject == "remember" || subject == "list_memories" || subject == "save_preference" || subject == "list_preferences" {
		pc.ToolName = subject
	} else if subject != "" {
		pc.WorkflowID = subject
	}
	return pc
}

func (w *Worker) userProfileContext(ctx context.Context, run *db.Run) (string, error) {
	memories, err := w.store.ListMemories(ctx, run.CustomerID, run.UserID, 8)
	if err != nil {
		return "", err
	}
	allPreferences, err := w.store.ListPreferences(ctx, run.CustomerID, run.UserID, 20)
	if err != nil {
		return "", err
	}
	matchedPreferences := preferences.Match(allPreferences, preferenceContext(time.Now()))
	if len(memories) == 0 && len(matchedPreferences) == 0 {
		return "", nil
	}
	var b strings.Builder
	if len(memories) > 0 {
		b.WriteString("Stable user facts:\n")
		for _, m := range memories {
			fmt.Fprintf(&b, "- %s: %s\n", m.Type, m.Content)
		}
	}
	if len(matchedPreferences) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Conditional user preferences:\n")
		for _, p := range matchedPreferences {
			condition := strings.TrimSpace(string(p.Condition))
			if condition == "" || condition == "null" {
				condition = "{}"
			}
			fmt.Fprintf(&b, "- %s: %s when %s\n", p.Category, p.Content, condition)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func preferenceContext(now time.Time) map[string]any {
	month := now.Month()
	season := "summer"
	switch month {
	case time.December, time.January, time.February:
		season = "winter"
	case time.March, time.April, time.May:
		season = "spring"
	case time.June, time.July, time.August:
		season = "summer"
	default:
		season = "autumn"
	}
	return map[string]any{
		"season": season,
		"month":  int(month),
		"hour":   now.Hour(),
	}
}

func messageText(raw json.RawMessage) string {
	var payload struct {
		Parts []db.ContentPart `json:"parts"`
		Text  string           `json:"text"`
	}
	_ = json.Unmarshal(raw, &payload)
	if strings.TrimSpace(payload.Text) != "" {
		return payload.Text
	}
	var out []string
	for _, p := range payload.Parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

func (w *Worker) toolDefinitions() []providers.ToolDefinition {
	if w.tools == nil {
		return internalToolDefinitions()
	}
	return append(w.tools.ToProviderDefs(), internalToolDefinitions()...)
}

func (w *Worker) executeToolCalls(ctx context.Context, run *db.Run, stepID string, calls []providers.ToolCall) ([]string, error) {
	if w.tools == nil {
		w.tools = tools.NewRegistry()
	}
	completed, err := w.completedNonRetryableTools(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	results := make([]string, 0, len(calls))
	for _, call := range calls {
		if w.isInternalTool(call.Function.Name) {
			result, err := w.executeInternalTool(ctx, run, stepID, call)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
			continue
		}
		if _, err := w.policyEngine().Enforce(ctx, "pre_tool", w.policyContext(run, stepID, call.Function.Name, "")); err != nil {
			return nil, err
		}
		retryable := true
		if w.tools != nil {
			retryable = w.tools.Retryable(call.Function.Name)
		}
		argsHash := db.StableArgsHash(call.Function.Name, call.Function.Arguments)
		if !retryable {
			if prior, ok := completed[call.Function.Name+":"+argsHash]; ok {
				results = append(results, call.Function.Name+": "+string(prior.Result))
				continue
			}
		}
		callID, err := w.store.StartToolCall(ctx, run.ID, call.Function.Name, call.Function.Arguments, retryable)
		if err != nil {
			return nil, err
		}
		result := w.tools.Execute(ctx, tools.ExecutionContext{
			CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID, SessionID: run.SessionID, RunID: run.ID, ToolCallID: callID, RequestID: run.RequestID,
		}, call.Function.Name, call.Function.Arguments)
		var errText *string
		if result.IsError {
			msg := result.ForLLM
			if result.Err != nil {
				msg = result.Err.Error()
			}
			errText = &msg
		}
		if err := w.store.CompleteToolCall(ctx, callID, run.ID, map[string]any{"for_llm": result.ForLLM, "is_error": result.IsError}, errText); err != nil {
			return nil, err
		}
		if result.IsError {
			if result.Err != nil {
				return nil, result.Err
			}
			return nil, fmt.Errorf("%s", result.ForLLM)
		}
		if _, err := w.policyEngine().Enforce(ctx, "post_tool", w.policyContext(run, stepID, call.Function.Name, result.ForLLM)); err != nil {
			return nil, err
		}
		results = append(results, call.Function.Name+": "+result.ForLLM)
	}
	return results, nil
}

func internalToolDefinitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "duraclaw.run_workflow",
				Description: "Run an assigned durable workflow and return its output.",
				Parameters: map[string]any{
					"properties": map[string]any{
						"workflow_id": map[string]any{"type": "string"},
						"input":       map[string]any{"type": "object"},
					},
					"required":             []any{"workflow_id"},
					"additionalProperties": false,
				},
			},
		},
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "duraclaw.ask_user",
				Description: "Pause the run and ask the user for clarification.",
				Parameters: map[string]any{
					"properties":           map[string]any{"question": map[string]any{"type": "string"}},
					"required":             []any{"question"},
					"additionalProperties": false,
				},
			},
		},
	}
}

func (w *Worker) isInternalTool(name string) bool {
	return name == "duraclaw.run_workflow" || name == "duraclaw.ask_user"
}

func (w *Worker) executeInternalTool(ctx context.Context, run *db.Run, stepID string, call providers.ToolCall) (string, error) {
	switch call.Function.Name {
	case "duraclaw.ask_user":
		question, _ := call.Function.Arguments["question"].(string)
		if strings.TrimSpace(question) == "" {
			question = "Additional input is required."
		}
		if _, err := w.policyEngine().Enforce(ctx, "pre_run", w.policyContext(run, stepID, call.Function.Name, question)); err != nil {
			return "", err
		}
		if err := w.store.SetRunState(ctx, run.ID, "awaiting_user", nil); err != nil {
			return "", err
		}
		if err := w.store.AddEvent(ctx, run.ID, "run.awaiting_user", map[string]any{"question": question}); err != nil {
			return "", err
		}
		return "", runStoppedError{runID: run.ID, state: "awaiting_user"}
	case "duraclaw.run_workflow":
		workflowID, _ := call.Function.Arguments["workflow_id"].(string)
		if strings.TrimSpace(workflowID) == "" {
			return "", fmt.Errorf("workflow_id is required")
		}
		if _, err := w.policyEngine().Enforce(ctx, "pre_workflow", w.policyContext(run, stepID, workflowID, "")); err != nil {
			return "", err
		}
		input, _ := call.Function.Arguments["input"].(map[string]any)
		result, err := workflow.NewExecutor(w.store).
			WithProviders(w.providers, w.modelConfig).
			WithTools(w.tools).
			WithMCP(w.mcpManager).
			WithCounters(w.counters).
			WithPolicy(w.policyEngine()).
			WithProcessors(w.processors).
			ExecuteGraph(ctx, workflow.GraphRequest{
				RunID: run.ID, CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID,
				SessionID: run.SessionID, RequestID: run.RequestID, WorkflowDefinitionID: workflowID, Input: input,
			})
		if err != nil {
			return "", err
		}
		if result.State == "awaiting_user" {
			return "", runStoppedError{runID: run.ID, state: "awaiting_user"}
		}
		if _, err := w.policyEngine().Enforce(ctx, "post_workflow", w.policyContext(run, stepID, workflowID, workflowOutputText(result.Output))); err != nil {
			return "", err
		}
		return "duraclaw.run_workflow: " + workflowOutputText(result.Output), nil
	default:
		return "", fmt.Errorf("unsupported internal tool %q", call.Function.Name)
	}
}

func (w *Worker) completedNonRetryableTools(ctx context.Context, runID string) (map[string]db.ToolCallRecord, error) {
	records, err := w.store.CompletedNonRetryableToolCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]db.ToolCallRecord, len(records))
	for _, rec := range records {
		out[rec.ToolName+":"+rec.ArgsHash] = rec
	}
	return out, nil
}

func (w *Worker) processArtifacts(ctx context.Context, run *db.Run) ([]string, error) {
	if w.processors == nil {
		return nil, nil
	}
	inputRefs := artifactRefs(run.Input)
	stored, err := w.store.ArtifactsForRun(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]db.Artifact, len(stored))
	for _, a := range stored {
		byID[a.ID] = a
	}
	var summaries []string
	for _, ref := range inputRefs {
		a, ok := byID[ref]
		if !ok {
			continue
		}
		pa := artifacts.Artifact{ID: a.ID, Modality: a.Modality, MediaType: a.MediaType, StorageRef: a.StorageRef, Metadata: a.Metadata}
		processor, ok := w.processors.ProcessorFor(pa)
		if !ok || a.State == "processed" {
			continue
		}
		if _, err := w.policyEngine().Enforce(ctx, "pre_artifact_process", policy.Context{
			CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID, SessionID: run.SessionID,
			RunID: run.ID, ArtifactID: a.ID, Processor: processor.Name(),
		}); err != nil {
			return nil, err
		}
		stepID, err := w.store.StartRunStep(ctx, run.ID, "artifact_processing", map[string]any{"artifact_id": a.ID, "processor": processor.Name()})
		if err != nil {
			return nil, err
		}
		if err := w.store.SetArtifactState(ctx, a.ID, "processing"); err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return nil, err
		}
		callID, err := w.store.StartProcessorCall(ctx, run.ID, a.ID, processor.Name(), map[string]any{"modality": a.Modality, "media_type": a.MediaType})
		if err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return nil, err
		}
		reps, err := processor.Process(ctx, artifacts.ProcessorContext{
			CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID, SessionID: run.SessionID, RunID: run.ID, ArtifactID: a.ID, RequestID: run.RequestID,
		}, pa)
		if err != nil {
			msg := err.Error()
			_ = w.store.CompleteProcessorCall(context.Background(), callID, run.ID, nil, &msg)
			_ = w.store.SetArtifactState(context.Background(), a.ID, "failed")
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return nil, err
		}
		for _, rep := range reps {
			if err := w.store.InsertArtifactRepresentation(ctx, a.ID, rep.Type, rep.Summary, rep.Metadata); err != nil {
				msg := err.Error()
				_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
				return nil, err
			}
			summaries = append(summaries, "- "+a.ID+" "+rep.Type+": "+rep.Summary)
		}
		if err := w.store.CompleteProcessorCall(ctx, callID, run.ID, map[string]any{"representations": len(reps)}, nil); err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return nil, err
		}
		if err := w.store.SetArtifactState(ctx, a.ID, "processed"); err != nil {
			msg := err.Error()
			_ = w.store.CompleteRunStep(context.Background(), run.ID, stepID, "failed", nil, &msg)
			return nil, err
		}
		if _, err := w.policyEngine().Enforce(ctx, "post_artifact_process", policy.Context{
			CustomerID: run.CustomerID, UserID: run.UserID, AgentInstanceID: run.AgentInstanceID, SessionID: run.SessionID,
			RunID: run.ID, StepID: stepID, ArtifactID: a.ID, Processor: processor.Name(),
		}); err != nil {
			return nil, err
		}
		if err := w.store.CompleteRunStep(ctx, run.ID, stepID, "succeeded", map[string]any{"representations": len(reps)}, nil); err != nil {
			return nil, err
		}
	}
	if len(summaries) > 0 {
		if err := w.store.Checkpoint(ctx, run.ID, "artifacts_processed", map[string]any{"representations": len(summaries)}); err != nil {
			return nil, err
		}
	}
	return summaries, nil
}

func extractText(raw json.RawMessage) string {
	var payload struct {
		Parts []db.ContentPart `json:"parts"`
		Text  string           `json:"text"`
	}
	_ = json.Unmarshal(raw, &payload)
	var b strings.Builder
	if payload.Text != "" {
		b.WriteString(payload.Text)
	}
	for _, p := range payload.Parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(p.Text)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "I received your request."
	}
	return b.String()
}

func workflowIDFromInput(raw json.RawMessage) string {
	payload := inputMap(raw)
	for _, key := range []string{"workflow_id", "workflow_definition_id"} {
		if value, _ := payload[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func inputMap(raw json.RawMessage) map[string]any {
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func workflowOutputText(output map[string]any) string {
	if len(output) == 0 {
		return ""
	}
	if text, _ := output["text"].(string); strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text)
	}
	b, _ := json.Marshal(output)
	return string(b)
}

func artifactRefs(raw json.RawMessage) []string {
	var payload struct {
		Parts []db.ContentPart `json:"parts"`
	}
	_ = json.Unmarshal(raw, &payload)
	var refs []string
	for _, p := range payload.Parts {
		if p.Type != "artifact_ref" {
			continue
		}
		if p.Data != nil {
			if id, ok := p.Data["artifact_id"].(string); ok && strings.TrimSpace(id) != "" {
				refs = append(refs, id)
			}
		}
	}
	return refs
}

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echoes a message for runtime verification." }
func (echoTool) Parameters() map[string]any {
	return map[string]any{
		"properties":           map[string]any{"message": map[string]any{"type": "string"}},
		"required":             []any{"message"},
		"additionalProperties": false,
	}
}
func (echoTool) Execute(ctx context.Context, _ tools.ExecutionContext, args map[string]any) *tools.Result {
	if err := ctx.Err(); err != nil {
		return tools.ErrorResult(err.Error())
	}
	msg, _ := args["message"].(string)
	return tools.NewResult(msg)
}
