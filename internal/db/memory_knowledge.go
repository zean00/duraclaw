package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Memory struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	UserID     string          `json:"user_id"`
	SessionID  *string         `json:"session_id,omitempty"`
	Type       string          `json:"memory_type"`
	Content    string          `json:"content"`
	Metadata   json.RawMessage `json:"metadata"`
}

type Preference struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	UserID     string          `json:"user_id"`
	SessionID  *string         `json:"session_id,omitempty"`
	Category   string          `json:"category"`
	Content    string          `json:"content"`
	Condition  json.RawMessage `json:"condition"`
	Metadata   json.RawMessage `json:"metadata"`
}

func (s *Store) AddMemory(ctx context.Context, customerID, userID, sessionID, memoryType, content string, metadata any) (string, error) {
	if err := s.ensureCustomer(ctx, customerID); err != nil {
		return "", err
	}
	b, _ := json.Marshal(metadata)
	var nullableSession any
	if sessionID != "" {
		nullableSession = sessionID
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO memories(customer_id,user_id,session_id,memory_type,content,metadata)
		VALUES($1,$2,$3,$4,$5,$6)
		RETURNING id::text`, customerID, userID, nullableSession, memoryType, content, b).Scan(&id)
	return id, err
}

func (s *Store) ListMemories(ctx context.Context, customerID, userID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata
		FROM memories
		WHERE customer_id=$1 AND user_id=$2
		ORDER BY updated_at DESC
		LIMIT $3`, customerID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.CustomerID, &m.UserID, &m.SessionID, &m.Type, &m.Content, &m.Metadata); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMemory(ctx context.Context, memoryID, customerID, userID, memoryType, content string, metadata any) error {
	b, _ := json.Marshal(metadata)
	tag, err := s.pool.Exec(ctx, `
		UPDATE memories
		SET memory_type=$4, content=$5, metadata=$6, updated_at=now()
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`,
		memoryID, customerID, userID, memoryType, content, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("memory not found")
	}
	return nil
}

func (s *Store) DeleteMemory(ctx context.Context, memoryID, customerID, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM memories
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`,
		memoryID, customerID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("memory not found")
	}
	return nil
}

func (s *Store) SearchMemories(ctx context.Context, customerID, userID string, embedding []float32, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	vector := pgVector(embedding)
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata
		FROM memories
		WHERE customer_id=$1 AND user_id=$2 AND embedding IS NOT NULL
		ORDER BY embedding <-> $3::vector
		LIMIT $4`, customerID, userID, vector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.CustomerID, &m.UserID, &m.SessionID, &m.Type, &m.Content, &m.Metadata); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddPreference(ctx context.Context, customerID, userID, sessionID, category, content string, condition, metadata any) (string, error) {
	if err := s.ensureCustomer(ctx, customerID); err != nil {
		return "", err
	}
	if strings.TrimSpace(category) == "" {
		category = "general"
	}
	conditionJSON, _ := json.Marshal(condition)
	metadataJSON, _ := json.Marshal(metadata)
	var nullableSession any
	if sessionID != "" {
		nullableSession = sessionID
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO preferences(customer_id,user_id,session_id,category,content,condition,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING id::text`, customerID, userID, nullableSession, category, content, conditionJSON, metadataJSON).Scan(&id)
	return id, err
}

func (s *Store) ListPreferences(ctx context.Context, customerID, userID string, limit int) ([]Preference, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata
		FROM preferences
		WHERE customer_id=$1 AND user_id=$2
		ORDER BY updated_at DESC
		LIMIT $3`, customerID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Preference
	for rows.Next() {
		var p Preference
		if err := rows.Scan(&p.ID, &p.CustomerID, &p.UserID, &p.SessionID, &p.Category, &p.Content, &p.Condition, &p.Metadata); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePreference(ctx context.Context, preferenceID, customerID, userID, category, content string, condition, metadata any) error {
	if strings.TrimSpace(category) == "" {
		category = "general"
	}
	conditionJSON, _ := json.Marshal(condition)
	metadataJSON, _ := json.Marshal(metadata)
	tag, err := s.pool.Exec(ctx, `
		UPDATE preferences
		SET category=$4, content=$5, condition=$6, metadata=$7, updated_at=now()
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`,
		preferenceID, customerID, userID, category, content, conditionJSON, metadataJSON)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("preference not found")
	}
	return nil
}

func (s *Store) DeletePreference(ctx context.Context, preferenceID, customerID, userID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM preferences
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`,
		preferenceID, customerID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("preference not found")
	}
	return nil
}

type KnowledgeDocument struct {
	ID         string          `json:"id"`
	CustomerID string          `json:"customer_id"`
	Scope      string          `json:"scope"`
	Title      string          `json:"title"`
	SourceRef  string          `json:"source_ref"`
	Metadata   json.RawMessage `json:"metadata"`
}

func (s *Store) CreateKnowledgeDocument(ctx context.Context, customerID, title, sourceRef string, metadata any) (string, error) {
	return s.CreateKnowledgeDocumentWithScope(ctx, customerID, "customer", title, sourceRef, metadata)
}

func (s *Store) CreateKnowledgeDocumentWithScope(ctx context.Context, customerID, scope, title, sourceRef string, metadata any) (string, error) {
	if err := s.ensureCustomer(ctx, customerID); err != nil {
		return "", err
	}
	scope = normalizeKnowledgeScope(scope)
	b, _ := json.Marshal(metadata)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO knowledge_documents(customer_id,scope,title,source_ref,metadata)
		VALUES($1,$2,$3,$4,$5)
		RETURNING id::text`, customerID, scope, title, sourceRef, b).Scan(&id)
	return id, err
}

func (s *Store) ListKnowledgeDocuments(ctx context.Context, customerID string, limit int) ([]KnowledgeDocument, error) {
	return s.ListKnowledgeDocumentsByScope(ctx, customerID, "", limit)
}

func (s *Store) ListKnowledgeDocumentsByScope(ctx context.Context, customerID, scope string, limit int) ([]KnowledgeDocument, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	scope = strings.TrimSpace(scope)
	filter := `WHERE customer_id=$1`
	args := []any{customerID, limit}
	if scope == "shared" {
		filter = `WHERE scope='shared'`
		args = []any{customerID, limit}
	} else if scope == "customer" {
		filter = `WHERE customer_id=$1 AND scope='customer'`
	} else if scope == "all" {
		filter = `WHERE customer_id=$1 OR scope='shared'`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, scope, title, source_ref, metadata
		FROM knowledge_documents
		`+filter+`
		ORDER BY CASE WHEN customer_id=$1 THEN 0 ELSE 1 END, created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var documents []KnowledgeDocument
	for rows.Next() {
		var document KnowledgeDocument
		if err := rows.Scan(&document.ID, &document.CustomerID, &document.Scope, &document.Title, &document.SourceRef, &document.Metadata); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) DeleteKnowledgeDocument(ctx context.Context, documentID, customerID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM knowledge_documents
		WHERE id=$1 AND customer_id=$2`, documentID, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("knowledge document not found")
	}
	return nil
}

func (s *Store) AddKnowledgeChunk(ctx context.Context, documentID, customerID string, chunkIndex int, content string, metadata any) (string, error) {
	b, _ := json.Marshal(metadata)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO knowledge_chunks(document_id,customer_id,scope,chunk_index,content,metadata)
		VALUES($1,$2,(SELECT scope FROM knowledge_documents WHERE id=$1),$3,$4,$5)
		ON CONFLICT (document_id, chunk_index) DO UPDATE SET content=EXCLUDED.content, metadata=EXCLUDED.metadata, scope=EXCLUDED.scope
		RETURNING id::text`, documentID, customerID, chunkIndex, content, b).Scan(&id)
	return id, err
}

func (s *Store) SetKnowledgeChunkEmbedding(ctx context.Context, chunkID, customerID string, embedding []float32) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE knowledge_chunks
		SET embedding=$3::vector
		WHERE id=$1 AND customer_id=$2`, chunkID, customerID, pgVector(embedding))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("knowledge chunk not found")
	}
	return nil
}

func (s *Store) SetMemoryEmbedding(ctx context.Context, memoryID, customerID, userID string, embedding []float32) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE memories
		SET embedding=$4::vector
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`, memoryID, customerID, userID, pgVector(embedding))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("memory not found")
	}
	return nil
}

func (s *Store) SetPreferenceEmbedding(ctx context.Context, preferenceID, customerID, userID string, embedding []float32) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE preferences
		SET embedding=$4::vector
		WHERE id=$1 AND customer_id=$2 AND user_id=$3`, preferenceID, customerID, userID, pgVector(embedding))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("preference not found")
	}
	return nil
}

type KnowledgeChunk struct {
	ID         string          `json:"id"`
	DocumentID string          `json:"document_id"`
	CustomerID string          `json:"customer_id"`
	Scope      string          `json:"scope"`
	ChunkIndex int             `json:"chunk_index"`
	Content    string          `json:"content"`
	Metadata   json.RawMessage `json:"metadata"`
	Score      float64         `json:"score,omitempty"`
}

func (s *Store) SearchKnowledgeChunks(ctx context.Context, customerID string, embedding []float32, limit int) ([]KnowledgeChunk, error) {
	if limit <= 0 {
		limit = 10
	}
	vector := pgVector(embedding)
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, document_id::text, customer_id, scope, chunk_index, content, metadata
		FROM knowledge_chunks
		WHERE (customer_id=$1 OR scope='shared') AND embedding IS NOT NULL
		ORDER BY embedding <-> $2::vector, CASE WHEN customer_id=$1 THEN 0 ELSE 1 END
		LIMIT $3`, customerID, vector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeChunk
	for rows.Next() {
		var k KnowledgeChunk
		if err := rows.Scan(&k.ID, &k.DocumentID, &k.CustomerID, &k.Scope, &k.ChunkIndex, &k.Content, &k.Metadata); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) SearchKnowledgeHybrid(ctx context.Context, customerID, query string, embedding []float32, limit int) ([]KnowledgeChunk, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	seen := map[string]bool{}
	var out []KnowledgeChunk
	if len(embedding) > 0 {
		vectorChunks, err := s.SearchKnowledgeChunks(ctx, customerID, embedding, limit)
		if err != nil {
			return nil, err
		}
		for i, chunk := range vectorChunks {
			if seen[chunk.ID] {
				continue
			}
			chunk.Score = 1 - float64(i)/float64(limit+1)
			out = append(out, chunk)
			seen[chunk.ID] = true
		}
	}
	if len(out) < limit {
		textChunks, err := s.SearchKnowledgeText(ctx, customerID, query, limit)
		if err != nil {
			return nil, err
		}
		for i, chunk := range textChunks {
			if seen[chunk.ID] {
				continue
			}
			chunk.Score = 0.5 - float64(i)/float64((limit+1)*2)
			out = append(out, chunk)
			seen[chunk.ID] = true
			if len(out) >= limit {
				break
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) SearchKnowledgeText(ctx context.Context, customerID, query string, limit int) ([]KnowledgeChunk, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, document_id::text, customer_id, scope, chunk_index, content, metadata
		FROM knowledge_chunks
		WHERE (customer_id=$1 OR scope='shared')
		AND (
			to_tsvector('simple', content) @@ plainto_tsquery('simple', $2)
			OR content ILIKE '%' || $2 || '%'
		)
		ORDER BY ts_rank_cd(to_tsvector('simple', content), plainto_tsquery('simple', $2)) DESC, CASE WHEN customer_id=$1 THEN 0 ELSE 1 END, chunk_index ASC
		LIMIT $3`, customerID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeChunk
	for rows.Next() {
		var k KnowledgeChunk
		if err := rows.Scan(&k.ID, &k.DocumentID, &k.CustomerID, &k.Scope, &k.ChunkIndex, &k.Content, &k.Metadata); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) ListKnowledgeChunks(ctx context.Context, documentID string, limit int) ([]KnowledgeChunk, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, document_id::text, customer_id, scope, chunk_index, content, metadata
		FROM knowledge_chunks
		WHERE document_id=$1
		ORDER BY chunk_index ASC
		LIMIT $2`, documentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chunks []KnowledgeChunk
	for rows.Next() {
		var chunk KnowledgeChunk
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.CustomerID, &chunk.Scope, &chunk.ChunkIndex, &chunk.Content, &chunk.Metadata); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func normalizeKnowledgeScope(scope string) string {
	switch strings.TrimSpace(scope) {
	case "shared":
		return "shared"
	default:
		return "customer"
	}
}

func pgVector(values []float32) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (s *Store) ensureCustomer(ctx context.Context, customerID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO customers(id) VALUES($1) ON CONFLICT DO NOTHING`, customerID)
	return err
}
