ALTER TABLE knowledge_documents ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'customer';
ALTER TABLE knowledge_chunks ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'customer';

ALTER TABLE knowledge_documents DROP CONSTRAINT IF EXISTS knowledge_documents_scope_check;
ALTER TABLE knowledge_documents ADD CONSTRAINT knowledge_documents_scope_check CHECK (scope IN ('customer','shared'));

ALTER TABLE knowledge_chunks DROP CONSTRAINT IF EXISTS knowledge_chunks_scope_check;
ALTER TABLE knowledge_chunks ADD CONSTRAINT knowledge_chunks_scope_check CHECK (scope IN ('customer','shared'));

UPDATE knowledge_chunks c
SET scope=d.scope
FROM knowledge_documents d
WHERE c.document_id=d.id AND c.scope<>d.scope;

CREATE INDEX IF NOT EXISTS knowledge_documents_scope_idx ON knowledge_documents (scope, customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS knowledge_chunks_scope_idx ON knowledge_chunks (scope, customer_id, document_id, chunk_index);
