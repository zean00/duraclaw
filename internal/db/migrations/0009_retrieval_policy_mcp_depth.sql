ALTER TABLE knowledge_chunks
ADD COLUMN IF NOT EXISTS search_vector tsvector
GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED;

CREATE INDEX IF NOT EXISTS knowledge_chunks_search_vector_idx
ON knowledge_chunks USING gin (search_vector);

CREATE INDEX IF NOT EXISTS knowledge_chunks_embedding_ivfflat_idx
ON knowledge_chunks USING ivfflat (embedding vector_l2_ops)
WITH (lists = 100)
WHERE embedding IS NOT NULL;
