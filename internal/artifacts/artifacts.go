package artifacts

import "context"

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
