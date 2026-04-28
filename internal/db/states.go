package db

var runStates = map[string]bool{
	"queued": true, "leased": true, "running": true, "running_workflow": true, "awaiting_user": true,
	"completed": true, "failed": true, "cancelled": true, "expired": true,
}

var stepStates = map[string]bool{
	"pending": true, "running": true, "succeeded": true, "failed": true, "skipped": true, "cancelled": true,
}

var artifactStates = map[string]bool{
	"pending": true, "available": true, "processing": true, "processed": true, "failed": true, "expired": true, "deleted": true,
}

func ValidRunState(state string) bool      { return runStates[state] }
func ValidStepState(state string) bool     { return stepStates[state] }
func ValidArtifactState(state string) bool { return artifactStates[state] }
