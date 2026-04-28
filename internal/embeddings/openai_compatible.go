package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type OpenAICompatibleProvider struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	Headers    map[string]string
	HTTPClient *http.Client
}

func (p OpenAICompatibleProvider) Dimension() int {
	if p.Dimensions > 0 {
		return p.Dimensions
	}
	return 768
}

func (p OpenAICompatibleProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return p.EmbedInput(ctx, text)
}

func (p OpenAICompatibleProvider) EmbedInput(ctx context.Context, input any) ([]float32, error) {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "text-embedding-3-small"
	}
	body := map[string]any{"model": model, "input": input}
	if p.Dimensions > 0 {
		body["dimensions"] = p.Dimensions
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for key, value := range p.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errPayload)
		return nil, fmt.Errorf("openai-compatible embeddings status %d: %v", resp.StatusCode, errPayload)
	}
	var payload struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai-compatible embeddings returned no embedding")
	}
	return payload.Data[0].Embedding, nil
}

type OpenRouterProvider struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dimensions int
	Referer    string
	Title      string
	HTTPClient *http.Client
}

func (p OpenRouterProvider) Dimension() int {
	return p.compatible().Dimension()
}

func (p OpenRouterProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return p.compatible().Embed(ctx, text)
}

func (p OpenRouterProvider) EmbedInput(ctx context.Context, input any) ([]float32, error) {
	return p.compatible().EmbedInput(ctx, input)
}

func (p OpenRouterProvider) compatible() OpenAICompatibleProvider {
	baseURL := p.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	headers := map[string]string{}
	if strings.TrimSpace(p.Referer) != "" {
		headers["HTTP-Referer"] = p.Referer
	}
	if strings.TrimSpace(p.Title) != "" {
		headers["X-Title"] = p.Title
	}
	return OpenAICompatibleProvider{
		BaseURL:    baseURL,
		APIKey:     p.APIKey,
		Model:      p.Model,
		Dimensions: p.Dimensions,
		Headers:    headers,
		HTTPClient: p.HTTPClient,
	}
}
