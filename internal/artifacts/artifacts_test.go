package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	var sawCustomer, sawAuth, sawArtifact, sawProcessor, sawTrace string
	var sawBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawCustomer = r.Header.Get("X-Customer-ID")
		sawAuth = r.Header.Get("Authorization")
		sawArtifact = r.Header.Get("X-Artifact-ID")
		sawProcessor = r.Header.Get("X-Processor-Call-ID")
		sawTrace = r.Header.Get("traceparent")
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(map[string]any{"representations": []map[string]any{{"type": "ocr_text", "summary": "hello"}}})
	}))
	defer server.Close()
	processor := HTTPProcessor{NameValue: "ocr", BaseURL: server.URL, Token: "tok", Modalities: map[string]bool{"image": true}}
	reps, err := processor.Process(context.Background(), ProcessorContext{CustomerID: "c", UserID: "u", AgentInstanceID: "a", SessionID: "s", RunID: "r", RequestID: "req", ProcessorCallID: "pc", TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}, Artifact{ID: "art", Modality: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if sawCustomer != "c" || sawAuth != "Bearer tok" || sawArtifact != "art" || sawProcessor != "pc" || sawTrace == "" || sawBody["processor_call_id"] != "pc" || len(reps) != 1 || reps[0].Summary != "hello" {
		t.Fatalf("headers customer=%q auth=%q artifact=%q processor=%q trace=%q body=%#v reps=%#v", sawCustomer, sawAuth, sawArtifact, sawProcessor, sawTrace, sawBody, reps)
	}
}

func TestHTTPProcessorRejectsRawPayloadMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"representations": []map[string]any{{"type": "ocr_text", "summary": "hello", "metadata": map[string]any{"base64": "abcd"}}}})
	}))
	defer server.Close()
	_, err := (HTTPProcessor{BaseURL: server.URL}).Process(context.Background(), ProcessorContext{}, Artifact{ID: "art", Modality: "image"})
	if err == nil {
		t.Fatal("expected raw payload validation error")
	}
}

func TestHTTPProcessorDegradesOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"representations":[{"type":"ocr_text","summary":"` + strings.Repeat("x", 128) + `"}]}`))
	}))
	defer server.Close()
	reps, err := (HTTPProcessor{BaseURL: server.URL, MaxResponseBytes: 16, DegradeOnOversize: true}).Process(context.Background(), ProcessorContext{}, Artifact{ID: "art", Modality: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Metadata["degraded"] != true {
		t.Fatalf("reps=%#v", reps)
	}
}
