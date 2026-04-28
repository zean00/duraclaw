package artifacts

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

const (
	defaultProcessorTimeout          = 60 * time.Second
	defaultProcessorMaxResponseBytes = 1 << 20
	defaultProcessorMaxSummaryBytes  = 64 << 10
	defaultProcessorMaxReps          = 16
)

type HTTPProcessor struct {
	NameValue          string
	BaseURL            string
	Token              string
	Modalities         map[string]bool
	MediaTypes         map[string]bool
	HTTPClient         *http.Client
	Timeout            time.Duration
	MaxResponseBytes   int
	MaxRepresentations int
	MaxSummaryBytes    int
	RawMediaAllowed    bool
	DegradeOnOversize  bool
	RequestedReps      []string
	MaxRetries         int
	RetryNonIdempotent bool
}

func (p HTTPProcessor) Name() string {
	if p.NameValue == "" {
		return "http_processor"
	}
	return p.NameValue
}

func (p HTTPProcessor) CanProcess(a Artifact) bool {
	if len(p.Modalities) > 0 && !p.Modalities[a.Modality] {
		return false
	}
	if len(p.MediaTypes) > 0 && !p.MediaTypes[a.MediaType] {
		return false
	}
	return strings.TrimSpace(p.BaseURL) != ""
}

func (p HTTPProcessor) Process(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, error) {
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, fmt.Errorf("artifact processor base url is required")
	}
	limits := p.limits()
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: p.timeout()}
	}
	exec.ArtifactID = a.ID
	if len(exec.RequestedRepresentations) == 0 {
		exec.RequestedRepresentations = append([]string(nil), p.RequestedReps...)
	}
	body, _ := json.Marshal(map[string]any{"context": exec, "artifact": a, "processor_call_id": exec.ProcessorCallID, "requested_representations": exec.RequestedRepresentations, "limits": limits})
	attempts := p.MaxRetries + 1
	if !p.RetryNonIdempotent || attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/artifacts/process", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		p.applyHeaders(req, exec)
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		reps, err := p.decodeResponse(resp, limits)
		_ = resp.Body.Close()
		if err == nil {
			return reps, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (p HTTPProcessor) applyHeaders(req *http.Request, exec ProcessorContext) {
	req.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	req.Header.Set("X-Customer-ID", exec.CustomerID)
	req.Header.Set("X-User-ID", exec.UserID)
	req.Header.Set("X-Agent-Instance-ID", exec.AgentInstanceID)
	req.Header.Set("X-Session-ID", exec.SessionID)
	req.Header.Set("X-Run-ID", exec.RunID)
	req.Header.Set("X-Artifact-ID", exec.ArtifactID)
	req.Header.Set("X-Processor-Call-ID", exec.ProcessorCallID)
	req.Header.Set("X-Request-ID", exec.RequestID)
	if exec.TraceParent != "" {
		req.Header.Set("traceparent", exec.TraceParent)
	}
}

func (p HTTPProcessor) decodeResponse(resp *http.Response, limits ProcessorLimits) ([]Representation, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("artifact processor failed with status %d", resp.StatusCode)
	}
	limit := int64(limits.MaxResponseBytes)
	if limit <= 0 {
		limit = defaultProcessorMaxResponseBytes
	}
	reader := io.LimitReader(resp.Body, limit+1)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		if limits.DegradeOnOversize {
			return []Representation{{Type: "metadata_summary", Summary: "Artifact processor response exceeded configured size limit.", Metadata: map[string]any{"degraded": true, "max_response_bytes": limit}}}, nil
		}
		return nil, fmt.Errorf("artifact processor response exceeds %d bytes", limit)
	}
	var payload struct {
		Representations []Representation `json:"representations"`
		Degraded        bool             `json:"degraded"`
		Metadata        map[string]any   `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.Degraded && len(payload.Representations) == 0 {
		payload.Representations = []Representation{{Type: "metadata_summary", Summary: "Artifact processor returned a degraded result.", Metadata: payload.Metadata}}
	}
	return ValidateRepresentations(payload.Representations, limits)
}

func (p HTTPProcessor) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return defaultProcessorTimeout
}

func (p HTTPProcessor) limits() ProcessorLimits {
	limits := ProcessorLimits{
		MaxResponseBytes:   p.MaxResponseBytes,
		MaxRepresentations: p.MaxRepresentations,
		MaxSummaryBytes:    p.MaxSummaryBytes,
		RawMediaAllowed:    p.RawMediaAllowed,
		DegradeOnOversize:  p.DegradeOnOversize,
		RetryNonIdempotent: p.RetryNonIdempotent,
	}
	if limits.MaxResponseBytes <= 0 {
		limits.MaxResponseBytes = defaultProcessorMaxResponseBytes
	}
	if limits.MaxRepresentations <= 0 {
		limits.MaxRepresentations = defaultProcessorMaxReps
	}
	if limits.MaxSummaryBytes <= 0 {
		limits.MaxSummaryBytes = defaultProcessorMaxSummaryBytes
	}
	return limits
}

func ValidateRepresentations(reps []Representation, limits ProcessorLimits) ([]Representation, error) {
	maxReps := limits.MaxRepresentations
	if maxReps <= 0 {
		maxReps = defaultProcessorMaxReps
	}
	if len(reps) > maxReps {
		return nil, fmt.Errorf("artifact processor returned %d representations, max %d", len(reps), maxReps)
	}
	maxSummary := limits.MaxSummaryBytes
	if maxSummary <= 0 {
		maxSummary = defaultProcessorMaxSummaryBytes
	}
	out := make([]Representation, 0, len(reps))
	for _, rep := range reps {
		rep.Type = strings.TrimSpace(rep.Type)
		if rep.Type == "" {
			return nil, fmt.Errorf("artifact representation type is required")
		}
		if len(rep.Summary) > maxSummary {
			if !limits.DegradeOnOversize {
				return nil, fmt.Errorf("artifact representation summary exceeds %d bytes", maxSummary)
			}
			rep.Summary = rep.Summary[:maxSummary]
			if rep.Metadata == nil {
				rep.Metadata = map[string]any{}
			}
			rep.Metadata["degraded"] = true
			rep.Metadata["original_summary_bytes_exceeded"] = true
		}
		if !limits.RawMediaAllowed && metadataHasRawPayload(rep.Metadata) {
			return nil, fmt.Errorf("artifact representation metadata contains raw payload fields")
		}
		out = append(out, rep)
	}
	return out, nil
}

func metadataHasRawPayload(metadata map[string]any) bool {
	for key := range metadata {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "raw", "raw_payload", "payload", "binary", "blob", "bytes", "base64", "data":
			return true
		}
	}
	return false
}
