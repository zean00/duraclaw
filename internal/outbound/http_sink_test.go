package outbound

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"duraclaw/internal/db"
)

func TestHTTPSinkPostsPayloadWithHeaders(t *testing.T) {
	var sawTopic, sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTopic = r.Header.Get("X-Duraclaw-Outbox-Topic")
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	err := (HTTPSink{URL: server.URL, Token: "token"}).Handle(t.Context(), db.OutboxItem{
		ID: 7, Topic: "nexus.outbound_intent", Payload: []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawTopic != "nexus.outbound_intent" || sawAuth != "Bearer token" {
		t.Fatalf("topic=%q auth=%q", sawTopic, sawAuth)
	}
}

func TestHTTPSinkReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer server.Close()
	err := (HTTPSink{URL: server.URL}).Handle(t.Context(), db.OutboxItem{ID: 1, Payload: []byte(`{}`)})
	if err == nil {
		t.Fatalf("expected status error")
	}
}

func TestHTTPSinkTopicURLOverride(t *testing.T) {
	sink := HTTPSink{URL: "http://default", TopicURLs: map[string]string{"topic": "http://topic"}}
	if got := sink.URLForTopic("topic"); got != "http://topic" {
		t.Fatalf("got %q", got)
	}
	if got := sink.URLForTopic("other"); got != "http://default" {
		t.Fatalf("got %q", got)
	}
}
