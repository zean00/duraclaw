package artifacts

import (
	"context"
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
