package knowledge

import (
	"context"
	"fmt"

	"duraclaw/internal/embeddings"
)

type Store interface {
	CreateKnowledgeDocument(ctx context.Context, customerID, title, sourceRef string, metadata any) (string, error)
	CreateKnowledgeDocumentWithScope(ctx context.Context, customerID, scope, title, sourceRef string, metadata any) (string, error)
	AddKnowledgeChunk(ctx context.Context, documentID, customerID string, chunkIndex int, content string, metadata any) (string, error)
	SetKnowledgeChunkEmbedding(ctx context.Context, chunkID, customerID string, embedding []float32) error
}

type Ingester struct {
	store    Store
	embedder embeddings.Provider
	maxChars int
}

func NewIngester(store Store) *Ingester {
	return &Ingester{store: store, maxChars: 1200}
}

func (i *Ingester) WithEmbedder(provider embeddings.Provider) *Ingester {
	i.embedder = provider
	return i
}

func (i *Ingester) IngestText(ctx context.Context, customerID, title, sourceRef, text string, metadata map[string]any) (string, int, error) {
	return i.IngestTextWithScope(ctx, customerID, "customer", title, sourceRef, text, metadata)
}

func (i *Ingester) IngestTextWithScope(ctx context.Context, customerID, scope, title, sourceRef, text string, metadata map[string]any) (string, int, error) {
	if i.store == nil {
		return "", 0, fmt.Errorf("knowledge store is nil")
	}
	if customerID == "" {
		return "", 0, fmt.Errorf("customer_id is required")
	}
	documentID, err := i.store.CreateKnowledgeDocumentWithScope(ctx, customerID, scope, title, sourceRef, metadata)
	if err != nil {
		return "", 0, err
	}
	chunks := ChunkText(text, i.maxChars)
	for _, chunk := range chunks {
		chunkID, err := i.store.AddKnowledgeChunk(ctx, documentID, customerID, chunk.Index, chunk.Content, metadata)
		if err != nil {
			return documentID, chunk.Index, err
		}
		if i.embedder != nil {
			embedding, err := i.embedder.Embed(ctx, chunk.Content)
			if err != nil {
				return documentID, chunk.Index, err
			}
			if err := i.store.SetKnowledgeChunkEmbedding(ctx, chunkID, customerID, embedding); err != nil {
				return documentID, chunk.Index, err
			}
		}
	}
	return documentID, len(chunks), nil
}
