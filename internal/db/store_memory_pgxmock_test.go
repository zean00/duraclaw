package db

import (
	"context"
	"strings"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
)

func TestStoreMemoryAndPreferenceMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()
	sessionID := "s1"

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO memories").
		WithArgs("c1", "u1", "s1", "fact", "likes tea", []byte(`{"source":"chat"}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("mem-1"))
	id, err := store.AddMemory(ctx, "c1", "u1", "s1", "fact", "likes tea", map[string]string{"source": "chat"})
	if err != nil || id != "mem-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "u1", 20).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "memory_type", "content", "metadata"}).
			AddRow("mem-1", "c1", "u1", &sessionID, "fact", "likes tea", []byte(`{}`)))
	memories, err := store.ListMemories(ctx, "c1", "u1", 0)
	if err != nil || len(memories) != 1 || memories[0].ID != "mem-1" {
		t.Fatalf("memories=%#v err=%v", memories, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "u1", "[1,2]", 2).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "memory_type", "content", "metadata"}).
			AddRow("mem-2", "c1", "u1", nil, "summary", "vector hit", []byte(`{}`)))
	found, err := store.SearchMemories(ctx, "c1", "u1", []float32{1, 2}, 2)
	if err != nil || len(found) != 1 || found[0].Content != "vector hit" {
		t.Fatalf("found=%#v err=%v", found, err)
	}

	mock.ExpectExec("UPDATE memories").WithArgs("mem-1", "c1", "u1", "fact", "updated", []byte(`{}`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.UpdateMemory(ctx, "mem-1", "c1", "u1", "fact", "updated", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM memories").WithArgs("mem-1", "c1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	if err := store.DeleteMemory(ctx, "mem-1", "c1", "u1"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO preferences").
		WithArgs("c1", "u1", nil, "general", "prefers concise answers", []byte(`null`), []byte(`null`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("pref-1"))
	prefID, err := store.AddPreference(ctx, "c1", "u1", "", "", "prefers concise answers", nil, nil)
	if err != nil || prefID != "pref-1" {
		t.Fatalf("prefID=%q err=%v", prefID, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "u1", 20).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "user_id", "session_id", "category", "content", "condition", "metadata"}).
			AddRow("pref-1", "c1", "u1", nil, "general", "prefers concise answers", []byte(`null`), []byte(`null`)))
	prefs, err := store.ListPreferences(ctx, "c1", "u1", 0)
	if err != nil || len(prefs) != 1 || prefs[0].Category != "general" {
		t.Fatalf("prefs=%#v err=%v", prefs, err)
	}

	mock.ExpectExec("UPDATE preferences").WithArgs("pref-1", "c1", "u1", "general", "updated", []byte(`null`), []byte(`null`)).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.UpdatePreference(ctx, "pref-1", "c1", "u1", "", "updated", nil, nil); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("DELETE FROM preferences").WithArgs("pref-1", "c1", "u1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeletePreference(ctx, "pref-1", "c1", "u1"); err != nil {
		t.Fatal(err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreKnowledgeDocumentAndChunkMethodsWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO knowledge_documents").
		WithArgs("c1", "shared", "Guide", "doc://guide", []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("doc-1"))
	docID, err := store.CreateKnowledgeDocumentWithScope(ctx, "c1", "shared", "Guide", "doc://guide", map[string]any{})
	if err != nil || docID != "doc-1" {
		t.Fatalf("docID=%q err=%v", docID, err)
	}
	if normalizeKnowledgeScope("other") != "customer" || normalizeKnowledgeScope("shared") != "shared" {
		t.Fatal("unexpected scope normalization")
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", 100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "scope", "title", "source_ref", "metadata"}).
			AddRow("doc-1", "c1", "shared", "Guide", "doc://guide", []byte(`{}`)))
	docs, err := store.ListKnowledgeDocumentsByScope(ctx, "c1", "all", 0)
	if err != nil || len(docs) != 1 || docs[0].Scope != "shared" {
		t.Fatalf("docs=%#v err=%v", docs, err)
	}

	mock.ExpectQuery("INSERT INTO knowledge_chunks").
		WithArgs("doc-1", "c1", 0, "chunk body", []byte(`{}`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("chunk-1"))
	chunkID, err := store.AddKnowledgeChunk(ctx, "doc-1", "c1", 0, "chunk body", map[string]any{})
	if err != nil || chunkID != "chunk-1" {
		t.Fatalf("chunkID=%q err=%v", chunkID, err)
	}

	mock.ExpectExec("UPDATE knowledge_chunks").WithArgs("chunk-1", "c1", "[0.5]").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetKnowledgeChunkEmbedding(ctx, "chunk-1", "c1", []float32{0.5}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE memories").WithArgs("mem-1", "c1", "u1", "[0.25]").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	if err := store.SetMemoryEmbedding(ctx, "mem-1", "c1", "u1", []float32{0.25}); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE preferences").WithArgs("pref-1", "c1", "u1", "[0.75]").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	if err := store.SetPreferenceEmbedding(ctx, "pref-1", "c1", "u1", []float32{0.75}); err == nil {
		t.Fatal("expected preference not found")
	}

	mock.ExpectQuery("SELECT id").WithArgs("doc-1", 100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "document_id", "customer_id", "scope", "chunk_index", "content", "metadata"}).
			AddRow("chunk-1", "doc-1", "c1", "shared", 0, "chunk body", []byte(`{}`)))
	chunks, err := store.ListKnowledgeChunks(ctx, "doc-1", 0)
	if err != nil || len(chunks) != 1 || chunks[0].ID != "chunk-1" {
		t.Fatalf("chunks=%#v err=%v", chunks, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "[1]", 10).
		WillReturnRows(pgxmock.NewRows([]string{"id", "document_id", "customer_id", "scope", "chunk_index", "content", "metadata"}).
			AddRow("chunk-1", "doc-1", "c1", "customer", 0, "vector", []byte(`{}`)))
	vector, err := store.SearchKnowledgeChunks(ctx, "c1", []float32{1}, 0)
	if err != nil || len(vector) != 1 {
		t.Fatalf("vector=%#v err=%v", vector, err)
	}
	empty, err := store.SearchKnowledgeText(ctx, "c1", "  ", 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreKnowledgeConvenienceAndHybridSearchWithPgxMock(t *testing.T) {
	store, mock := newMockStore(t)
	ctx := context.Background()

	mock.ExpectExec("INSERT INTO customers").WithArgs("c1").WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectQuery("INSERT INTO knowledge_documents").
		WithArgs("c1", "customer", "Guide", "doc://guide", []byte(`null`)).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("doc-1"))
	docID, err := store.CreateKnowledgeDocument(ctx, "c1", "Guide", "doc://guide", nil)
	if err != nil || docID != "doc-1" {
		t.Fatalf("docID=%q err=%v", docID, err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", 100).
		WillReturnRows(pgxmock.NewRows([]string{"id", "customer_id", "scope", "title", "source_ref", "metadata"}).
			AddRow("doc-1", "c1", "customer", "Guide", "doc://guide", []byte(`{}`)))
	docs, err := store.ListKnowledgeDocuments(ctx, "c1", 0)
	if err != nil || len(docs) != 1 {
		t.Fatalf("docs=%#v err=%v", docs, err)
	}
	mock.ExpectExec("DELETE FROM knowledge_documents").WithArgs("doc-1", "c1").WillReturnResult(pgxmock.NewResult("DELETE", 1))
	if err := store.DeleteKnowledgeDocument(ctx, "doc-1", "c1"); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT id").WithArgs("c1", "[1]", 2).
		WillReturnRows(pgxmock.NewRows([]string{"id", "document_id", "customer_id", "scope", "chunk_index", "content", "metadata"}).
			AddRow("chunk-1", "doc-1", "c1", "customer", 0, "vector", []byte(`{}`)))
	mock.ExpectQuery("SELECT id").WithArgs("c1", "query", 2).
		WillReturnRows(pgxmock.NewRows([]string{"id", "document_id", "customer_id", "scope", "chunk_index", "content", "metadata"}).
			AddRow("chunk-1", "doc-1", "c1", "customer", 0, "duplicate", []byte(`{}`)).
			AddRow("chunk-2", "doc-1", "c1", "customer", 1, "text", []byte(`{}`)))
	chunks, err := store.SearchKnowledgeHybrid(ctx, "c1", "query", []float32{1}, 2)
	if err != nil || len(chunks) != 2 || chunks[0].Score <= chunks[1].Score {
		t.Fatalf("chunks=%#v err=%v", chunks, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
