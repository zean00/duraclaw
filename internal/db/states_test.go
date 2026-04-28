package db

import "testing"

func TestExactStateEnums(t *testing.T) {
	for _, state := range []string{"queued", "leased", "running", "running_workflow", "awaiting_user", "completed", "failed", "cancelled", "expired"} {
		if !ValidRunState(state) {
			t.Fatalf("expected valid run state %q", state)
		}
	}
	if ValidRunState("waiting") {
		t.Fatalf("unexpected run state accepted")
	}
	for _, state := range []string{"pending", "available", "processing", "processed", "failed", "expired", "deleted"} {
		if !ValidArtifactState(state) {
			t.Fatalf("expected valid artifact state %q", state)
		}
	}
	if ValidArtifactState("ready") {
		t.Fatalf("unexpected artifact state accepted")
	}
	for _, state := range []string{"queued", "running", "awaiting_user", "succeeded", "failed", "cancelled", "expired"} {
		if !ValidWorkflowRunState(state) {
			t.Fatalf("expected valid workflow state %q", state)
		}
	}
	if ValidWorkflowRunState("completed") {
		t.Fatalf("unexpected workflow state accepted")
	}
}
