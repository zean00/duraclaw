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
