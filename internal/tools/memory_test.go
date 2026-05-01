package tools

import (
	"context"
	"strings"
	"testing"

	"duraclaw/internal/db"
)

type fakeMemoryStore struct {
	addedContent    string
	addedPreference string
	addedCondition  map[string]any
	memories        []db.Memory
	preferences     []db.Preference
}

func (s *fakeMemoryStore) AddMemory(_ context.Context, _, _, _, _ string, content string, _ any) (string, error) {
	s.addedContent = content
	return "mem-1", nil
}

func (s *fakeMemoryStore) ListMemories(context.Context, string, string, int) ([]db.Memory, error) {
	return s.memories, nil
}

func (s *fakeMemoryStore) AddPreference(_ context.Context, _, _, _, _ string, content string, condition, _ any) (string, error) {
	s.addedPreference = content
	s.addedCondition, _ = condition.(map[string]any)
	return "pref-1", nil
}

func (s *fakeMemoryStore) ListPreferences(context.Context, string, string, int) ([]db.Preference, error) {
	return s.preferences, nil
}

func TestRememberTool(t *testing.T) {
	store := &fakeMemoryStore{}
	res := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "likes tea"})
	if res.IsError || store.addedContent != "likes tea" {
		t.Fatalf("res=%#v store=%#v", res, store)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Type != "memory_reference" || res.Artifacts[0].ID != "mem-1" {
		t.Fatalf("artifacts=%#v", res.Artifacts)
	}
	if !strings.Contains(res.ForLLM, `"memory_reference"`) || res.Artifacts[0].Data["update_api"] != "PUT /admin/memories/mem-1" {
		t.Fatalf("res=%#v", res)
	}
}

func TestListMemoriesTool(t *testing.T) {
	store := &fakeMemoryStore{memories: []db.Memory{{Type: "fact", Content: "works at Acme"}}}
	res := (ListMemoriesTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, nil)
	if res.IsError || res.ForLLM == "" {
		t.Fatalf("res=%#v", res)
	}
}

func TestSavePreferenceTool(t *testing.T) {
	store := &fakeMemoryStore{}
	res := (SavePreferenceTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{
		"content":   "prefers ice cream",
		"condition": map[string]any{"season": "summer"},
	})
	if res.IsError || store.addedPreference != "prefers ice cream" || store.addedCondition["season"] != "summer" {
		t.Fatalf("res=%#v store=%#v", res, store)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Type != "preference_reference" || res.Artifacts[0].ID != "pref-1" {
		t.Fatalf("artifacts=%#v", res.Artifacts)
	}
	if !strings.Contains(res.ForLLM, `"preference_reference"`) || res.Artifacts[0].Data["delete_api"] != "DELETE /admin/preferences/pref-1" {
		t.Fatalf("res=%#v", res)
	}
}

func TestListPreferencesTool(t *testing.T) {
	store := &fakeMemoryStore{preferences: []db.Preference{{Category: "food", Content: "hot chocolate", Condition: []byte(`{"season":"winter"}`)}}}
	res := (ListPreferencesTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, nil)
	if res.IsError || res.ForLLM == "" {
		t.Fatalf("res=%#v", res)
	}
}
