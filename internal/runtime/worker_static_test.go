package runtime

import (
	"os"
	"strings"
	"testing"
)

func TestWorkerUsesAgentInstanceVersionConfiguration(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"agentInstanceVersionInstructions",
		"modelConfigForRun",
		"version.ModelConfig",
		"WithProviders(w.providers, modelConfig)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("worker missing version configuration hook %q", want)
		}
	}
}

func TestWorkerPassesCountersToAllWorkflowExecutors(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if strings.Count(src, "workflow.NewExecutor(w.store)") != strings.Count(src, "WithCounters(w.counters)") {
		t.Fatalf("every workflow executor path should receive counters")
	}
}

func TestWorkerPassesTraceContextToWorkflowExecutors(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "runTraceContext") || !strings.Contains(src, "TraceParent: traceCtx.TraceParent") || !strings.Contains(src, "TraceID: traceCtx.TraceID") {
		t.Fatalf("workflow executor paths should receive durable trace context")
	}
}

func TestWorkerStreamsProviderDeltasToRunEvents(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"providers.StreamingProvider",
		"ChatStream(ctx, messages, toolDefs, model, nil)",
		`AddEvent(ctx, run.ID, "model.delta"`,
		`AddEvent(ctx, run.ID, "model.tool_delta"`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("worker missing streaming hook %q", want)
		}
	}
}

func TestWorkerAppliesVersionRuntimeConfig(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"toolRegistryForRun",
		"toolAllowedForRun",
		"workflowAllowedForRun",
		"mcpManagerForRun",
		"mcpToolManifest",
		"maxIterationsForRun",
		"maxToolCallsForRun",
		"ToolCallCount",
		"plannedToolExecutions",
		"recordModelUsage",
		"WithAsyncWriter",
		"enqueueAsyncObservability",
		"EnforceRunStartQuota",
		"sessionSummaryContext",
		"knowledgeContext",
		"updateSessionSummary",
		"policyConfigInstructions",
		"policyPackIDsForRun",
		"PolicyPackIDs",
		"pre_artifact_read",
		"artifactRepresentationSummaries",
		"ArtifactRepresentations",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("worker missing version runtime config hook %q", want)
		}
	}
}

func TestDirectWorkflowRunsEnforceWorkflowPolicy(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, `StartRunStep(ctx, run.ID, "workflow_graph"`)
	if start < 0 {
		t.Fatalf("worker direct workflow path not found")
	}
	exec := strings.Index(src[start:], "ExecuteGraph")
	if exec < 0 {
		t.Fatalf("worker direct workflow path not found")
	}
	block := src[start : start+exec]
	if !strings.Contains(block, `Enforce(ctx, "pre_workflow"`) {
		t.Fatalf("direct workflow path should enforce pre_workflow before ExecuteGraph")
	}
	afterExec := src[start+exec:]
	awaiting := strings.Index(afterExec, `result.State == "awaiting_user"`)
	if awaiting < 0 {
		t.Fatalf("direct workflow awaiting branch not found")
	}
	awaitingBlockEnd := strings.Index(afterExec[awaiting:], `runStoppedError{runID: run.ID, state: "awaiting_user"}`)
	if awaitingBlockEnd < 0 {
		t.Fatalf("direct workflow awaiting return not found")
	}
	awaitingBlock := afterExec[awaiting : awaiting+awaitingBlockEnd]
	if !strings.Contains(awaitingBlock, `CompleteRunStep(ctx, run.ID, stepID, "succeeded"`) {
		t.Fatalf("direct workflow awaiting branch should complete workflow_graph step before pausing")
	}
	post := strings.Index(afterExec, `Enforce(ctx, "post_workflow"`)
	if post < 0 {
		t.Fatalf("direct workflow path should enforce post_workflow")
	}
	complete := strings.Index(afterExec[post:], `CompleteRunStep(ctx, run.ID, stepID, "succeeded"`)
	if complete < 0 {
		t.Fatalf("direct workflow terminal completion not found")
	}
	block = afterExec[:post+complete]
	if !strings.Contains(block, `Enforce(ctx, "post_workflow"`) {
		t.Fatalf("direct workflow path should enforce post_workflow before completing the step")
	}
}

func TestScopeJudgeUsesTwoPassImplicitIntent(t *testing.T) {
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		`Intent              string  ` + "`json:\"intent\"`",
		`Classify intent as "direct"`,
		`intent is "implicit", set in_scope to true`,
		"normalizeInitialScopeJudgement(judgement, threshold)",
		`strings.EqualFold(strings.TrimSpace(judgement.Intent), "implicit")`,
		"scopeJudgeContext(ctx, run)",
		`"pass": "context"`,
		"Recent conversation:",
		"sessionSummaryContext(ctx, run)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("scope judge two-pass behavior missing %q", want)
		}
	}
}
