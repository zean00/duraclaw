package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistrySelectsProcessor(t *testing.T) {
	reg := NewRegistry(MockProcessor{})
	processor, ok := reg.ProcessorFor(Artifact{Modality: "image"})
	if !ok || processor.Name() != "mock_processor" {
		t.Fatalf("processor=%v ok=%v", processor, ok)
	}
	if _, ok := reg.ProcessorFor(Artifact{Modality: "contact"}); ok {
		t.Fatalf("unexpected processor")
	}
}

func TestProviderAdapterFiltersByModalityAndMediaType(t *testing.T) {
	p := ProviderAdapter{
		NameValue:  "ocr",
		Modalities: map[string]bool{"image": true},
		MediaTypes: map[string]bool{"image/png": true},
		ProcessFunc: func(context.Context, ProcessorContext, Artifact) ([]Representation, error) {
			return []Representation{{Type: "ocr_text", Summary: "ok"}}, nil
		},
	}
	if !p.CanProcess(Artifact{Modality: "image", MediaType: "image/png"}) {
		t.Fatalf("expected adapter to process image/png")
	}
	if p.CanProcess(Artifact{Modality: "audio", MediaType: "audio/wav"}) {
		t.Fatalf("unexpected adapter match")
	}
}

func TestHTTPProcessorPostsContextAndArtifact(t *testing.T) {
	var sawCustomer, sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCustomer = r.Header.Get("X-Customer-ID")
		sawAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"representations": []map[string]any{{"type": "ocr_text", "summary": "hello"}}})
	}))
	defer server.Close()
	processor := HTTPProcessor{NameValue: "ocr", BaseURL: server.URL, Token: "tok", Modalities: map[string]bool{"image": true}}
	reps, err := processor.Process(context.Background(), ProcessorContext{CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", RunID: "r", RequestID: "req"}, Artifact{ID: "art", Modality: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if sawCustomer != "c" || sawAuth != "Bearer tok" || len(reps) != 1 || reps[0].Summary != "hello" {
		t.Fatalf("headers customer=%q auth=%q reps=%#v", sawCustomer, sawAuth, reps)
	}
}
