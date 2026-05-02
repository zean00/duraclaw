package providers

import (
	"context"
	"errors"
	"testing"
)

type failingProvider struct{}

func (failingProvider) GetDefaultModel() string { return "fail" }
func (failingProvider) Chat(context.Context, []Message, []ToolDefinition, string, map[string]any) (*LLMResponse, error) {
	return nil, errors.New("fail")
}

type captureOptionsProvider struct {
	options map[string]any
}

func (p *captureOptionsProvider) GetDefaultModel() string { return "capture" }
func (p *captureOptionsProvider) Chat(_ context.Context, _ []Message, _ []ToolDefinition, _ string, options map[string]any) (*LLMResponse, error) {
	p.options = options
	return &LLMResponse{Content: "ok"}, nil
}

func TestRegistryChatWithFallback(t *testing.T) {
	reg := NewRegistry("mock")
	reg.Register("fail", failingProvider{})
	reg.Register("mock", MockProvider{})
	res, err := reg.ChatWithFallback(t.Context(), ModelConfig{
		Primary:   "fail/bad",
		Fallbacks: []string{"mock/good"},
	}, []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "mock" || len(res.Attempts) != 1 {
		t.Fatalf("res=%#v", res)
	}
}

func TestRegistryChatWithFallbackMergesModelConfigOptions(t *testing.T) {
	provider := &captureOptionsProvider{}
	reg := NewRegistry("capture")
	reg.Register("capture", provider)

	_, err := reg.ChatWithFallback(t.Context(), ModelConfig{
		Primary: "capture/model",
		Options: map[string]any{
			"max_tokens": 256,
			"purpose":    "default",
		},
	}, []Message{{Role: "user", Content: "hi"}}, nil, map[string]any{
		"purpose":         "scope_judge",
		"response_format": "json_object",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.options["max_tokens"] != 256 {
		t.Fatalf("expected model config option to be passed, got %#v", provider.options)
	}
	if provider.options["response_format"] != "json_object" {
		t.Fatalf("expected call option to be passed, got %#v", provider.options)
	}
	if provider.options["purpose"] != "scope_judge" {
		t.Fatalf("expected call option to override config option, got %#v", provider.options)
	}
}
