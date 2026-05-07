package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
)

type Memory struct {
	ID             string          `json:"id"`
	CustomerID     string          `json:"customer_id"`
	UserID         string          `json:"user_id"`
	SessionID      *string         `json:"session_id,omitempty"`
	Type           string          `json:"memory_type"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	UpdatedAt      *time.Time      `json:"updated_at,omitempty"`
	LastUsedAt     *time.Time      `json:"last_used_at,omitempty"`
	UsageCount     int             `json:"usage_count,omitempty"`
	RelevanceScore float64         `json:"relevance_score,omitempty"`
}

type Preference struct {
	ID             string          `json:"id"`
	CustomerID     string          `json:"customer_id"`
	UserID         string          `json:"user_id"`
	SessionID      *string         `json:"session_id,omitempty"`
	Category       string          `json:"category"`
	Content        string          `json:"content"`
	Condition      json.RawMessage `json:"condition"`
	Metadata       json.RawMessage `json:"metadata"`
	UpdatedAt      *time.Time      `json:"updated_at,omitempty"`
	LastUsedAt     *time.Time      `json:"last_used_at,omitempty"`
	UsageCount     int             `json:"usage_count,omitempty"`
	RelevanceScore float64         `json:"relevance_score,omitempty"`
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
		SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count
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
		m, err := scanMemoryWithUsage(rows)
		if err != nil {
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
		SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count
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
		m, err := scanMemoryWithUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RankMemories(ctx context.Context, customerID, userID, query string, embedding []float32, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 8
	}
	candidateLimit := candidateLimitForRank(limit)
	rows, err := s.rankMemoryRows(ctx, customerID, userID, query, embedding, candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []rankedMemoryCandidate
	for rows.Next() {
		candidate, err := scanRankedMemory(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rankMemoryCandidates(candidates, query, len(embedding) > 0)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Memory.RelevanceScore == candidates[j].Memory.RelevanceScore {
			if candidates[i].Memory.UsageCount == candidates[j].Memory.UsageCount {
				return timeAfter(candidates[i].Memory.UpdatedAt, candidates[j].Memory.UpdatedAt)
			}
			return candidates[i].Memory.UsageCount > candidates[j].Memory.UsageCount
		}
		return candidates[i].Memory.RelevanceScore > candidates[j].Memory.RelevanceScore
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]Memory, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Memory)
	}
	return out, nil
}

func (s *Store) rankMemoryRows(ctx context.Context, customerID, userID, query string, embedding []float32, limit int) (pgx.Rows, error) {
	if len(embedding) == 0 {
		return s.pool.Query(ctx, `
			WITH text_candidates AS (
				SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count, NULL::double precision AS vector_distance
				FROM memories
				WHERE customer_id=$1 AND user_id=$2
				  AND ($3::text[] IS NULL OR content ILIKE ANY($3::text[]) OR memory_type ILIKE ANY($3::text[]))
				ORDER BY usage_count DESC, updated_at DESC
				LIMIT $4
			), usage_candidates AS (
				SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count, NULL::double precision AS vector_distance
				FROM memories
				WHERE customer_id=$1 AND user_id=$2
				ORDER BY usage_count DESC, updated_at DESC
				LIMIT $4
			)
			SELECT DISTINCT ON (id) id, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count, vector_distance
			FROM (
				SELECT * FROM text_candidates
				UNION ALL SELECT * FROM usage_candidates
			) candidates
			ORDER BY id`, customerID, userID, textSearchPatterns(query), limit)
	}
	return s.pool.Query(ctx, `
		WITH vector_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count,
			       embedding <-> $3::vector AS vector_distance
			FROM memories
			WHERE customer_id=$1 AND user_id=$2 AND embedding IS NOT NULL
			ORDER BY embedding <-> $3::vector
			LIMIT $4
		), text_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count,
			       CASE WHEN embedding IS NULL THEN NULL ELSE embedding <-> $3::vector END AS vector_distance
			FROM memories
			WHERE customer_id=$1 AND user_id=$2
			  AND ($5::text[] IS NULL OR content ILIKE ANY($5::text[]) OR memory_type ILIKE ANY($5::text[]))
			ORDER BY usage_count DESC, updated_at DESC
			LIMIT $4
		), usage_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count,
			       CASE WHEN embedding IS NULL THEN NULL ELSE embedding <-> $3::vector END AS vector_distance
			FROM memories
			WHERE customer_id=$1 AND user_id=$2
			ORDER BY usage_count DESC, updated_at DESC
			LIMIT $4
		)
		SELECT DISTINCT ON (id) id, customer_id, user_id, session_id, memory_type, content, metadata, updated_at, last_used_at, usage_count, vector_distance
		FROM (
			SELECT * FROM vector_candidates
			UNION ALL SELECT * FROM text_candidates
			UNION ALL SELECT * FROM usage_candidates
		) candidates
		ORDER BY id, vector_distance NULLS LAST`, customerID, userID, pgVector(embedding), limit, textSearchPatterns(query))
}

func (s *Store) MarkMemoriesUsed(ctx context.Context, customerID, userID string, ids []string) error {
	ids = nonEmptyIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE memories
		SET usage_count=usage_count+1, last_used_at=now()
		WHERE customer_id=$1 AND user_id=$2 AND id::text = ANY($3)`, customerID, userID, ids)
	return err
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
		SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count
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
		p, err := scanPreferenceWithUsage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RankPreferences(ctx context.Context, customerID, userID, query string, embedding []float32, limit int) ([]Preference, error) {
	if limit <= 0 {
		limit = 20
	}
	candidateLimit := candidateLimitForRank(limit)
	rows, err := s.rankPreferenceRows(ctx, customerID, userID, query, embedding, candidateLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []rankedPreferenceCandidate
	for rows.Next() {
		candidate, err := scanRankedPreference(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rankPreferenceCandidates(candidates, query, len(embedding) > 0)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Preference.RelevanceScore == candidates[j].Preference.RelevanceScore {
			if candidates[i].Preference.UsageCount == candidates[j].Preference.UsageCount {
				return timeAfter(candidates[i].Preference.UpdatedAt, candidates[j].Preference.UpdatedAt)
			}
			return candidates[i].Preference.UsageCount > candidates[j].Preference.UsageCount
		}
		return candidates[i].Preference.RelevanceScore > candidates[j].Preference.RelevanceScore
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]Preference, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.Preference)
	}
	return out, nil
}

func (s *Store) rankPreferenceRows(ctx context.Context, customerID, userID, query string, embedding []float32, limit int) (pgx.Rows, error) {
	if len(embedding) == 0 {
		return s.pool.Query(ctx, `
			WITH text_candidates AS (
				SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count, NULL::double precision AS vector_distance
				FROM preferences
				WHERE customer_id=$1 AND user_id=$2
				  AND ($3::text[] IS NULL OR content ILIKE ANY($3::text[]) OR category ILIKE ANY($3::text[]))
				ORDER BY usage_count DESC, updated_at DESC
				LIMIT $4
			), usage_candidates AS (
				SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count, NULL::double precision AS vector_distance
				FROM preferences
				WHERE customer_id=$1 AND user_id=$2
				ORDER BY usage_count DESC, updated_at DESC
				LIMIT $4
			)
			SELECT DISTINCT ON (id) id, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count, vector_distance
			FROM (
				SELECT * FROM text_candidates
				UNION ALL SELECT * FROM usage_candidates
			) candidates
			ORDER BY id`, customerID, userID, textSearchPatterns(query), limit)
	}
	return s.pool.Query(ctx, `
		WITH vector_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count,
			       embedding <-> $3::vector AS vector_distance
			FROM preferences
			WHERE customer_id=$1 AND user_id=$2 AND embedding IS NOT NULL
			ORDER BY embedding <-> $3::vector
			LIMIT $4
		), text_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count,
			       CASE WHEN embedding IS NULL THEN NULL ELSE embedding <-> $3::vector END AS vector_distance
			FROM preferences
			WHERE customer_id=$1 AND user_id=$2
			  AND ($5::text[] IS NULL OR content ILIKE ANY($5::text[]) OR category ILIKE ANY($5::text[]))
			ORDER BY usage_count DESC, updated_at DESC
			LIMIT $4
		), usage_candidates AS (
			SELECT id::text, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count,
			       CASE WHEN embedding IS NULL THEN NULL ELSE embedding <-> $3::vector END AS vector_distance
			FROM preferences
			WHERE customer_id=$1 AND user_id=$2
			ORDER BY usage_count DESC, updated_at DESC
			LIMIT $4
		)
		SELECT DISTINCT ON (id) id, customer_id, user_id, session_id, category, content, condition, metadata, updated_at, last_used_at, usage_count, vector_distance
		FROM (
			SELECT * FROM vector_candidates
			UNION ALL SELECT * FROM text_candidates
			UNION ALL SELECT * FROM usage_candidates
		) candidates
		ORDER BY id, vector_distance NULLS LAST`, customerID, userID, pgVector(embedding), limit, textSearchPatterns(query))
}

func (s *Store) MarkPreferencesUsed(ctx context.Context, customerID, userID string, ids []string) error {
	ids = nonEmptyIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE preferences
		SET usage_count=usage_count+1, last_used_at=now()
		WHERE customer_id=$1 AND user_id=$2 AND id::text = ANY($3)`, customerID, userID, ids)
	return err
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

type rankedMemoryCandidate struct {
	Memory         Memory
	VectorDistance sql.NullFloat64
}

type rankedPreferenceCandidate struct {
	Preference     Preference
	VectorDistance sql.NullFloat64
}

func scanMemoryWithUsage(rows pgx.Rows) (Memory, error) {
	var m Memory
	var updated sql.NullTime
	var lastUsed sql.NullTime
	if err := rows.Scan(&m.ID, &m.CustomerID, &m.UserID, &m.SessionID, &m.Type, &m.Content, &m.Metadata, &updated, &lastUsed, &m.UsageCount); err != nil {
		return m, err
	}
	if updated.Valid {
		m.UpdatedAt = &updated.Time
	}
	if lastUsed.Valid {
		m.LastUsedAt = &lastUsed.Time
	}
	return m, nil
}

func scanPreferenceWithUsage(rows pgx.Rows) (Preference, error) {
	var p Preference
	var updated sql.NullTime
	var lastUsed sql.NullTime
	if err := rows.Scan(&p.ID, &p.CustomerID, &p.UserID, &p.SessionID, &p.Category, &p.Content, &p.Condition, &p.Metadata, &updated, &lastUsed, &p.UsageCount); err != nil {
		return p, err
	}
	if updated.Valid {
		p.UpdatedAt = &updated.Time
	}
	if lastUsed.Valid {
		p.LastUsedAt = &lastUsed.Time
	}
	return p, nil
}

func scanRankedMemory(rows pgx.Rows) (rankedMemoryCandidate, error) {
	var candidate rankedMemoryCandidate
	var updated sql.NullTime
	var lastUsed sql.NullTime
	if err := rows.Scan(
		&candidate.Memory.ID,
		&candidate.Memory.CustomerID,
		&candidate.Memory.UserID,
		&candidate.Memory.SessionID,
		&candidate.Memory.Type,
		&candidate.Memory.Content,
		&candidate.Memory.Metadata,
		&updated,
		&lastUsed,
		&candidate.Memory.UsageCount,
		&candidate.VectorDistance,
	); err != nil {
		return candidate, err
	}
	if lastUsed.Valid {
		candidate.Memory.LastUsedAt = &lastUsed.Time
	}
	if updated.Valid {
		candidate.Memory.UpdatedAt = &updated.Time
	}
	return candidate, nil
}

func scanRankedPreference(rows pgx.Rows) (rankedPreferenceCandidate, error) {
	var candidate rankedPreferenceCandidate
	var updated sql.NullTime
	var lastUsed sql.NullTime
	if err := rows.Scan(
		&candidate.Preference.ID,
		&candidate.Preference.CustomerID,
		&candidate.Preference.UserID,
		&candidate.Preference.SessionID,
		&candidate.Preference.Category,
		&candidate.Preference.Content,
		&candidate.Preference.Condition,
		&candidate.Preference.Metadata,
		&updated,
		&lastUsed,
		&candidate.Preference.UsageCount,
		&candidate.VectorDistance,
	); err != nil {
		return candidate, err
	}
	if lastUsed.Valid {
		candidate.Preference.LastUsedAt = &lastUsed.Time
	}
	if updated.Valid {
		candidate.Preference.UpdatedAt = &updated.Time
	}
	return candidate, nil
}

func rankMemoryCandidates(candidates []rankedMemoryCandidate, query string, hasEmbedding bool) {
	for i := range candidates {
		score := relevanceScore(candidates[i].Memory.Type+" "+candidates[i].Memory.Content, candidates[i].Memory.Metadata, query, candidates[i].VectorDistance, candidates[i].Memory.UpdatedAt, candidates[i].Memory.UsageCount, hasEmbedding)
		candidates[i].Memory.RelevanceScore = score
	}
}

func rankPreferenceCandidates(candidates []rankedPreferenceCandidate, query string, hasEmbedding bool) {
	for i := range candidates {
		score := relevanceScore(candidates[i].Preference.Category+" "+candidates[i].Preference.Content, candidates[i].Preference.Metadata, query, candidates[i].VectorDistance, candidates[i].Preference.UpdatedAt, candidates[i].Preference.UsageCount, hasEmbedding)
		candidates[i].Preference.RelevanceScore = score
	}
}

func relevanceScore(content string, metadata json.RawMessage, query string, distance sql.NullFloat64, updatedAt *time.Time, usageCount int, hasEmbedding bool) float64 {
	text := tokenOverlapScore(query, content)
	vector := 0.0
	if distance.Valid && distance.Float64 >= 0 {
		vector = 1 / (1 + distance.Float64)
	}
	frequency := math.Min(1, math.Log1p(float64(maxInt(usageCount, 0)))/math.Log1p(20))
	recency := recencyScore(updatedAt)
	priority := explicitPriorityScore(metadata)
	if hasEmbedding && distance.Valid {
		return 0.62*vector + 0.22*text + 0.08*frequency + 0.05*recency + 0.03*priority
	}
	return 0.62*text + 0.18*frequency + 0.15*recency + 0.05*priority
}

func tokenOverlapScore(query, content string) float64 {
	queryTokens := tokenSet(query)
	if len(queryTokens) == 0 {
		return 0
	}
	contentTokens := tokenSet(content)
	if len(contentTokens) == 0 {
		return 0
	}
	matches := 0
	for token := range queryTokens {
		if contentTokens[token] {
			matches++
		}
	}
	return float64(matches) / math.Sqrt(float64(len(queryTokens)*len(contentTokens)))
}

func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	var b strings.Builder
	flush := func() {
		if b.Len() < 2 {
			b.Reset()
			return
		}
		token := b.String()
		if !rankStopWords[token] {
			out[token] = true
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

func textSearchPatterns(query string) []string {
	tokens := tokenSet(query)
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, 0, len(tokens))
	for token := range tokens {
		if strings.ContainsAny(token, `%_\`) {
			continue
		}
		out = append(out, "%"+token+"%")
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

var rankStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "you": true, "your": true,
	"dan": true, "yang": true, "untuk": true, "saya": true, "aku": true, "kamu": true, "ini": true, "itu": true,
}

func recencyScore(updatedAt *time.Time) float64 {
	if updatedAt == nil || updatedAt.IsZero() {
		return 0
	}
	age := time.Since(*updatedAt)
	if age <= 0 {
		return 1
	}
	return 1 / (1 + age.Hours()/168)
}

func timeAfter(left, right *time.Time) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}
	return left.After(*right)
}

func explicitPriorityScore(metadata json.RawMessage) float64 {
	var payload map[string]any
	if len(metadata) == 0 || json.Unmarshal(metadata, &payload) != nil {
		return 0
	}
	switch raw := payload["priority"].(type) {
	case float64:
		return math.Max(0, math.Min(1, raw))
	case int:
		return math.Max(0, math.Min(1, float64(raw)))
	case string:
		if strings.EqualFold(raw, "high") {
			return 1
		}
	}
	return 0
}

func candidateLimitForRank(limit int) int {
	if limit <= 0 {
		limit = 8
	}
	if limit < 25 {
		limit *= 8
	}
	if limit < 50 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func nonEmptyIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
