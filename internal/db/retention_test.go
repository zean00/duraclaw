package db

import (
	"os"
	"strings"
	"testing"
)

func TestRetentionQueriesExist(t *testing.T) {
	raw, err := os.ReadFile("retention.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{"ExpireArtifactsOlderThan", "DeleteRunEventsOlderThan", "DeleteCompletedOutboxOlderThan", "DeleteObservabilityEventsOlderThan", "DeleteTerminalBroadcastsOlderThan"} {
		if !strings.Contains(src, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
