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
		"policyConfigInstructions",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("worker missing version runtime config hook %q", want)
		}
	}
}
