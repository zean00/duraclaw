package providers

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
	BaseURL      string
	APIKey       string
	DefaultModel string
	Headers      map[string]string
	HTTPClient   *http.Client
}

func (p OpenAICompatibleProvider) GetDefaultModel() string {
	if strings.TrimSpace(p.DefaultModel) != "" {
		return p.DefaultModel
	}
	return "gpt-4.1-mini"
}

func (p OpenAICompatibleProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	if strings.TrimSpace(model) == "" {
		model = p.GetDefaultModel()
	}
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	for k, v := range options {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
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
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errPayload)
		return nil, fmt.Errorf("openai-compatible provider status %d: %v", resp.StatusCode, errPayload)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content   any        `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage UsageInfo `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Choices) == 0 {
		return nil, fmt.Errorf("openai-compatible provider returned no choices")
	}
	choice := payload.Choices[0]
	return &LLMResponse{
		Content:      responseContentText(choice.Message.Content),
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Usage:        payload.Usage,
	}, nil
}

func responseContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return strings.Join(out, "\n")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type OpenAIProvider struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	HTTPClient   *http.Client
}

func (p OpenAIProvider) GetDefaultModel() string {
	return p.compatible().GetDefaultModel()
}

func (p OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().Chat(ctx, messages, tools, model, options)
}

func (p OpenAIProvider) compatible() OpenAICompatibleProvider {
	baseURL := p.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return OpenAICompatibleProvider{
		BaseURL:      baseURL,
		APIKey:       p.APIKey,
		DefaultModel: p.DefaultModel,
		HTTPClient:   p.HTTPClient,
	}
}

type OpenRouterProvider struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	Referer      string
	Title        string
	HTTPClient   *http.Client
}

func (p OpenRouterProvider) GetDefaultModel() string {
	return p.compatible().GetDefaultModel()
}

func (p OpenRouterProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().Chat(ctx, messages, tools, model, options)
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
		BaseURL:      baseURL,
		APIKey:       p.APIKey,
		DefaultModel: p.DefaultModel,
		Headers:      headers,
		HTTPClient:   p.HTTPClient,
	}
}
