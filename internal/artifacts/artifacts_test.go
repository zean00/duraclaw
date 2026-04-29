package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"duraclaw/internal/providers"
)

type fakeLLMProvider struct {
	messages []providers.Message
}

func (p *fakeLLMProvider) GetDefaultModel() string { return "fake/model" }

func (p *fakeLLMProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.messages = messages
	return &providers.LLMResponse{Content: "extracted text", FinishReason: "stop"}, nil
}

type fakeUploadLLMProvider struct {
	fakeLLMProvider
	uploaded providers.FileUploadRequest
}

func (p *fakeUploadLLMProvider) UploadFile(_ context.Context, req providers.FileUploadRequest) (*providers.FileUploadResult, error) {
	p.uploaded = req
	return &providers.FileUploadResult{ID: "file_123", Filename: req.Filename, Purpose: req.Purpose, Bytes: int64(len(req.Data))}, nil
}

type fakeTranscriptionProvider struct {
	fakeLLMProvider
	request providers.AudioTranscriptionRequest
}

func (p *fakeTranscriptionProvider) TranscribeAudio(_ context.Context, req providers.AudioTranscriptionRequest) (*providers.AudioTranscriptionResult, error) {
	p.request = req
	return &providers.AudioTranscriptionResult{Text: "hello from audio"}, nil
}

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

func TestProviderProcessorProcessesImage(t *testing.T) {
	provider := &fakeLLMProvider{}
	processor := ProviderProcessor{Provider: provider, Model: "vision", Modalities: map[string]bool{"image": true}}
	reps, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "img-1", Modality: "image", MediaType: "image/png", StorageRef: "https://example.test/image.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Type != "vision_summary" || reps[0].Summary != "extracted text" {
		t.Fatalf("reps=%#v", reps)
	}
	if len(provider.messages) != 1 || len(provider.messages[0].ContentParts) != 2 || provider.messages[0].ContentParts[1].ImageURL == nil {
		t.Fatalf("messages=%#v", provider.messages)
	}
}

func TestProviderProcessorProcessesAudioTranscript(t *testing.T) {
	provider := &fakeLLMProvider{}
	processor := ProviderProcessor{Provider: provider, Model: "audio"}
	reps, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "aud-1", Modality: "audio", MediaType: "audio/mpeg", Metadata: map[string]any{"base64": "abc", "format": "mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Type != "transcript" {
		t.Fatalf("reps=%#v", reps)
	}
	if provider.messages[0].ContentParts[1].InputAudio == nil {
		t.Fatalf("messages=%#v", provider.messages)
	}
}

func TestProviderProcessorFetchesDocumentURLAsDataURI(t *testing.T) {
	provider := &fakeLLMProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7"))
	}))
	defer server.Close()
	processor := ProviderProcessor{Provider: provider, Model: "document"}
	reps, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "doc-1", Modality: "document", MediaType: "application/pdf", StorageRef: server.URL + "/doc.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Type != "document_text" {
		t.Fatalf("reps=%#v", reps)
	}
	file := provider.messages[0].ContentParts[1].File
	if file == nil || !strings.HasPrefix(file.FileData, "data:application/pdf;base64,") {
		t.Fatalf("messages=%#v", provider.messages)
	}
}

func TestProviderProcessorProcessesDocumentDataURI(t *testing.T) {
	provider := &fakeLLMProvider{}
	processor := ProviderProcessor{Provider: provider, Model: "document"}
	reps, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "doc-1", Modality: "document", MediaType: "application/pdf", StorageRef: "data:application/pdf;base64,abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Type != "document_text" {
		t.Fatalf("reps=%#v", reps)
	}
	if provider.messages[0].ContentParts[1].File == nil || provider.messages[0].ContentParts[1].File.FileData == "" {
		t.Fatalf("messages=%#v", provider.messages)
	}
}

func TestProviderProcessorUploadsFetchedDocumentWhenSupported(t *testing.T) {
	provider := &fakeUploadLLMProvider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("document body"))
	}))
	defer server.Close()
	processor := ProviderProcessor{Provider: provider, Model: "document"}
	_, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "doc-1", Modality: "document", MediaType: "text/plain", StorageRef: server.URL + "/doc.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(provider.uploaded.Data) != "document body" || provider.uploaded.Purpose != "user_data" {
		t.Fatalf("uploaded=%#v", provider.uploaded)
	}
	file := provider.messages[0].ContentParts[1].File
	if file == nil || file.FileID != "file_123" || file.FileData != "" {
		t.Fatalf("messages=%#v", provider.messages)
	}
}

func TestProviderProcessorUsesTranscriptionEndpointForAudio(t *testing.T) {
	provider := &fakeTranscriptionProvider{}
	processor := ProviderProcessor{Provider: provider, Model: "audio"}
	reps, err := processor.Process(context.Background(), ProcessorContext{}, Artifact{
		ID: "aud-1", Modality: "audio", MediaType: "audio/mpeg", Metadata: map[string]any{"base64": "aGVsbG8=", "format": "mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Summary != "hello from audio" || reps[0].Metadata["transcription_api"] != true {
		t.Fatalf("reps=%#v", reps)
	}
	if string(provider.request.Data) != "hello" || provider.request.MediaType != "audio/mpeg" {
		t.Fatalf("request=%#v", provider.request)
	}
	if len(provider.messages) != 0 {
		t.Fatalf("chat should not be called: %#v", provider.messages)
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
