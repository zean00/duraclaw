package workflow

import (
	"context"
	"strings"
	"testing"

	"duraclaw/internal/db"
)

type fakeManifestStore struct {
	manifests []db.WorkflowManifest
}

func (s fakeManifestStore) WorkflowManifests(context.Context, string, string) ([]db.WorkflowManifest, error) {
	return s.manifests, nil
}

func TestPromptManifest(t *testing.T) {
	got, err := PromptManifest(context.Background(), fakeManifestStore{manifests: []db.WorkflowManifest{{
		ID: "wf-1", Name: "Collect details", Description: "Ask for missing fields", InputSchema: []byte(`{"type":"object"}`),
	}}}, "c", "a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Available workflows") || !strings.Contains(got, "wf-1") {
		t.Fatalf("got %q", got)
	}
}
