package profiles

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMergeMetadataStoresProfileEnvelope(t *testing.T) {
	got := MergeMetadata(map[string]any{"keep": true}, &Result{
		Profile:  map[string]any{"display_name": "Sahal", "email": "s@example.test"},
		Metadata: map[string]any{"etag": "v1"},
	}, "http", time.Date(2026, 4, 29, 1, 2, 3, 0, time.UTC))
	if got["keep"] != true {
		t.Fatalf("metadata was not preserved: %#v", got)
	}
	profile := got["profile"].(map[string]any)
	if profile["display_name"] != "Sahal" {
		t.Fatalf("profile=%#v", profile)
	}
	source := got["profile_source"].(map[string]any)
	if source["provider"] != "http" || source["etag"] != "v1" {
		t.Fatalf("source=%#v", source)
	}
}

func TestAllowedProfileFiltersFields(t *testing.T) {
	got := AllowedProfile(map[string]any{
		"profile": map[string]any{"display_name": "Sahal", "email": "s@example.test", "timezone": "Asia/Jakarta"},
	}, []string{"display_name", "timezone"})
	if len(got) != 2 || got["display_name"] != "Sahal" || got["timezone"] != "Asia/Jakarta" {
		t.Fatalf("filtered=%#v", got)
	}
	if _, ok := got["email"]; ok {
		t.Fatalf("email should not be included: %#v", got)
	}
}

func TestHTTPRetrieverRetrieveProfile(t *testing.T) {
	var sawAuth, sawCustomer, sawCustom string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCustomer = r.Header.Get("X-Customer-ID")
		sawCustom = r.Header.Get("X-Custom")
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.CustomerID != "c" || req.UserID != "u" {
			t.Fatalf("request=%#v", req)
		}
		_ = json.NewEncoder(w).Encode(Result{Profile: map[string]any{"display_name": "Sahal"}})
	}))
	defer server.Close()
	got, err := (HTTPRetriever{
		URL:     server.URL,
		Token:   "token",
		Headers: map[string]string{"X-Custom": "ok", "X-Empty": ""},
		Timeout: time.Second,
	}).RetrieveProfile(t.Context(), Request{CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Profile["display_name"] != "Sahal" {
		t.Fatalf("profile=%#v", got)
	}
	if sawAuth != "Bearer token" || sawCustomer != "c" || sawCustom != "ok" {
		t.Fatalf("headers auth=%q customer=%q custom=%q", sawAuth, sawCustomer, sawCustom)
	}
}

func TestHTTPRetrieverEmptyAndNotFound(t *testing.T) {
	if got, err := (HTTPRetriever{}).RetrieveProfile(t.Context(), Request{}); err != nil || got != nil {
		t.Fatalf("empty retriever got=%#v err=%v", got, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	if got, err := (HTTPRetriever{URL: server.URL}).RetrieveProfile(t.Context(), Request{}); err != nil || got != nil {
		t.Fatalf("not found got=%#v err=%v", got, err)
	}
}

func TestHTTPRetrieverReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()
	if _, err := (HTTPRetriever{URL: server.URL}).RetrieveProfile(t.Context(), Request{}); err == nil {
		t.Fatalf("expected error")
	}
}
