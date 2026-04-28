package providers

import (
	"context"
	"fmt"
)

type Registry struct {
	providers       map[string]LLMProvider
	defaultProvider string
}

func NewRegistry(defaultProvider string) *Registry {
	if defaultProvider == "" {
		defaultProvider = "mock"
	}
	return &Registry{providers: map[string]LLMProvider{}, defaultProvider: NormalizeProvider(defaultProvider)}
}

func (r *Registry) Register(name string, provider LLMProvider) {
	if r.providers == nil {
		r.providers = map[string]LLMProvider{}
	}
	r.providers[NormalizeProvider(name)] = provider
}

func (r *Registry) Get(name string) (LLMProvider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.providers[NormalizeProvider(name)]
	return p, ok
}

func (r *Registry) DefaultProvider() string {
	if r == nil || r.defaultProvider == "" {
		return "mock"
	}
	return r.defaultProvider
}

func (r *Registry) ChatWithFallback(
	ctx context.Context,
	cfg ModelConfig,
	messages []Message,
	tools []ToolDefinition,
	options map[string]any,
) (*FallbackResult, error) {
	candidates := ResolveCandidates(cfg, r.DefaultProvider())
	if len(candidates) == 0 {
		if p, ok := r.Get(r.DefaultProvider()); ok {
			candidates = []FallbackCandidate{{Provider: r.DefaultProvider(), Model: p.GetDefaultModel()}}
		}
	}
	return ExecuteFallback(ctx, candidates, func(ctx context.Context, providerName, model string) (*LLMResponse, error) {
		provider, ok := r.Get(providerName)
		if !ok {
			return nil, fmt.Errorf("provider %q is not registered", providerName)
		}
		return provider.Chat(ctx, messages, tools, model, options)
	})
}
