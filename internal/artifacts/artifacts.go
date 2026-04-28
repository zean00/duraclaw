package artifacts

import (
	"context"
	"fmt"
)

type ProcessorContext struct {
	CustomerID               string   `json:"customer_id"`
	UserID                   string   `json:"user_id"`
	AgentInstanceID          string   `json:"agent_instance_id"`
	SessionID                string   `json:"session_id"`
	RunID                    string   `json:"run_id"`
	ArtifactID               string   `json:"artifact_id"`
	ProcessorCallID          string   `json:"processor_call_id"`
	RequestID                string   `json:"request_id"`
	TraceParent              string   `json:"traceparent,omitempty"`
	RequestedRepresentations []string `json:"requested_representations,omitempty"`
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

type ProcessorLimits struct {
	MaxResponseBytes   int  `json:"max_response_bytes,omitempty"`
	MaxRepresentations int  `json:"max_representations,omitempty"`
	MaxSummaryBytes    int  `json:"max_summary_bytes,omitempty"`
	RawMediaAllowed    bool `json:"raw_media_allowed"`
	DegradeOnOversize  bool `json:"degrade_on_oversize"`
	RetryNonIdempotent bool `json:"retry_non_idempotent"`
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
