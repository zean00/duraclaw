package profiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Request struct {
	CustomerID      string `json:"customer_id"`
	UserID          string `json:"user_id"`
	AgentInstanceID string `json:"agent_instance_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
}

type Result struct {
	Profile  map[string]any `json:"profile"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type Retriever interface {
	RetrieveProfile(ctx context.Context, req Request) (*Result, error)
}

type HTTPRetriever struct {
	URL     string
	Token   string
	Headers map[string]string
	Client  *http.Client
	Timeout time.Duration
}

func (r HTTPRetriever) RetrieveProfile(ctx context.Context, req Request) (*Result, error) {
	if strings.TrimSpace(r.URL) == "" {
		return nil, nil
	}
	payload, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Customer-ID", req.CustomerID)
	httpReq.Header.Set("X-User-ID", req.UserID)
	httpReq.Header.Set("X-Agent-Instance-ID", req.AgentInstanceID)
	httpReq.Header.Set("X-Session-ID", req.SessionID)
	if r.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+r.Token)
	}
	for key, value := range r.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			httpReq.Header.Set(key, value)
		}
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("profile retriever returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out Result
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if out.Profile == nil {
		return nil, nil
	}
	return &out, nil
}

func (r HTTPRetriever) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 5 * time.Second
}

func MergeMetadata(existing map[string]any, result *Result, source string, now time.Time) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	if result == nil || len(result.Profile) == 0 {
		return existing
	}
	existing["profile"] = result.Profile
	sourceMeta := map[string]any{
		"provider":     source,
		"retrieved_at": now.UTC().Format(time.RFC3339),
	}
	for key, value := range result.Metadata {
		sourceMeta[key] = value
	}
	existing["profile_source"] = sourceMeta
	return existing
}

func AllowedProfile(metadata map[string]any, allowed []string) map[string]any {
	raw, ok := metadata["profile"].(map[string]any)
	if !ok || len(raw) == 0 || len(allowed) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, field := range allowed {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if value, ok := raw[field]; ok {
			out[field] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
