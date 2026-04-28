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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawModel, _ = body["model"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float32{0.1, 0.2}}}})
	}))
	defer server.Close()
	got, err := (OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "tok", Model: "embed"}).Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if sawAuth != "Bearer tok" || sawModel != "embed" || len(got) != 2 || got[0] != 0.1 {
		t.Fatalf("auth=%q model=%q got=%#v", sawAuth, sawModel, got)
	}
}
