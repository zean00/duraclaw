package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duraclaw/internal/db"
	"duraclaw/internal/policy"
	"duraclaw/internal/providers"
)

type fakeMediaRegistry struct {
	provider providers.LLMProvider
}

func (r fakeMediaRegistry) DefaultProvider() string { return "fake" }
func (r fakeMediaRegistry) Get(name string) (providers.LLMProvider, bool) {
	return r.provider, name == "fake"
}

type fakeImageProvider struct{}

func (fakeImageProvider) GetDefaultModel() string { return "fake/image" }
func (fakeImageProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}
func (fakeImageProvider) GenerateImage(context.Context, providers.ImageGenerationRequest) (*providers.ImageGenerationResult, error) {
	return &providers.ImageGenerationResult{Images: []providers.GeneratedImage{{B64JSON: "ZmFrZQ==", RevisedPrompt: "drawn"}}}, nil
}

type fakeVideoProvider struct{}

func (fakeVideoProvider) GetDefaultModel() string { return "fake/video" }
func (fakeVideoProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "ok"}, nil
}
func (fakeVideoProvider) GenerateVideo(context.Context, providers.VideoGenerationRequest) (*providers.VideoGenerationResult, error) {
	return &providers.VideoGenerationResult{Videos: []providers.GeneratedVideo{{B64JSON: "ZmFrZQ==", Status: "completed"}}}, nil
}

type fakeArtifactStore struct {
	artifact db.Artifact
	repType  string
	repText  string
}

func (s *fakeArtifactStore) AttachArtifact(_ context.Context, runID string, a db.Artifact) error {
	s.artifact = a
	return nil
}

func (s *fakeArtifactStore) InsertArtifactRepresentation(_ context.Context, _ string, typ, summary string, _ any) error {
	s.repType = typ
	s.repText = summary
	return nil
}

func TestGenerateImageToolAttachesArtifact(t *testing.T) {
	store := &fakeArtifactStore{}
	tool := MediaGenerationTool{Kind: "image", Registry: fakeMediaRegistry{provider: fakeImageProvider{}}, Store: store}
	result := tool.Execute(context.Background(), ExecutionContext{RunID: "run-1"}, map[string]any{
		"prompt":      "draw",
		"artifact_id": "img-1",
	})
	if result.IsError {
		t.Fatalf("result=%#v", result)
	}
	if store.artifact.ID != "img-1" || store.artifact.Modality != "image" || store.artifact.MediaType != "image/png" {
		t.Fatalf("artifact=%#v", store.artifact)
	}
	if store.artifact.StorageRef != "data:image/png;base64,ZmFrZQ==" || store.artifact.Metadata["revised_prompt"] != "drawn" {
		t.Fatalf("artifact=%#v", store.artifact)
	}
	if store.repType != "generated_image" || store.repText != "drawn" {
		t.Fatalf("representation type=%q text=%q", store.repType, store.repText)
	}
}

func TestGenerateImageToolHonorsArtifactPolicy(t *testing.T) {
	store := &fakeArtifactStore{}
	tool := MediaGenerationTool{
		Kind:           "image",
		Registry:       fakeMediaRegistry{provider: fakeImageProvider{}},
		Store:          store,
		ArtifactPolicy: policy.ArtifactRule{MediaTypes: map[string]bool{"audio/mpeg": true}},
	}
	result := tool.Execute(context.Background(), ExecutionContext{RunID: "run-1"}, map[string]any{"prompt": "draw"})
	if !result.IsError {
		t.Fatalf("expected policy error, got %#v", result)
	}
	if store.artifact.ID != "" {
		t.Fatalf("artifact should not be attached: %#v", store.artifact)
	}
}

func TestGenerateImageToolStoresBlobWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	store := &fakeArtifactStore{}
	tool := MediaGenerationTool{
		Kind:      "image",
		Registry:  fakeMediaRegistry{provider: fakeImageProvider{}},
		Store:     store,
		BlobStore: FileMediaBlobStore{Directory: dir, RefPrefix: "object://generated"},
	}
	result := tool.Execute(context.Background(), ExecutionContext{RunID: "run-1"}, map[string]any{"prompt": "draw", "artifact_id": "img/1"})
	if result.IsError {
		t.Fatalf("result=%#v", result)
	}
	if !strings.HasPrefix(store.artifact.StorageRef, "object://generated/") || !strings.HasSuffix(store.artifact.StorageRef, ".png") || store.artifact.SizeBytes != 4 || store.artifact.Checksum == "" {
		t.Fatalf("artifact=%#v", store.artifact)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".png" {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestGenerateVideoToolStoresBlobWithVideoExtension(t *testing.T) {
	dir := t.TempDir()
	store := &fakeArtifactStore{}
	tool := MediaGenerationTool{
		Kind:      "video",
		Registry:  fakeMediaRegistry{provider: fakeVideoProvider{}},
		Store:     store,
		BlobStore: FileMediaBlobStore{Directory: dir, RefPrefix: "object://generated"},
	}
	result := tool.Execute(context.Background(), ExecutionContext{RunID: "run-1"}, map[string]any{"prompt": "make video", "artifact_id": "vid-1"})
	if result.IsError {
		t.Fatalf("result=%#v", result)
	}
	if !strings.HasSuffix(store.artifact.StorageRef, ".mp4") || store.artifact.MediaType != "video/mp4" {
		t.Fatalf("artifact=%#v", store.artifact)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".mp4" {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestHTTPMediaBlobStoreUsesSignedPutURLAsIs(t *testing.T) {
	var sawPath, sawType, sawChecksum, sawAuth, sawBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.RequestURI()
		sawType = r.Header.Get("Content-Type")
		sawChecksum = r.Header.Get("X-Checksum-SHA256")
		sawAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		sawBody = string(raw)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	ref, size, checksum, err := (HTTPMediaBlobStore{
		PutURL:    server.URL + "/signed/key?signature=abc",
		RefPrefix: "object://bucket/generated",
		Headers:   map[string]string{"Authorization": "Bearer token"},
	}).StoreGeneratedMedia(context.Background(), "img/1", "image/png", []byte("fake"))
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/signed/key?signature=abc" || sawType != "image/png" || sawAuth != "Bearer token" || sawBody != "fake" {
		t.Fatalf("path=%q type=%q auth=%q body=%q", sawPath, sawType, sawAuth, sawBody)
	}
	if sawChecksum == "" || checksum == "" || size != 4 || ref != "object://bucket/generated/img-1.png" {
		t.Fatalf("checksum=%q result checksum=%q size=%d ref=%q", sawChecksum, checksum, size, ref)
	}
}

func TestHTTPMediaBlobStoreCanAppendToBaseURL(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	ref, _, _, err := (HTTPMediaBlobStore{BaseURL: server.URL + "/objects", RefPrefix: "object://bucket/generated"}).StoreGeneratedMedia(context.Background(), "img/1", "image/png", []byte("fake"))
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/objects/img-1.png" || ref != "object://bucket/generated/img-1.png" {
		t.Fatalf("path=%q ref=%q", sawPath, ref)
	}
}
