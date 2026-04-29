package agentconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
)

type fakeStore struct {
	imported db.AgentInstanceVersionSpec
	version  *db.AgentInstanceVersion
}

func (s *fakeStore) CreateAgentInstanceVersion(_ context.Context, spec db.AgentInstanceVersionSpec) (*db.AgentInstanceVersion, error) {
	s.imported = spec
	return &db.AgentInstanceVersion{ID: "version-1", CustomerID: spec.CustomerID, AgentInstanceID: spec.AgentInstanceID, Version: 1}, nil
}

func (s *fakeStore) AgentInstanceVersion(context.Context, string) (*db.AgentInstanceVersion, error) {
	return s.version, nil
}

func TestDecodeImportYAML(t *testing.T) {
	doc, err := Decode(bytes.NewBufferString(`
customer_id: c1
agent_instance_id: a1
name: Assistant
model_config:
  primary: mock/duraclaw
profile_config:
  personality: direct
activate_immediately: true
`), "yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	version, err := Import(context.Background(), store, doc)
	if err != nil {
		t.Fatal(err)
	}
	if version.ID != "version-1" || store.imported.CustomerID != "c1" || store.imported.AgentInstanceID != "a1" || store.imported.ModelConfig.(map[string]any)["primary"] != "mock/duraclaw" {
		t.Fatalf("version=%#v spec=%#v", version, store.imported)
	}
	if !store.imported.ActivateImmediately {
		t.Fatalf("expected activate_immediately")
	}
}

func TestExportJSONAndYAML(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeStore{version: &db.AgentInstanceVersion{
		ID: "version-1", CustomerID: "c1", AgentInstanceID: "a1", Version: 2, Name: "Assistant",
		ModelConfig:   json.RawMessage(`{"primary":"mock/duraclaw"}`),
		ProfileConfig: json.RawMessage(`{"personality":"direct"}`),
		Metadata:      json.RawMessage(`{"source":"test"}`),
		CreatedAt:     now,
	}}
	doc, err := Export(context.Background(), store, "version-1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.CustomerID != "c1" || doc.ProfileConfig["personality"] != "direct" {
		t.Fatalf("doc=%#v", doc)
	}
	raw, err := Encode(doc, "json")
	if err != nil || !bytes.Contains(raw, []byte(`"agent_instance_id": "a1"`)) {
		t.Fatalf("json=%s err=%v", raw, err)
	}
	raw, err = Encode(doc, "yaml")
	if err != nil || !bytes.Contains(raw, []byte("agent_instance_id: a1")) {
		t.Fatalf("yaml=%s err=%v", raw, err)
	}
}
