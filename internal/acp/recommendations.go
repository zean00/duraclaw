package acp

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"duraclaw/internal/db"
)

func (h *Handler) createRecommendationItem(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID  string         `json:"customer_id"`
		Kind        string         `json:"kind"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Tags        []string       `json:"tags"`
		URL         string         `json:"url"`
		Priority    int            `json:"priority"`
		Sponsored   bool           `json:"sponsored"`
		SponsorName string         `json:"sponsor_name"`
		Status      string         `json:"status"`
		ValidFrom   *time.Time     `json:"valid_from"`
		ValidUntil  *time.Time     `json:"valid_until"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := h.store.CreateRecommendationItem(r.Context(), db.RecommendationItemSpec{
		CustomerID: payload.CustomerID, Kind: payload.Kind, Title: payload.Title, Description: payload.Description,
		Tags: payload.Tags, URL: payload.URL, Priority: payload.Priority, Sponsored: payload.Sponsored,
		SponsorName: payload.SponsorName, Status: payload.Status, ValidFrom: payload.ValidFrom,
		ValidUntil: payload.ValidUntil, Metadata: payload.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *Handler) listRecommendationItems(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.store.ListRecommendationItems(r.Context(), customerID, r.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) updateRecommendationItem(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		CustomerID  string         `json:"customer_id"`
		Kind        *string        `json:"kind"`
		Title       *string        `json:"title"`
		Description *string        `json:"description"`
		Tags        *[]string      `json:"tags"`
		URL         *string        `json:"url"`
		Priority    *int           `json:"priority"`
		Sponsored   *bool          `json:"sponsored"`
		SponsorName *string        `json:"sponsor_name"`
		Status      *string        `json:"status"`
		ValidFrom   **time.Time    `json:"valid_from"`
		ValidUntil  **time.Time    `json:"valid_until"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := decodeJSON(w, r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if payload.CustomerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	item, err := h.store.UpdateRecommendationItem(r.Context(), r.PathValue("item_id"), payload.CustomerID, db.RecommendationItemUpdate{
		Kind: payload.Kind, Title: payload.Title, Description: payload.Description, Tags: payload.Tags,
		URL: payload.URL, Priority: payload.Priority, Sponsored: payload.Sponsored, SponsorName: payload.SponsorName,
		Status: payload.Status, ValidFrom: payload.ValidFrom, ValidUntil: payload.ValidUntil, Metadata: payload.Metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *Handler) deleteRecommendationItem(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	if customerID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("customer_id is required"))
		return
	}
	if err := h.store.DeleteRecommendationItem(r.Context(), r.PathValue("item_id"), customerID); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listRecommendationDecisions(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	decisions, err := h.store.ListRecommendationDecisions(r.Context(), customerID, r.URL.Query().Get("run_id"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

func (h *Handler) listRecommendationJobs(w http.ResponseWriter, r *http.Request) {
	customerID := r.URL.Query().Get("customer_id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := h.store.ListRecommendationJobs(r.Context(), customerID, r.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}
