package outbound

import (
	"encoding/json"
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
	if err := (HTTPSink{}).Handle(t.Context(), db.OutboxItem{ID: 1, Payload: []byte(`{}`)}); err == nil {
		t.Fatalf("expected missing URL error")
	}
}

func TestHTTPSinkPostsBatchPayload(t *testing.T) {
	var sawSize, sawAuth string
	var body struct {
		Topic string `json:"topic"`
		Items []struct {
			OutboxID int64           `json:"outbox_id"`
			Topic    string          `json:"topic"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"items"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSize = r.Header.Get("X-Duraclaw-Outbox-Batch-Size")
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	err := (HTTPSink{BatchURL: server.URL, Token: "token"}).HandleBatch(t.Context(), "nexus.outbound_intent", []db.OutboxItem{
		{ID: 7, Topic: "nexus.outbound_intent", Payload: []byte(`{"a":1}`)},
		{ID: 8, Topic: "nexus.outbound_intent", Payload: []byte(`{"b":2}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawSize != "2" || sawAuth != "Bearer token" || body.Topic != "nexus.outbound_intent" || len(body.Items) != 2 || body.Items[0].OutboxID != 7 {
		t.Fatalf("size=%q auth=%q body=%#v", sawSize, sawAuth, body)
	}
}

func TestHTTPSinkDoesNotSupportBatchWithoutBatchURL(t *testing.T) {
	sink := HTTPSink{URL: "http://single"}
	if sink.SupportsBatch("topic") {
		t.Fatalf("expected batch support to be disabled without batch URL")
	}
	if err := sink.HandleBatch(t.Context(), "topic", []db.OutboxItem{{ID: 1, Topic: "topic"}}); err == nil {
		t.Fatalf("expected missing batch URL error")
	}
	if err := sink.HandleBatch(t.Context(), "topic", nil); err != nil {
		t.Fatalf("empty batch should be a no-op: %v", err)
	}
}

func TestHTTPSinkBatchTopicOverrideAndStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad batch", http.StatusBadGateway)
	}))
	defer server.Close()
	sink := HTTPSink{BatchURL: "http://default", BatchURLs: map[string]string{"topic": server.URL}}
	if !sink.SupportsBatch("topic") || sink.BatchURLForTopic("topic") != server.URL || sink.BatchURLForTopic("other") != "http://default" {
		t.Fatalf("batch routing failed")
	}
	if err := sink.HandleBatch(t.Context(), "topic", []db.OutboxItem{{ID: 1, Topic: "topic"}}); err == nil {
		t.Fatalf("expected batch status error")
	}
}

func TestBatchItemsUsesEmptyObjectForMissingPayload(t *testing.T) {
	got := batchItems([]db.OutboxItem{{ID: 1, Topic: "topic"}})
	payload, ok := got[0]["payload"].(map[string]any)
	if len(got) != 1 || !ok || len(payload) != 0 {
		t.Fatalf("items=%#v", got)
	}
}
