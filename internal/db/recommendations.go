package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type RecommendationItem struct {
	ID          string          `json:"id"`
	CustomerID  string          `json:"customer_id"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags"`
	URL         string          `json:"url"`
	Priority    int             `json:"priority"`
	Sponsored   bool            `json:"sponsored"`
	SponsorName string          `json:"sponsor_name"`
	Status      string          `json:"status"`
	ValidFrom   *time.Time      `json:"valid_from,omitempty"`
	ValidUntil  *time.Time      `json:"valid_until,omitempty"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type RecommendationItemSpec struct {
	CustomerID  string
	Kind        string
	Title       string
	Description string
	Tags        []string
	URL         string
	Priority    int
	Sponsored   bool
	SponsorName string
	Status      string
	ValidFrom   *time.Time
	ValidUntil  *time.Time
	Metadata    any
}

type RecommendationItemUpdate struct {
	Kind        *string
	Title       *string
	Description *string
	Tags        *[]string
	URL         *string
	Priority    *int
	Sponsored   *bool
	SponsorName *string
	Status      *string
	ValidFrom   **time.Time
	ValidUntil  **time.Time
	Metadata    any
}

type RecommendationDecisionSpec struct {
	CustomerID         string
	RunID              string
	UserID             string
	SessionID          string
	ScopeIntent        string
	ContextMode        string
	CandidateItemIDs   []string
	SelectedItemID     string
	RecommendationText string
	Reason             string
	DeliveryStatus     string
	Error              string
	Metadata           any
}

type RecommendationDecision struct {
	ID                 string          `json:"id"`
	CustomerID         string          `json:"customer_id"`
	RunID              *string         `json:"run_id,omitempty"`
	UserID             string          `json:"user_id"`
	SessionID          string          `json:"session_id"`
	ScopeIntent        string          `json:"scope_intent"`
	ContextMode        string          `json:"context_mode"`
	CandidateItemIDs   []string        `json:"candidate_item_ids"`
	SelectedItemID     *string         `json:"selected_item_id,omitempty"`
	RecommendationText string          `json:"recommendation_text"`
	Reason             string          `json:"reason"`
	DeliveryStatus     string          `json:"delivery_status"`
	Error              string          `json:"error"`
	Metadata           json.RawMessage `json:"metadata"`
	CreatedAt          time.Time       `json:"created_at"`
}

type RecommendationJobSpec struct {
	CustomerID            string
	RunID                 string
	UserID                string
	SessionID             string
	ScopeIntent           string
	ContextMode           string
	RecommendationContext string
	OriginalResponse      string
	Config                any
	CandidateItemIDs      []string
}

type RecommendationJob struct {
	ID                    string          `json:"id"`
	CustomerID            string          `json:"customer_id"`
	RunID                 *string         `json:"run_id,omitempty"`
	UserID                string          `json:"user_id"`
	SessionID             string          `json:"session_id"`
	ScopeIntent           string          `json:"scope_intent"`
	ContextMode           string          `json:"context_mode"`
	RecommendationContext string          `json:"recommendation_context,omitempty"`
	OriginalResponse      string          `json:"original_response,omitempty"`
	Config                json.RawMessage `json:"config"`
	CandidateItemIDs      []string        `json:"candidate_item_ids"`
	Status                string          `json:"status"`
	LeaseOwner            string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt        *time.Time      `json:"lease_expires_at,omitempty"`
	LastError             string          `json:"last_error"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

func (s *Store) CreateRecommendationItem(ctx context.Context, spec RecommendationItemSpec) (*RecommendationItem, error) {
	if spec.CustomerID == "" || strings.TrimSpace(spec.Title) == "" {
		return nil, fmt.Errorf("customer_id and title are required")
	}
	if err := s.ensureCustomer(ctx, spec.CustomerID); err != nil {
		return nil, err
	}
	if spec.Status == "" {
		spec.Status = "active"
	}
	tagsJSON := mustJSON(spec.Tags)
	metadata := mustJSON(spec.Metadata)
	var item RecommendationItem
	var tagsRaw []byte
	err := s.pool.QueryRow(ctx, `
		INSERT INTO recommendation_items(customer_id,kind,title,description,tags,url,priority,sponsored,sponsor_name,status,valid_from,valid_until,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id::text, customer_id, kind, title, description, tags, url, priority, sponsored, sponsor_name, status, valid_from, valid_until, metadata, created_at, updated_at`,
		spec.CustomerID, defaultString(spec.Kind, "activity"), strings.TrimSpace(spec.Title), spec.Description, tagsJSON, spec.URL, spec.Priority, spec.Sponsored, spec.SponsorName, spec.Status, spec.ValidFrom, spec.ValidUntil, metadata).
		Scan(&item.ID, &item.CustomerID, &item.Kind, &item.Title, &item.Description, &tagsRaw, &item.URL, &item.Priority, &item.Sponsored, &item.SponsorName, &item.Status, &item.ValidFrom, &item.ValidUntil, &item.Metadata, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.Tags = decodeStringSlice(tagsRaw)
	return &item, nil
}

func (s *Store) ListRecommendationItems(ctx context.Context, customerID, status string, limit int) ([]RecommendationItem, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	statusFilter := ""
	if status != "" {
		args = append(args, status)
		statusFilter = " AND status=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, kind, title, description, tags, url, priority, sponsored, sponsor_name, status, valid_from, valid_until, metadata, created_at, updated_at
		FROM recommendation_items
		WHERE customer_id=$1`+statusFilter+`
		ORDER BY priority DESC, created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	return scanRecommendationItems(rows)
}

func (s *Store) SearchRecommendationItems(ctx context.Context, customerID, query string, allowSponsored bool, limit int) ([]RecommendationItem, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	args := []any{customerID, limit, allowSponsored}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, kind, title, description, tags, url, priority, sponsored, sponsor_name, status, valid_from, valid_until, metadata, created_at, updated_at
		FROM recommendation_items
		WHERE customer_id=$1
		  AND status='active'
		  AND ($3 OR sponsored=false)
		  AND (valid_from IS NULL OR valid_from <= now())
		  AND (valid_until IS NULL OR valid_until >= now())
		ORDER BY priority DESC, created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	return scanRecommendationItems(rows)
}

func (s *Store) UpdateRecommendationItem(ctx context.Context, itemID, customerID string, update RecommendationItemUpdate) (*RecommendationItem, error) {
	if itemID == "" || customerID == "" {
		return nil, fmt.Errorf("item_id and customer_id are required")
	}
	var current RecommendationItem
	var currentTags []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, customer_id, kind, title, description, tags, url, priority, sponsored, sponsor_name, status, valid_from, valid_until, metadata, created_at, updated_at
		FROM recommendation_items
		WHERE id=$1 AND customer_id=$2`, itemID, customerID).
		Scan(&current.ID, &current.CustomerID, &current.Kind, &current.Title, &current.Description, &currentTags, &current.URL, &current.Priority, &current.Sponsored, &current.SponsorName, &current.Status, &current.ValidFrom, &current.ValidUntil, &current.Metadata, &current.CreatedAt, &current.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("recommendation item not found")
	}
	if err != nil {
		return nil, err
	}
	current.Tags = decodeStringSlice(currentTags)
	if update.Kind != nil {
		current.Kind = *update.Kind
	}
	if update.Title != nil {
		current.Title = *update.Title
	}
	if update.Description != nil {
		current.Description = *update.Description
	}
	if update.Tags != nil {
		current.Tags = *update.Tags
	}
	if update.URL != nil {
		current.URL = *update.URL
	}
	if update.Priority != nil {
		current.Priority = *update.Priority
	}
	if update.Sponsored != nil {
		current.Sponsored = *update.Sponsored
	}
	if update.SponsorName != nil {
		current.SponsorName = *update.SponsorName
	}
	if update.Status != nil {
		current.Status = *update.Status
	}
	if update.ValidFrom != nil {
		current.ValidFrom = *update.ValidFrom
	}
	if update.ValidUntil != nil {
		current.ValidUntil = *update.ValidUntil
	}
	metadata := current.Metadata
	if update.Metadata != nil {
		metadata = mustJSON(update.Metadata)
	}
	tagsJSON := mustJSON(current.Tags)
	var item RecommendationItem
	var tagsRaw []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE recommendation_items
		SET kind=$3,title=$4,description=$5,tags=$6,url=$7,priority=$8,sponsored=$9,sponsor_name=$10,status=$11,valid_from=$12,valid_until=$13,metadata=$14,updated_at=now()
		WHERE id=$1 AND customer_id=$2
		RETURNING id::text, customer_id, kind, title, description, tags, url, priority, sponsored, sponsor_name, status, valid_from, valid_until, metadata, created_at, updated_at`,
		itemID, customerID, current.Kind, current.Title, current.Description, tagsJSON, current.URL, current.Priority, current.Sponsored, current.SponsorName, current.Status, current.ValidFrom, current.ValidUntil, metadata).
		Scan(&item.ID, &item.CustomerID, &item.Kind, &item.Title, &item.Description, &tagsRaw, &item.URL, &item.Priority, &item.Sponsored, &item.SponsorName, &item.Status, &item.ValidFrom, &item.ValidUntil, &item.Metadata, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.Tags = decodeStringSlice(tagsRaw)
	return &item, nil
}

func (s *Store) DeleteRecommendationItem(ctx context.Context, itemID, customerID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM recommendation_items WHERE id=$1 AND customer_id=$2`, itemID, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("recommendation item not found")
	}
	return nil
}

func (s *Store) RecordRecommendationDecision(ctx context.Context, spec RecommendationDecisionSpec) (*RecommendationDecision, error) {
	if spec.CustomerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	runID := nullableText(spec.RunID)
	selectedID := nullableText(spec.SelectedItemID)
	if spec.DeliveryStatus == "" {
		spec.DeliveryStatus = "none"
	}
	candidates := mustJSON(spec.CandidateItemIDs)
	metadata := mustJSON(spec.Metadata)
	var out RecommendationDecision
	var candidateRaw []byte
	err := s.pool.QueryRow(ctx, `
		INSERT INTO recommendation_decisions(customer_id,run_id,user_id,session_id,scope_intent,context_mode,candidate_item_ids,selected_item_id,recommendation_text,reason,delivery_status,error,metadata)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id::text, customer_id, run_id::text, user_id, session_id, scope_intent, context_mode, candidate_item_ids, selected_item_id::text, recommendation_text, reason, delivery_status, error, metadata, created_at`,
		spec.CustomerID, runID, spec.UserID, spec.SessionID, spec.ScopeIntent, spec.ContextMode, candidates, selectedID, spec.RecommendationText, spec.Reason, spec.DeliveryStatus, spec.Error, metadata).
		Scan(&out.ID, &out.CustomerID, &out.RunID, &out.UserID, &out.SessionID, &out.ScopeIntent, &out.ContextMode, &candidateRaw, &out.SelectedItemID, &out.RecommendationText, &out.Reason, &out.DeliveryStatus, &out.Error, &out.Metadata, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	out.CandidateItemIDs = decodeStringSlice(candidateRaw)
	return &out, nil
}

func (s *Store) ListRecommendationDecisions(ctx context.Context, customerID, runID string, limit int) ([]RecommendationDecision, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	runFilter := ""
	if runID != "" {
		args = append(args, runID)
		runFilter = " AND run_id=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, run_id::text, user_id, session_id, scope_intent, context_mode, candidate_item_ids, selected_item_id::text, recommendation_text, reason, delivery_status, error, metadata, created_at
		FROM recommendation_decisions
		WHERE customer_id=$1`+runFilter+`
		ORDER BY created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecommendationDecision
	for rows.Next() {
		var d RecommendationDecision
		var candidates []byte
		if err := rows.Scan(&d.ID, &d.CustomerID, &d.RunID, &d.UserID, &d.SessionID, &d.ScopeIntent, &d.ContextMode, &candidates, &d.SelectedItemID, &d.RecommendationText, &d.Reason, &d.DeliveryStatus, &d.Error, &d.Metadata, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.CandidateItemIDs = decodeStringSlice(candidates)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateRecommendationJob(ctx context.Context, spec RecommendationJobSpec) (*RecommendationJob, error) {
	if spec.CustomerID == "" || spec.UserID == "" || spec.SessionID == "" {
		return nil, fmt.Errorf("customer_id, user_id, and session_id are required")
	}
	config := mustJSON(spec.Config)
	candidates := mustJSON(spec.CandidateItemIDs)
	var out RecommendationJob
	var candidateRaw []byte
	err := s.pool.QueryRow(ctx, `
		INSERT INTO recommendation_jobs(customer_id,run_id,user_id,session_id,scope_intent,context_mode,recommendation_context,original_response,config,candidate_item_ids,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued')
		RETURNING id::text, customer_id, run_id::text, user_id, session_id, scope_intent, context_mode, recommendation_context, original_response, config, candidate_item_ids, status, lease_owner, lease_expires_at, last_error, created_at, updated_at`,
		spec.CustomerID, nullableText(spec.RunID), spec.UserID, spec.SessionID, spec.ScopeIntent, spec.ContextMode, spec.RecommendationContext, spec.OriginalResponse, config, candidates).
		Scan(&out.ID, &out.CustomerID, &out.RunID, &out.UserID, &out.SessionID, &out.ScopeIntent, &out.ContextMode, &out.RecommendationContext, &out.OriginalResponse, &out.Config, &candidateRaw, &out.Status, &out.LeaseOwner, &out.LeaseExpiresAt, &out.LastError, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	out.CandidateItemIDs = decodeStringSlice(candidateRaw)
	return &out, nil
}

func (s *Store) ClaimRecommendationJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]RecommendationJob, error) {
	if owner == "" {
		owner = "duraclaw-worker"
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if leaseFor <= 0 {
		leaseFor = 2 * time.Minute
	}
	rows, err := s.pool.Query(ctx, `
		UPDATE recommendation_jobs
		SET status='leased', lease_owner=$1, lease_expires_at=now()+$2::interval, updated_at=now()
		WHERE id IN (
			SELECT id FROM recommendation_jobs
			WHERE status='queued' OR (status='leased' AND lease_expires_at < now())
			ORDER BY created_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, customer_id, run_id::text, user_id, session_id, scope_intent, context_mode, recommendation_context, original_response, config, candidate_item_ids, status, lease_owner, lease_expires_at, last_error, created_at, updated_at`,
		owner, fmt.Sprintf("%f seconds", leaseFor.Seconds()), limit)
	if err != nil {
		return nil, err
	}
	return scanRecommendationJobs(rows)
}

func (s *Store) CompleteRecommendationJob(ctx context.Context, id, status, errText string) error {
	if id == "" {
		return fmt.Errorf("job id is required")
	}
	if status == "" {
		status = "sent"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE recommendation_jobs
		SET status=$2, last_error=$3, lease_owner='', lease_expires_at=NULL, updated_at=now()
		WHERE id=$1`, id, status, errText)
	return err
}

func (s *Store) ListRecommendationJobs(ctx context.Context, customerID, status string, limit int) ([]RecommendationJob, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{customerID, limit}
	statusFilter := ""
	if status != "" {
		args = append(args, status)
		statusFilter = " AND status=$3"
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, customer_id, run_id::text, user_id, session_id, scope_intent, context_mode, recommendation_context, original_response, config, candidate_item_ids, status, lease_owner, lease_expires_at, last_error, created_at, updated_at
		FROM recommendation_jobs
		WHERE customer_id=$1`+statusFilter+`
		ORDER BY created_at DESC
		LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	return scanRecommendationJobs(rows)
}

func scanRecommendationItems(rows pgx.Rows) ([]RecommendationItem, error) {
	defer rows.Close()
	var out []RecommendationItem
	for rows.Next() {
		var item RecommendationItem
		var tagsRaw []byte
		if err := rows.Scan(&item.ID, &item.CustomerID, &item.Kind, &item.Title, &item.Description, &tagsRaw, &item.URL, &item.Priority, &item.Sponsored, &item.SponsorName, &item.Status, &item.ValidFrom, &item.ValidUntil, &item.Metadata, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Tags = decodeStringSlice(tagsRaw)
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanRecommendationJobs(rows pgx.Rows) ([]RecommendationJob, error) {
	defer rows.Close()
	var out []RecommendationJob
	for rows.Next() {
		var job RecommendationJob
		var candidates []byte
		if err := rows.Scan(&job.ID, &job.CustomerID, &job.RunID, &job.UserID, &job.SessionID, &job.ScopeIntent, &job.ContextMode, &job.RecommendationContext, &job.OriginalResponse, &job.Config, &candidates, &job.Status, &job.LeaseOwner, &job.LeaseExpiresAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		job.CandidateItemIDs = decodeStringSlice(candidates)
		out = append(out, job)
	}
	return out, rows.Err()
}

func decodeStringSlice(raw []byte) []string {
	var values []string
	_ = json.Unmarshal(raw, &values)
	if values == nil {
		return []string{}
	}
	return values
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func nullableText(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
