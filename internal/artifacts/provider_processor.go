package artifacts

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"duraclaw/internal/providers"
)

type ProviderProcessor struct {
	NameValue       string
	Provider        providers.LLMProvider
	Model           string
	Modalities      map[string]bool
	MediaTypes      map[string]bool
	MaxSummaryBytes int
	MaxFetchBytes   int64
	HTTPClient      *http.Client
}

func (p ProviderProcessor) Name() string {
	if strings.TrimSpace(p.NameValue) != "" {
		return p.NameValue
	}
	return "provider_artifact_processor"
}

func (p ProviderProcessor) CanProcess(a Artifact) bool {
	if p.Provider == nil {
		return false
	}
	if len(p.Modalities) > 0 && !p.Modalities[a.Modality] {
		return false
	}
	if len(p.MediaTypes) > 0 && !p.MediaTypes[a.MediaType] {
		return false
	}
	switch a.Modality {
	case "image", "document", "audio", "video":
		return true
	default:
		return false
	}
}

func (p ProviderProcessor) Process(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, error) {
	if p.Provider == nil {
		return nil, fmt.Errorf("artifact provider processor %q has no provider", p.Name())
	}
	if a.Modality == "audio" {
		if reps, ok, err := p.transcribeAudio(ctx, exec, a); ok || err != nil {
			return reps, err
		}
	}
	part, err := p.contentPart(ctx, a)
	if err != nil {
		return nil, err
	}
	messages := []providers.Message{{
		Role: "user",
		ContentParts: []providers.ContentPart{
			{Type: "text", Text: p.prompt(a, exec.RequestedRepresentations)},
			part,
		},
	}}
	resp, err := p.Provider.Chat(ctx, messages, nil, p.Model, map[string]any{"temperature": 0})
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return nil, fmt.Errorf("artifact provider processor returned empty content")
	}
	rep := Representation{
		Type:    p.representationType(a, exec.RequestedRepresentations),
		Summary: summary,
		Metadata: map[string]any{
			"provider_processor": p.Name(),
			"model":              p.Model,
			"modality":           a.Modality,
			"media_type":         a.MediaType,
			"finish_reason":      resp.FinishReason,
		},
	}
	return ValidateRepresentations([]Representation{rep}, ProcessorLimits{
		MaxRepresentations: 1,
		MaxSummaryBytes:    p.MaxSummaryBytes,
		DegradeOnOversize:  true,
	})
}

func (p ProviderProcessor) transcribeAudio(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, bool, error) {
	transcriber, ok := p.Provider.(providers.AudioTranscriptionProvider)
	if !ok {
		return nil, false, nil
	}
	payload, err := p.artifactPayload(ctx, a)
	if err != nil {
		return nil, true, err
	}
	resp, err := transcriber.TranscribeAudio(ctx, providers.AudioTranscriptionRequest{
		Data:      payload.Data,
		Filename:  payload.Filename,
		MediaType: payload.MediaType,
		Model:     metadataString(a.Metadata, "transcription_model", "model"),
		Language:  metadataString(a.Metadata, "language"),
		Prompt:    p.prompt(a, exec.RequestedRepresentations),
	})
	if err != nil {
		return nil, true, err
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		return nil, true, fmt.Errorf("audio transcription returned empty content")
	}
	rep := Representation{
		Type:    p.representationType(a, exec.RequestedRepresentations),
		Summary: summary,
		Metadata: map[string]any{
			"provider_processor": p.Name(),
			"transcription_api":  true,
			"model":              metadataString(a.Metadata, "transcription_model", "model"),
			"modality":           a.Modality,
			"media_type":         a.MediaType,
		},
	}
	reps, err := ValidateRepresentations([]Representation{rep}, ProcessorLimits{MaxRepresentations: 1, MaxSummaryBytes: p.MaxSummaryBytes, DegradeOnOversize: true})
	return reps, true, err
}

func (p ProviderProcessor) prompt(a Artifact, requested []string) string {
	if len(requested) > 0 {
		return "Analyze the attached artifact and produce these durable representations if possible: " + strings.Join(requested, ", ") + ". Return only the representation text."
	}
	switch a.Modality {
	case "image":
		return "Analyze this image. Extract any visible text and summarize important visual details. Return concise plain text."
	case "document":
		return "Extract the document text and summarize the important content. Return concise plain text."
	case "audio":
		return "Transcribe this audio as accurately as possible. Include a short summary only if useful. Return plain text."
	case "video":
		return "Analyze this video. Summarize key events, visible text, and important context. Return concise plain text."
	default:
		return "Analyze this artifact and return a concise plain-text representation."
	}
}

func (p ProviderProcessor) representationType(a Artifact, requested []string) string {
	for _, rep := range requested {
		rep = strings.TrimSpace(rep)
		if rep != "" {
			return rep
		}
	}
	switch a.Modality {
	case "image":
		return "vision_summary"
	case "document":
		return "document_text"
	case "audio":
		return "transcript"
	case "video":
		return "video_summary"
	default:
		return "metadata_summary"
	}
}

func (p ProviderProcessor) contentPart(ctx context.Context, a Artifact) (providers.ContentPart, error) {
	data := a.Metadata
	if data == nil {
		data = map[string]any{}
	}
	str := func(keys ...string) string {
		for _, key := range keys {
			if value, _ := data[key].(string); strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	storageRef := strings.TrimSpace(a.StorageRef)
	switch a.Modality {
	case "image":
		url := firstNonEmpty(str("url", "image_url", "data_uri"), storageRef)
		if url == "" {
			return providers.ContentPart{}, fmt.Errorf("image artifact %s requires storage_ref, metadata.url, or metadata.data_uri", a.ID)
		}
		return providers.ContentPart{Type: "image_url", ImageURL: &providers.ImageURLContent{URL: url, Detail: str("detail")}}, nil
	case "document":
		fileData := firstNonEmpty(str("file_data", "data_uri"))
		if fileData == "" && strings.HasPrefix(storageRef, "data:") {
			fileData = storageRef
		}
		fileID := str("file_id")
		if fileData == "" && fileID == "" {
			payload, err := p.artifactPayload(ctx, a)
			if err != nil {
				return providers.ContentPart{}, fmt.Errorf("document artifact %s requires inline metadata.file_data, metadata.data_uri, data URI storage_ref, metadata.file_id, or fetchable URL: %w", a.ID, err)
			}
			if uploader, ok := p.Provider.(providers.FileUploadProvider); ok {
				uploaded, err := uploader.UploadFile(ctx, providers.FileUploadRequest{
					Data:      payload.Data,
					Filename:  payload.Filename,
					MediaType: payload.MediaType,
					Purpose:   firstNonEmpty(str("purpose", "file_purpose"), "user_data"),
				})
				if err != nil {
					return providers.ContentPart{}, err
				}
				fileID = uploaded.ID
			} else {
				fileData = payload.DataURI()
			}
		}
		return providers.ContentPart{Type: "file", File: &providers.FileContent{Filename: str("filename"), FileData: fileData, FileID: fileID}}, nil
	case "audio":
		audio := str("data", "base64")
		if audio == "" && strings.HasPrefix(storageRef, "data:") {
			audio = storageRef
		}
		format := str("format")
		if format == "" {
			format = mediaFormat(firstNonEmpty(str("media_type"), a.MediaType))
		}
		if audio == "" || format == "" {
			return providers.ContentPart{}, fmt.Errorf("audio artifact %s requires base64 data/data URI and format", a.ID)
		}
		return providers.ContentPart{Type: "input_audio", InputAudio: &providers.InputAudioContent{Data: audio, Format: format}}, nil
	case "video":
		url := firstNonEmpty(str("url", "video_url", "data_uri"), storageRef)
		if url == "" {
			return providers.ContentPart{}, fmt.Errorf("video artifact %s requires storage_ref, metadata.url, or metadata.data_uri", a.ID)
		}
		return providers.ContentPart{Type: "video_url", VideoURL: &providers.VideoURLContent{URL: url}}, nil
	default:
		return providers.ContentPart{}, fmt.Errorf("unsupported artifact modality %q", a.Modality)
	}
}

type artifactPayload struct {
	Data      []byte
	MediaType string
	Filename  string
}

func (p artifactPayload) DataURI() string {
	mediaType := strings.TrimSpace(p.MediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)
}

func (p ProviderProcessor) artifactPayload(ctx context.Context, a Artifact) (artifactPayload, error) {
	data := a.Metadata
	if data == nil {
		data = map[string]any{}
	}
	mediaType := firstNonEmpty(metadataString(data, "media_type", "content_type"), a.MediaType, "application/octet-stream")
	filename := firstNonEmpty(metadataString(data, "filename", "name"), filenameFromRef(a.StorageRef), a.ID+extensionFromMediaType(mediaType))
	for _, key := range []string{"data_uri", "file_data"} {
		if raw := metadataString(data, key); strings.HasPrefix(raw, "data:") {
			payload, err := parseDataURI(raw)
			if err != nil {
				return artifactPayload{}, err
			}
			if payload.MediaType == "" {
				payload.MediaType = mediaType
			}
			if payload.Filename == "" {
				payload.Filename = filename
			}
			return payload, nil
		}
	}
	if strings.HasPrefix(a.StorageRef, "data:") {
		payload, err := parseDataURI(a.StorageRef)
		if err != nil {
			return artifactPayload{}, err
		}
		if payload.MediaType == "" {
			payload.MediaType = mediaType
		}
		payload.Filename = filename
		return payload, nil
	}
	if raw := metadataString(data, "base64", "data"); raw != "" && !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return artifactPayload{}, err
		}
		return artifactPayload{Data: decoded, MediaType: mediaType, Filename: filename}, nil
	}
	ref := firstNonEmpty(metadataString(data, "url", "document_url", "audio_url"), a.StorageRef)
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return p.fetchPayload(ctx, ref, mediaType, filename)
	}
	return artifactPayload{}, fmt.Errorf("no inline data or fetchable URL")
}

func (p ProviderProcessor) fetchPayload(ctx context.Context, rawURL, fallbackMediaType, fallbackFilename string) (artifactPayload, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return artifactPayload{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return artifactPayload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return artifactPayload{}, fmt.Errorf("fetch status %d", resp.StatusCode)
	}
	limit := p.MaxFetchBytes
	if limit <= 0 {
		limit = 25 * 1024 * 1024
	}
	reader := io.LimitReader(resp.Body, limit+1)
	raw, err := io.ReadAll(reader)
	if err != nil {
		return artifactPayload{}, err
	}
	if int64(len(raw)) > limit {
		return artifactPayload{}, fmt.Errorf("artifact exceeds fetch limit %d bytes", limit)
	}
	mediaType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if mediaType != "" {
		mediaType, _, _ = strings.Cut(mediaType, ";")
	}
	mediaType = firstNonEmpty(mediaType, fallbackMediaType, http.DetectContentType(raw))
	filename := firstNonEmpty(filenameFromContentDisposition(resp.Header.Get("Content-Disposition")), filenameFromRef(rawURL), fallbackFilename)
	return artifactPayload{Data: raw, MediaType: mediaType, Filename: filename}, nil
}

func parseDataURI(raw string) (artifactPayload, error) {
	header, encoded, ok := strings.Cut(raw, ",")
	if !ok || !strings.HasPrefix(header, "data:") {
		return artifactPayload{}, fmt.Errorf("invalid data URI")
	}
	mediaType := strings.TrimPrefix(header, "data:")
	base64Encoded := false
	if before, _, ok := strings.Cut(mediaType, ";"); ok {
		base64Encoded = strings.Contains(mediaType, ";base64")
		mediaType = before
	}
	var data []byte
	var err error
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(encoded)
	} else {
		data, err = io.ReadAll(strings.NewReader(encoded))
	}
	if err != nil {
		return artifactPayload{}, err
	}
	return artifactPayload{Data: data, MediaType: mediaType}, nil
}

func metadataString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, _ := data[key].(string); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func filenameFromRef(ref string) string {
	parsed, err := url.Parse(ref)
	if err == nil {
		ref = parsed.Path
	}
	name := path.Base(ref)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func filenameFromContentDisposition(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return params["filename"]
}

func extensionFromMediaType(mediaType string) string {
	extensions, err := mime.ExtensionsByType(mediaType)
	if err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mediaFormat(mediaType string) string {
	if mediaType == "" {
		return ""
	}
	if _, suffix, ok := strings.Cut(mediaType, "/"); ok {
		return suffix
	}
	return mediaType
}
