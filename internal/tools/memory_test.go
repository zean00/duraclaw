package tools

import (
	"context"
	"testing"

	"duraclaw/internal/db"
)

type fakeMemoryStore struct {
	addedContent string
	memories     []db.Memory
}

func (s *fakeMemoryStore) AddMemory(_ context.Context, _, _, _, _ string, content string, _ any) (string, error) {
	s.addedContent = content
	return "mem-1", nil
}

func (s *fakeMemoryStore) ListMemories(context.Context, string, string, int) ([]db.Memory, error) {
	return s.memories, nil
}

func TestRememberTool(t *testing.T) {
	store := &fakeMemoryStore{}
	res := (RememberTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, map[string]any{"content": "likes tea"})
	if res.IsError || store.addedContent != "likes tea" {
		t.Fatalf("res=%#v store=%#v", res, store)
	}
}

func TestListMemoriesTool(t *testing.T) {
	store := &fakeMemoryStore{memories: []db.Memory{{Type: "preference", Content: "likes tea"}}}
	res := (ListMemoriesTool{Store: store}).Execute(context.Background(), ExecutionContext{CustomerID: "c", UserID: "u"}, nil)
	if res.IsError || res.ForLLM == "" {
		t.Fatalf("res=%#v", res)
	}
}
