package providers

import (
	"context"
	"errors"
	"testing"
)

func TestResolveCandidatesDeduplicatesAndNormalizes(t *testing.T) {
	got := ResolveCandidates(ModelConfig{
		Primary:   "gpt/gpt-5",
		Fallbacks: []string{"openai/gpt-5", "claude/sonnet", "sonnet"},
	}, "anthropic")
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2: %#v", len(got), got)
	}
	if got[0].Provider != "openai" || got[0].Model != "gpt-5" {
		t.Fatalf("first=%#v", got[0])
	}
	if got[1].Provider != "anthropic" || got[1].Model != "sonnet" {
		t.Fatalf("second=%#v", got[1])
	}
}

func TestExecuteFallbackTriesNextCandidate(t *testing.T) {
	candidates := []FallbackCandidate{{Provider: "a", Model: "m1"}, {Provider: "b", Model: "m2"}}
	attempts := 0
	res, err := ExecuteFallback(context.Background(), candidates, func(_ context.Context, provider, model string) (*LLMResponse, error) {
		attempts++
		if provider == "a" {
			return nil, errors.New("boom")
		}
		return &LLMResponse{Content: model}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || res.Provider != "b" || res.Response.Content != "m2" {
		t.Fatalf("unexpected result attempts=%d res=%#v", attempts, res)
	}
}

func TestMockProviderCanRequestAndConsumeTool(t *testing.T) {
	p := MockProvider{}
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "please [[use_echo:hello]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "echo" {
		t.Fatalf("expected echo tool call, got %#v", resp)
	}
	resp, err = p.Chat(context.Background(), []Message{
		{Role: "user", Content: "please [[use_echo:hello]]"},
		{Role: "tool", Content: "echo: hello"},
	}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "stop" || resp.Content == "" {
		t.Fatalf("expected final response, got %#v", resp)
	}
}

func TestMockProviderCanRequestMemoryTools(t *testing.T) {
	p := MockProvider{}
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "[[remember:likes tea]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "remember" {
		t.Fatalf("resp=%#v", resp)
	}
	resp, err = p.Chat(context.Background(), []Message{{Role: "user", Content: "[[list_memories]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "list_memories" {
		t.Fatalf("resp=%#v", resp)
	}
}
