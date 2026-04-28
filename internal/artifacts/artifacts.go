package artifacts

import (
	"context"
	"fmt"
)

type ProcessorContext struct {
	CustomerID      string
	UserID          string
	AgentInstanceID string
	SessionID       string
	RunID           string
	ArtifactID      string
	RequestID       string
}

type Artifact struct {
	ID         string
	Modality   string
	MediaType  string
	StorageRef string
	Metadata   map[string]any
}

type Representation struct {
	Type     string
	Summary  string
	Metadata map[string]any
}

type Processor interface {
	Name() string
	CanProcess(a Artifact) bool
	Process(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, error)
}

type ProviderAdapter struct {
	NameValue   string
	Modalities  map[string]bool
	MediaTypes  map[string]bool
	ProcessFunc func(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, error)
}

func (p ProviderAdapter) Name() string {
	if p.NameValue == "" {
		return "provider_adapter"
	}
	return p.NameValue
}

func (p ProviderAdapter) CanProcess(a Artifact) bool {
	if len(p.Modalities) > 0 && !p.Modalities[a.Modality] {
		return false
	}
	if len(p.MediaTypes) > 0 && !p.MediaTypes[a.MediaType] {
		return false
	}
	return p.ProcessFunc != nil
}

func (p ProviderAdapter) Process(ctx context.Context, exec ProcessorContext, a Artifact) ([]Representation, error) {
	if p.ProcessFunc == nil {
		return nil, fmt.Errorf("artifact provider adapter %q has no process function", p.Name())
	}
	return p.ProcessFunc(ctx, exec, a)
}

type Registry struct {
	processors []Processor
}

func NewRegistry(processors ...Processor) *Registry {
	return &Registry{processors: append([]Processor(nil), processors...)}
}

func (r *Registry) Register(processor Processor) {
	if processor != nil {
		r.processors = append(r.processors, processor)
	}
}

func (r *Registry) ProcessorFor(a Artifact) (Processor, bool) {
	if r == nil {
		return nil, false
	}
	for _, processor := range r.processors {
		if processor.CanProcess(a) {
			return processor, true
		}
	}
	return nil, false
}

type MockProcessor struct{}

func (MockProcessor) Name() string { return "mock_processor" }
func (MockProcessor) CanProcess(a Artifact) bool {
	return a.Modality == "audio" || a.Modality == "image" || a.Modality == "document"
}
func (MockProcessor) Process(ctx context.Context, _ ProcessorContext, a Artifact) ([]Representation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []Representation{{Type: "metadata_summary", Summary: "Mock representation for " + a.Modality, Metadata: map[string]any{"media_type": a.MediaType}}}, nil
}
