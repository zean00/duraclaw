package db

import (
	"context"
	"os"
	"testing"
)

func TestMigrateAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name='artifact_representations'
		AND column_name='embedding'
	)`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("embedding column missing")
	}
	var extVersion string
	if err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname='vector'`).Scan(&extVersion); err != nil {
		t.Fatalf("pgvector extension missing: %v", err)
	}
	var dims int
	if err := pool.QueryRow(ctx, `SELECT vector_dims(embedding) FROM knowledge_chunks WHERE embedding IS NOT NULL LIMIT 1`).Scan(&dims); err != nil {
		if _, err := pool.Exec(ctx, `
			INSERT INTO customers(id) VALUES('migration-vector-test') ON CONFLICT DO NOTHING;
			INSERT INTO knowledge_documents(customer_id,title) VALUES('migration-vector-test','vector test') ON CONFLICT DO NOTHING;
			INSERT INTO knowledge_chunks(document_id,customer_id,chunk_index,content,embedding)
			SELECT id,'migration-vector-test',999,'vector dimensions',('[0.1,' || repeat('0,',766) || '0.2]')::vector
			FROM knowledge_documents WHERE customer_id='migration-vector-test' LIMIT 1
			ON CONFLICT (document_id, chunk_index) DO UPDATE SET embedding=EXCLUDED.embedding`); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT vector_dims(embedding) FROM knowledge_chunks WHERE customer_id='migration-vector-test' AND chunk_index=999`).Scan(&dims); err != nil {
			t.Fatal(err)
		}
	}
	if dims != 768 {
		t.Fatalf("vector dimensions=%d", dims)
	}
}
