package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/policy"
	"duraclaw/internal/providers"
)

type ProviderRegistry interface {
	DefaultProvider() string
	Get(name string) (providers.LLMProvider, bool)
}

type ArtifactStore interface {
	AttachArtifact(ctx context.Context, runID string, a db.Artifact) error
	InsertArtifactRepresentation(ctx context.Context, artifactID, typ, summary string, metadata any) error
}

type MediaBlobStore interface {
	StoreGeneratedMedia(ctx context.Context, artifactID, mediaType string, data []byte) (storageRef string, sizeBytes int64, checksum string, err error)
}

type FileMediaBlobStore struct {
	Directory string
	RefPrefix string
}

func (s FileMediaBlobStore) StoreGeneratedMedia(ctx context.Context, artifactID, mediaType string, data []byte) (string, int64, string, error) {
	if strings.TrimSpace(s.Directory) == "" {
		return "", 0, "", fmt.Errorf("generated media directory is required")
	}
	if err := ctx.Err(); err != nil {
		return "", 0, "", err
	}
	if err := os.MkdirAll(s.Directory, 0o750); err != nil {
		return "", 0, "", err
	}
	sum := sha256.Sum256(data)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	name := safeArtifactFilename(artifactID) + mediaExtension(mediaType)
	path := filepath.Join(s.Directory, name)
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", 0, "", err
	}
	ref := strings.TrimRight(s.RefPrefix, "/")
	if ref != "" {
		ref += "/" + name
	} else {
		ref = path
	}
	return ref, int64(len(data)), checksum, nil
}

type HTTPMediaBlobStore struct {
	PutURL    string
	BaseURL   string
	RefPrefix string
	Headers   map[string]string
	Client    *http.Client
}

func (s HTTPMediaBlobStore) StoreGeneratedMedia(ctx context.Context, artifactID, mediaType string, data []byte) (string, int64, string, error) {
	name := safeArtifactFilename(artifactID) + mediaExtension(mediaType)
	target := strings.TrimSpace(s.PutURL)
	if target == "" {
		base := strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		if base == "" {
			return "", 0, "", fmt.Errorf("generated media HTTP PUT URL or base URL is required")
		}
		var err error
		target, err = joinURLPath(base, name)
		if err != nil {
			return "", 0, "", err
		}
	}
	sum := sha256.Sum256(data)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(data))
	if err != nil {
		return "", 0, "", err
	}
	req.Header.Set("Content-Type", mediaType)
	req.Header.Set("X-Checksum-SHA256", strings.TrimPrefix(checksum, "sha256:"))
	for key, value := range s.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", 0, "", fmt.Errorf("generated media upload status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	refPrefix := strings.TrimRight(strings.TrimSpace(s.RefPrefix), "/")
	if refPrefix != "" {
		return refPrefix + "/" + name, int64(len(data)), checksum, nil
	}
	return target, int64(len(data)), checksum, nil
}

type MediaGenerationTool struct {
	Kind           string
	Registry       ProviderRegistry
	Store          ArtifactStore
	ModelConfig    providers.ModelConfig
	ArtifactPolicy policy.ArtifactRule
	BlobStore      MediaBlobStore
}

func (t MediaGenerationTool) Name() string { return "duraclaw.generate_" + t.kind() }

func (t MediaGenerationTool) Description() string {
	switch t.kind() {
	case "image":
		return "Generate an image with a provider media generation API and attach it as a durable artifact."
	case "audio":
		return "Generate speech/audio with a provider media generation API and attach it as a durable artifact."
	case "video":
		return "Start video generation with a provider media generation API and attach the generated job/result as a durable artifact."
	default:
		return "Generate provider media and attach it as a durable artifact."
	}
}

func (t MediaGenerationTool) Parameters() map[string]any {
	props := map[string]any{
		"prompt":      map[string]any{"type": "string"},
		"model":       map[string]any{"type": "string"},
		"provider":    map[string]any{"type": "string"},
		"artifact_id": map[string]any{"type": "string"},
	}
	required := []string{"prompt"}
	if t.kind() == "audio" {
		props["voice"] = map[string]any{"type": "string"}
		props["format"] = map[string]any{"type": "string"}
		props["instructions"] = map[string]any{"type": "string"}
	} else {
		props["size"] = map[string]any{"type": "string"}
		props["count"] = map[string]any{"type": "integer"}
	}
	if t.kind() == "image" {
		props["quality"] = map[string]any{"type": "string"}
		props["response_format"] = map[string]any{"type": "string"}
	}
	if t.kind() == "video" {
		props["seconds"] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func (t MediaGenerationTool) Execute(ctx context.Context, exec ExecutionContext, args map[string]any) *Result {
	artifact, err := GenerateMediaArtifact(ctx, MediaGenerationRequest{
		Kind:           t.kind(),
		Registry:       t.Registry,
		Store:          t.Store,
		ModelConfig:    t.ModelConfig,
		ArtifactPolicy: t.ArtifactPolicy,
		BlobStore:      t.BlobStore,
		RunID:          exec.RunID,
		Prompt:         stringArg(args, "prompt"),
		Provider:       stringArg(args, "provider"),
		Model:          stringArg(args, "model"),
		ArtifactID:     stringArg(args, "artifact_id"),
		Size:           stringArg(args, "size"),
		Quality:        stringArg(args, "quality"),
		ResponseFormat: stringArg(args, "response_format"),
		Voice:          stringArg(args, "voice"),
		Format:         stringArg(args, "format"),
		Instructions:   stringArg(args, "instructions"),
		Seconds:        stringArg(args, "seconds"),
		Count:          intArg(args, "count"),
	})
	if err != nil {
		return ErrorResult(err.Error())
	}
	raw, _ := json.Marshal(artifact)
	return NewResult(string(raw))
}

func (t MediaGenerationTool) kind() string {
	k := strings.ToLower(strings.TrimSpace(t.Kind))
	if k == "" {
		return "image"
	}
	return k
}

type MediaGenerationRequest struct {
	Kind           string
	Registry       ProviderRegistry
	Store          ArtifactStore
	ModelConfig    providers.ModelConfig
	ArtifactPolicy policy.ArtifactRule
	BlobStore      MediaBlobStore
	RunID          string
	Prompt         string
	Provider       string
	Model          string
	ArtifactID     string
	Size           string
	Quality        string
	ResponseFormat string
	Voice          string
	Format         string
	Instructions   string
	Seconds        string
	Count          int
}

func GenerateMediaArtifact(ctx context.Context, req MediaGenerationRequest) (db.Artifact, error) {
	if req.Registry == nil || req.Store == nil {
		return db.Artifact{}, fmt.Errorf("media generation requires provider registry and artifact store")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return db.Artifact{}, fmt.Errorf("media generation requires prompt")
	}
	providerName, model, provider, err := mediaProvider(req)
	if err != nil {
		return db.Artifact{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = "image"
	}
	artifactID := strings.TrimSpace(req.ArtifactID)
	if artifactID == "" {
		artifactID = fmt.Sprintf("generated-%s-%d", kind, time.Now().UnixNano())
	}
	artifact := db.Artifact{ID: artifactID, Modality: kind, State: "available", SourceChannel: "duraclaw", Metadata: map[string]any{"provider": providerName, "model": model, "prompt": req.Prompt, "generated": true}}
	switch kind {
	case "image":
		gen, ok := provider.(providers.ImageGenerator)
		if !ok {
			return db.Artifact{}, fmt.Errorf("provider %q does not support image generation", providerName)
		}
		result, err := gen.GenerateImage(ctx, providers.ImageGenerationRequest{Prompt: req.Prompt, Model: model, Size: req.Size, Quality: req.Quality, ResponseFormat: req.ResponseFormat, Count: req.Count})
		if err != nil {
			return db.Artifact{}, err
		}
		if len(result.Images) == 0 {
			return db.Artifact{}, fmt.Errorf("image generation returned no images")
		}
		image := result.Images[0]
		artifact.MediaType = "image/png"
		artifact.Filename = artifactID + ".png"
		artifact.StorageRef = image.URL
		if artifact.StorageRef == "" && image.B64JSON != "" {
			if err := storeGeneratedData(ctx, req.BlobStore, &artifact, image.B64JSON); err != nil {
				return db.Artifact{}, err
			}
			if artifact.StorageRef == "" {
				artifact.StorageRef = dataURI("image/png", image.B64JSON)
			}
		}
		artifact.Metadata["revised_prompt"] = image.RevisedPrompt
	case "audio":
		gen, ok := provider.(providers.AudioGenerator)
		if !ok {
			return db.Artifact{}, fmt.Errorf("provider %q does not support audio generation", providerName)
		}
		result, err := gen.GenerateAudio(ctx, providers.AudioGenerationRequest{Text: req.Prompt, Model: model, Voice: req.Voice, Instructions: req.Instructions, ResponseFormat: req.Format})
		if err != nil {
			return db.Artifact{}, err
		}
		mediaType := firstNonEmpty(result.MediaType, "audio/mpeg")
		artifact.MediaType = mediaType
		artifact.Filename = artifactID + mediaExtension(mediaType)
		if req.BlobStore != nil {
			ref, size, checksum, err := req.BlobStore.StoreGeneratedMedia(ctx, artifact.ID, artifact.MediaType, result.Data)
			if err != nil {
				return db.Artifact{}, err
			}
			artifact.StorageRef, artifact.SizeBytes, artifact.Checksum = ref, size, checksum
		} else {
			artifact.StorageRef = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(result.Data)
			artifact.SizeBytes = int64(len(result.Data))
		}
	case "video":
		gen, ok := provider.(providers.VideoGenerator)
		if !ok {
			return db.Artifact{}, fmt.Errorf("provider %q does not support video generation", providerName)
		}
		result, err := gen.GenerateVideo(ctx, providers.VideoGenerationRequest{Prompt: req.Prompt, Model: model, Size: req.Size, Seconds: req.Seconds, Count: req.Count})
		if err != nil {
			return db.Artifact{}, err
		}
		if len(result.Videos) == 0 {
			return db.Artifact{}, fmt.Errorf("video generation returned no videos")
		}
		video := result.Videos[0]
		artifact.MediaType = "video/mp4"
		artifact.Filename = artifactID + ".mp4"
		artifact.StorageRef = video.URL
		if artifact.StorageRef == "" && video.B64JSON != "" {
			if err := storeGeneratedData(ctx, req.BlobStore, &artifact, video.B64JSON); err != nil {
				return db.Artifact{}, err
			}
			if artifact.StorageRef == "" {
				artifact.StorageRef = dataURI("video/mp4", video.B64JSON)
			}
		}
		if artifact.StorageRef == "" {
			artifact.StorageRef = video.ID
		}
		artifact.Metadata["generation_id"] = video.ID
		artifact.Metadata["status"] = video.Status
	default:
		return db.Artifact{}, fmt.Errorf("unsupported media generation kind %q", kind)
	}
	if artifact.StorageRef == "" {
		return db.Artifact{}, fmt.Errorf("%s generation returned no durable storage reference", kind)
	}
	if req.ArtifactPolicy.MaxSizeBytes > 0 || len(req.ArtifactPolicy.MediaTypes) > 0 {
		if err := policy.AllowArtifact(artifact.SizeBytes, artifact.MediaType, req.ArtifactPolicy); err != nil {
			return db.Artifact{}, err
		}
	}
	if err := req.Store.AttachArtifact(ctx, req.RunID, artifact); err != nil {
		return db.Artifact{}, err
	}
	if err := req.Store.InsertArtifactRepresentation(ctx, artifact.ID, generatedRepresentationType(kind), generatedRepresentationSummary(artifact), artifact.Metadata); err != nil {
		return db.Artifact{}, err
	}
	return artifact, nil
}

func storeGeneratedData(ctx context.Context, store MediaBlobStore, artifact *db.Artifact, b64 string) error {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	artifact.SizeBytes = int64(len(raw))
	if store == nil {
		return nil
	}
	ref, size, checksum, err := store.StoreGeneratedMedia(ctx, artifact.ID, artifact.MediaType, raw)
	if err != nil {
		return err
	}
	artifact.StorageRef, artifact.SizeBytes, artifact.Checksum = ref, size, checksum
	return nil
}

func generatedRepresentationType(kind string) string {
	switch kind {
	case "image":
		return "generated_image"
	case "audio":
		return "generated_audio"
	case "video":
		return "generated_video"
	default:
		return "generated_artifact"
	}
}

func generatedRepresentationSummary(a db.Artifact) string {
	prompt, _ := a.Metadata["prompt"].(string)
	revised, _ := a.Metadata["revised_prompt"].(string)
	switch {
	case revised != "":
		return revised
	case prompt != "":
		return prompt
	default:
		return "Generated " + a.Modality + " artifact " + a.ID
	}
}

func mediaProvider(req MediaGenerationRequest) (string, string, providers.LLMProvider, error) {
	providerName := providers.NormalizeProvider(req.Provider)
	model := strings.TrimSpace(req.Model)
	if providerName == "" {
		if ref := providers.ParseModelRef(model, req.Registry.DefaultProvider()); ref != nil {
			providerName = ref.Provider
			model = ref.Model
		}
	}
	if providerName == "" {
		candidates := providers.ResolveCandidates(req.ModelConfig, req.Registry.DefaultProvider())
		if len(candidates) > 0 {
			providerName = candidates[0].Provider
			if model == "" {
				model = candidates[0].Model
			}
		}
	}
	if providerName == "" {
		providerName = req.Registry.DefaultProvider()
	}
	provider, ok := req.Registry.Get(providerName)
	if !ok {
		return "", "", nil, fmt.Errorf("provider %q is not registered", providerName)
	}
	return providerName, model, provider, nil
}

func dataURI(mediaType, b64 string) string {
	if strings.TrimSpace(b64) == "" {
		return ""
	}
	return "data:" + mediaType + ";base64," + b64
}

func joinURLPath(base, name string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("generated media HTTP base URL must be absolute")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + url.PathEscape(name)
	return parsed.String(), nil
}

func mediaExtension(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "audio/wav":
		return ".wav"
	case "audio/aac":
		return ".aac"
	case "audio/flac":
		return ".flac"
	case "audio/opus":
		return ".opus"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	default:
		return ".bin"
	}
}

func safeArtifactFilename(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = fmt.Sprint(time.Now().UnixNano())
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func stringArg(args map[string]any, key string) string {
	if value, _ := args[key].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func intArg(args map[string]any, key string) int {
	switch value := args[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
