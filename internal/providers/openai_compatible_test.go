package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleProviderChat(t *testing.T) {
	var sawAuth string
	var sawCustom string
	var sawContent any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCustom = r.Header.Get("X-Test")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		if len(messages) > 0 {
			message, _ := messages[0].(map[string]any)
			sawContent = message["content"]
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "hello"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		})
	}))
	defer server.Close()
	p := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "key", DefaultModel: "model", Headers: map[string]string{"X-Test": "ok"}}
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", ContentParts: []ContentPart{
		{Type: "text", Text: "hi"},
		{Type: "image_url", ImageURL: &ImageURLContent{URL: "https://example.test/image.png"}},
	}}}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("resp=%#v", resp)
	}
	if sawAuth != "Bearer key" {
		t.Fatalf("auth=%q", sawAuth)
	}
	if sawCustom != "ok" {
		t.Fatalf("custom=%q", sawCustom)
	}
	parts, ok := sawContent.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content=%#v", sawContent)
	}
	imagePart, _ := parts[1].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("imagePart=%#v", imagePart)
	}
}

func TestOpenAIProviderUsesOpenAIDefaultBaseURL(t *testing.T) {
	p := OpenAIProvider{APIKey: "key", DefaultModel: "gpt-test"}
	compatible := p.compatible()
	if compatible.BaseURL != "https://api.openai.com/v1" || compatible.DefaultModel != "gpt-test" {
		t.Fatalf("compatible=%#v", compatible)
	}
}

func TestOpenRouterProviderUsesHeadersAndDefaultBaseURL(t *testing.T) {
	p := OpenRouterProvider{APIKey: "key", DefaultModel: "openai/gpt-test", Referer: "https://duraclaw.test", Title: "Duraclaw"}
	compatible := p.compatible()
	if compatible.BaseURL != "https://openrouter.ai/api/v1" || compatible.DefaultModel != "openai/gpt-test" {
		t.Fatalf("compatible=%#v", compatible)
	}
	if compatible.Headers["HTTP-Referer"] != "https://duraclaw.test" || compatible.Headers["X-Title"] != "Duraclaw" {
		t.Fatalf("headers=%#v", compatible.Headers)
	}
}

func TestResponseContentTextHandlesArrayContent(t *testing.T) {
	got := responseContentText([]any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "output_text", "text": "world"},
	})
	if got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}
