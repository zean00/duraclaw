package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleProviderChat(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
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
	p := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "key", DefaultModel: "model"}
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("resp=%#v", resp)
	}
	if sawAuth != "Bearer key" {
		t.Fatalf("auth=%q", sawAuth)
	}
}
