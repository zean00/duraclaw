package knowledge

import (
	"context"
	"testing"

	"duraclaw/internal/embeddings"
)

type fakeKnowledgeStore struct {
	chunks     int
	embeddings int
}

func TestIngesterEmbedsChunksWhenConfigured(t *testing.T) {
	store := &fakeKnowledgeStore{}
	_, _, err := NewIngester(store).WithEmbedder(embeddings.NewHashProvider(8)).IngestText(context.Background(), "c", "title", "src", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.embeddings != 1 {
		t.Fatalf("store=%#v", store)
	}
}

func (s *fakeKnowledgeStore) CreateKnowledgeDocument(context.Context, string, string, string, any) (string, error) {
	return "doc-1", nil
}

func (s *fakeKnowledgeStore) AddKnowledgeChunk(context.Context, string, string, int, string, any) (string, error) {
	s.chunks++
	return "chunk", nil
}

func (s *fakeKnowledgeStore) SetKnowledgeChunkEmbedding(context.Context, string, string, []float32) error {
	s.embeddings++
	return nil
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
