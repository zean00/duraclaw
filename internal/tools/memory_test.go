package tools

import (
	"context"
	"strings"
	"testing"

	"duraclaw/internal/db"
)

type fakeMemoryStore struct {
	addedContent    string
	addedCategory   string
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

func (s *fakeMemoryStore) AddPreference(_ context.Context, _, _, _, category string, content string, condition, _ any) (string, error) {
	s.addedCategory = category
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
	desc := SavePreferenceTool{}.Description()
	for _, want := range []string{"preference reference artifact", "call this tool", "preference_reference"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %s", want, desc)
		}
	}
}

func TestSavePreferenceToolAcceptsLegacyKeyValueArgs(t *testing.T) {
	store := &fakeMemoryStore{}
	res := (SavePreferenceTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{
		"key":   "address_format",
		"value": "panggil dengan \"Yang Mulia\"",
	})
	if res.IsError || store.addedPreference != "panggil dengan \"Yang Mulia\"" {
		t.Fatalf("res=%#v store=%#v", res, store)
	}
	if store.addedCategory != "communication_style" {
		t.Fatalf("category=%q", store.addedCategory)
	}
}

func TestPersistenceToolsRejectCaptureLikeContent(t *testing.T) {
	store := &fakeMemoryStore{}
	mem := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "catet tempat makan enak di Jalan Merdeka"})
	if !mem.IsError || store.addedContent != "" {
		t.Fatalf("memory should reject capture-like content: res=%#v store=%#v", mem, store)
	}
	place := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "Ada tempat makan enak di Jalan Merdeka namanya Kedai Contoh"})
	if !place.IsError || store.addedContent != "" {
		t.Fatalf("memory should reject stripped place note: res=%#v store=%#v", place, store)
	}
	idea := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "Ide untuk artikel harian"})
	if !idea.IsError || store.addedContent != "" {
		t.Fatalf("memory should reject capitalized idea note: res=%#v store=%#v", idea, store)
	}
	pref := (SavePreferenceTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "bookmark repo example-client"})
	if !pref.IsError || store.addedPreference != "" {
		t.Fatalf("preference should reject capture-like content: res=%#v store=%#v", pref, store)
	}
	link := (SavePreferenceTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "save https://example.com/docs for later"})
	if !link.IsError || store.addedPreference != "" {
		t.Fatalf("preference should reject link captures: res=%#v store=%#v", link, store)
	}
	product := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "save product ABC-123 for later"})
	if !product.IsError || store.addedContent != "" {
		t.Fatalf("memory should reject product captures: res=%#v store=%#v", product, store)
	}
}

func TestListPreferencesTool(t *testing.T) {
	store := &fakeMemoryStore{preferences: []db.Preference{{Category: "food", Content: "hot chocolate", Condition: []byte(`{"season":"winter"}`)}}}
	res := (ListPreferencesTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, nil)
	if res.IsError || res.ForLLM == "" {
		t.Fatalf("res=%#v", res)
	}
}
