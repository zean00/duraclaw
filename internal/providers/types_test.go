package providers

import (
	"context"
	"encoding/json"
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

func TestOpenAICompatibleProviderAliases(t *testing.T) {
	for _, raw := range []string{"openai_compatible", "openai-compatible", "local", "local-llm"} {
		if got := NormalizeProvider(raw); got != "openai-compatible" {
			t.Fatalf("%s normalized to %s", raw, got)
		}
	}
}

func TestMessageMarshalsContentParts(t *testing.T) {
	raw, err := json.Marshal(Message{Role: "user", ContentParts: []ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "image_url", ImageURL: &ImageURLContent{URL: "https://example.test/image.png"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["role"] != "user" {
		t.Fatalf("got=%#v", got)
	}
	parts, ok := got["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("got=%s", raw)
	}
}

func TestMessageTextUsesContentPartsWhenContentBlank(t *testing.T) {
	got := Message{ContentParts: []ContentPart{
		{Type: "image_url", ImageURL: &ImageURLContent{URL: "https://example.test/image.png"}},
		{Type: "text", Text: " first "},
		{Type: "text", Text: "second"},
	}}.Text()
	if got != " first \nsecond" {
		t.Fatalf("got %q", got)
	}
	if got := (Message{Content: " direct ", ContentParts: []ContentPart{{Type: "text", Text: "ignored"}}}).Text(); got != " direct " {
		t.Fatalf("got %q", got)
	}
}

func TestModelRefAndCandidateHelpers(t *testing.T) {
	if got := ParseModelRef("", "openai"); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
	if got := ParseModelRef("openai/", "mock"); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
	ref := ParseModelRef("model-only", "gpt")
	if ref == nil || ref.Provider != "openai" || ref.Model != "model-only" {
		t.Fatalf("ref=%#v", ref)
	}
	if got := NormalizeProvider(" GOOGLE "); got != "gemini" {
		t.Fatalf("provider=%q", got)
	}
	if got := (FallbackCandidate{Provider: "OpenAI", Model: " GPT-5 "}).StableKey(); got != "openai/gpt-5" {
		t.Fatalf("stable=%q", got)
	}
	if got := (FallbackCandidate{IdentityKey: "custom", Provider: "openai", Model: "gpt"}).StableKey(); got != "custom" {
		t.Fatalf("stable=%q", got)
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

func TestExecuteFallbackErrors(t *testing.T) {
	if _, err := ExecuteFallback(context.Background(), nil, nil); err == nil {
		t.Fatalf("expected empty candidates error")
	}
	_, err := ExecuteFallback(context.Background(), []FallbackCandidate{{Provider: "a", Model: "m"}}, func(context.Context, string, string) (*LLMResponse, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatalf("expected all failed error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ExecuteFallback(ctx, []FallbackCandidate{{Provider: "a", Model: "m"}}, func(context.Context, string, string) (*LLMResponse, error) {
		return nil, errors.New("boom")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestMockProviderDurableAndPreferenceTools(t *testing.T) {
	p := MockProvider{}
	resp, err := p.ChatDurable(context.Background(), CallMetadata{}, []Message{{Role: "user", Content: "[[prefer:no calls]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "save_preference" {
		t.Fatalf("resp=%#v", resp)
	}
	resp, err = p.Chat(context.Background(), []Message{{Role: "user", Content: "[[list_preferences]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "list_preferences" {
		t.Fatalf("resp=%#v", resp)
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

func TestMockProviderCanRequestInternalControlTools(t *testing.T) {
	p := MockProvider{}
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "[[workflow:wf-1]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "duraclaw.run_workflow" || resp.ToolCalls[0].Function.Arguments["workflow_id"] != "wf-1" {
		t.Fatalf("resp=%#v", resp)
	}
	resp, err = p.Chat(context.Background(), []Message{{Role: "user", Content: "[[ask_user:Need details?]]"}}, nil, p.GetDefaultModel(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Function.Name != "duraclaw.ask_user" {
		t.Fatalf("resp=%#v", resp)
	}
}
