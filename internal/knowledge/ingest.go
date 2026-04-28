package knowledge

import (
	"context"
	"fmt"
)

type Store interface {
	CreateKnowledgeDocument(ctx context.Context, customerID, title, sourceRef string, metadata any) (string, error)
	AddKnowledgeChunk(ctx context.Context, documentID, customerID string, chunkIndex int, content string, metadata any) (string, error)
}

type Ingester struct {
	store    Store
	maxChars int
}

func NewIngester(store Store) *Ingester {
	return &Ingester{store: store, maxChars: 1200}
}

func (i *Ingester) IngestText(ctx context.Context, customerID, title, sourceRef, text string, metadata map[string]any) (string, int, error) {
	if i.store == nil {
		return "", 0, fmt.Errorf("knowledge store is nil")
	}
	if customerID == "" {
		return "", 0, fmt.Errorf("customer_id is required")
	}
	documentID, err := i.store.CreateKnowledgeDocument(ctx, customerID, title, sourceRef, metadata)
	if err != nil {
		return "", 0, err
	}
	chunks := ChunkText(text, i.maxChars)
	for _, chunk := range chunks {
		if _, err := i.store.AddKnowledgeChunk(ctx, documentID, customerID, chunk.Index, chunk.Content, metadata); err != nil {
			return documentID, chunk.Index, err
		}
	}
	return documentID, len(chunks), nil
}
