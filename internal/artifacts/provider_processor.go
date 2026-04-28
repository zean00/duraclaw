package artifacts

import (
	"context"
	"fmt"
	"strings"

	"duraclaw/internal/providers"
)

type ProviderProcessor struct {
	NameValue       string
	Provider        providers.LLMProvider
	Model           string
	Modalities      map[string]bool
	MediaTypes      map[string]bool
	MaxSummaryBytes int
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
	part, err := p.contentPart(a)
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

func (p ProviderProcessor) contentPart(a Artifact) (providers.ContentPart, error) {
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
			return providers.ContentPart{}, fmt.Errorf("document artifact %s requires inline metadata.file_data, metadata.data_uri, data URI storage_ref, or metadata.file_id", a.ID)
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
