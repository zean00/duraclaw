package workflow

import (
	"context"
	"encoding/json"
	"strings"

	"duraclaw/internal/db"
)

type ManifestStore interface {
	WorkflowManifests(ctx context.Context, customerID, agentInstanceID string) ([]db.WorkflowManifest, error)
}

func PromptManifest(ctx context.Context, store ManifestStore, customerID, agentInstanceID string) (string, error) {
	if store == nil {
		return "", nil
	}
	manifests, err := store.WorkflowManifests(ctx, customerID, agentInstanceID)
	if err != nil {
		return "", err
	}
	if len(manifests) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("Available workflows:\n")
	for _, m := range manifests {
		b.WriteString("- ")
		b.WriteString(m.ID)
		b.WriteString(" ")
		b.WriteString(m.Name)
		if m.Description != "" {
			b.WriteString(": ")
			b.WriteString(m.Description)
		}
		if len(m.InputSchema) > 0 && string(m.InputSchema) != "{}" {
			b.WriteString(" input_schema=")
			compact := json.RawMessage(m.InputSchema)
			b.Write(compact)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}
