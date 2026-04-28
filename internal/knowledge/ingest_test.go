package knowledge

import (
	"context"
	"testing"
)

type fakeKnowledgeStore struct {
	chunks int
}

func (s *fakeKnowledgeStore) CreateKnowledgeDocument(context.Context, string, string, string, any) (string, error) {
	return "doc-1", nil
}

func (s *fakeKnowledgeStore) AddKnowledgeChunk(context.Context, string, string, int, string, any) (string, error) {
	s.chunks++
	return "chunk", nil
}

func TestIngester(t *testing.T) {
	store := &fakeKnowledgeStore{}
	id, count, err := NewIngester(store).IngestText(context.Background(), "c", "title", "src", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "doc-1" || count != 1 || store.chunks != 1 {
		t.Fatalf("id=%s count=%d store=%#v", id, count, store)
	}
}
