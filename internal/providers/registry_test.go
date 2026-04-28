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
