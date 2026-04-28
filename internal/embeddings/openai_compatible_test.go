package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleProviderEmbeds(t *testing.T) {
	var sawAuth, sawModel string
	var sawInput any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawModel, _ = body["model"].(string)
		sawInput = body["input"]
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float32{0.1, 0.2}}}})
	}))
	defer server.Close()
	got, err := (OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "tok", Model: "embed"}).EmbedInput(context.Background(), []any{"hello", map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer tok" || sawModel != "embed" || len(got) != 2 || got[0] != 0.1 {
		t.Fatalf("auth=%q model=%q got=%#v", sawAuth, sawModel, got)
	}
	if input, ok := sawInput.([]any); !ok || len(input) != 2 {
		t.Fatalf("input=%#v", sawInput)
	}
}

func TestOpenRouterProviderEmbedsWithHeaders(t *testing.T) {
	var sawReferer, sawTitle string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawReferer = r.Header.Get("HTTP-Referer")
		sawTitle = r.Header.Get("X-Title")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float32{0.1, 0.2}}}})
	}))
	defer server.Close()
	got, err := (OpenRouterProvider{
		BaseURL: server.URL, APIKey: "tok", Model: "openai/text-embedding-3-small", Referer: "https://duraclaw.test", Title: "Duraclaw",
	}).Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if sawReferer != "https://duraclaw.test" || sawTitle != "Duraclaw" || len(got) != 2 {
		t.Fatalf("referer=%q title=%q got=%#v", sawReferer, sawTitle, got)
	}
}
