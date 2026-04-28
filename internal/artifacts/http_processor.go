package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPProcessor struct {
	NameValue  string
	BaseURL    string
	Token      string
	Modalities map[string]bool
	MediaTypes map[string]bool
	HTTPClient *http.Client
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
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"context": exec, "artifact": a})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.BaseURL, "/")+"/artifacts/process", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	req.Header.Set("X-Customer-ID", exec.CustomerID)
	req.Header.Set("X-User-ID", exec.UserID)
	req.Header.Set("X-Agent-Instance-ID", exec.AgentInstanceID)
	req.Header.Set("X-Session-ID", exec.SessionID)
	req.Header.Set("X-Run-ID", exec.RunID)
	req.Header.Set("X-Request-ID", exec.RequestID)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("artifact processor failed with status %d", resp.StatusCode)
	}
	var payload struct {
		Representations []Representation `json:"representations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Representations, nil
}
