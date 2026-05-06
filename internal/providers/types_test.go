package providers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
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

func TestMessageMarshalsToolCallsAndToolCallID(t *testing.T) {
	raw, err := json.Marshal(Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: FunctionCall{
				Name:      "list_memories",
				Arguments: map[string]any{"limit": 3},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var assistant map[string]any
	if err := json.Unmarshal(raw, &assistant); err != nil {
		t.Fatal(err)
	}
	if _, ok := assistant["tool_calls"].([]any); !ok {
		t.Fatalf("assistant tool calls missing: %s", raw)
	}
	call := assistant["tool_calls"].([]any)[0].(map[string]any)
	fn := call["function"].(map[string]any)
	if _, ok := fn["arguments"].(string); !ok {
		t.Fatalf("function arguments must marshal as JSON string: %s", raw)
	}
	raw, err = json.Marshal(Message{Role: "tool", Content: "ok", ToolCallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	var tool map[string]any
	if err := json.Unmarshal(raw, &tool); err != nil {
		t.Fatal(err)
	}
	if tool["tool_call_id"] != "call-1" || tool["content"] != "ok" {
		t.Fatalf("tool response malformed: %s", raw)
	}
}

func TestFunctionCallUnmarshalsStringAndObjectArguments(t *testing.T) {
	for _, raw := range []string{
		`{"name":"list_memories","arguments":"{\"limit\":3}"}`,
		`{"name":"list_memories","arguments":{"limit":3}}`,
	} {
		var call FunctionCall
		if err := json.Unmarshal([]byte(raw), &call); err != nil {
			t.Fatal(err)
		}
		if call.Name != "list_memories" || call.Arguments["limit"] == nil {
			t.Fatalf("call=%#v", call)
		}
	}
}

func TestFunctionCallMarshalsNilArgumentsAsEmptyObjectString(t *testing.T) {
	raw, err := json.Marshal(FunctionCall{Name: "list_memories"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["arguments"] != "{}" {
		t.Fatalf("arguments=%#v raw=%s", payload["arguments"], raw)
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
	if got := NormalizeProvider(" together-ai "); got != "together" {
		t.Fatalf("provider=%q", got)
	}
	if got := NormalizeProvider(" deepseek-ai "); got != "deepseek" {
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
	res, err := ExecuteFallback(context.Background(), []FallbackCandidate{{Provider: "a", Model: "m"}}, func(context.Context, string, string) (*LLMResponse, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatalf("expected all failed error")
	}
	if res == nil || len(res.Attempts) != 1 || res.Attempts[0].Error == nil {
		t.Fatalf("expected failed attempt details, res=%#v", res)
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

func TestExecuteFallbackRetriesTransientCandidate(t *testing.T) {
	candidates := []FallbackCandidate{{Provider: "a", Model: "m"}}
	calls := 0
	res, err := ExecuteFallback(context.Background(), candidates, func(context.Context, string, string) (*LLMResponse, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("openai-compatible provider status 503: upstream overloaded")
		}
		return &LLMResponse{Content: "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(res.Attempts) != 1 || res.Attempts[0].Attempt != 1 {
		t.Fatalf("unexpected retry result calls=%d res=%#v", calls, res)
	}
}

func TestExecuteFallbackCancelsDuringTransientBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	firstAttempt := make(chan struct{})
	go func() {
		<-firstAttempt
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	res, err := ExecuteFallback(ctx, []FallbackCandidate{{Provider: "a", Model: "m"}}, func(context.Context, string, string) (*LLMResponse, error) {
		calls++
		if calls == 1 {
			close(firstAttempt)
		}
		return nil, errors.New("openai-compatible provider status 503: upstream overloaded")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no retry after cancellation, calls=%d", calls)
	}
	if res == nil || len(res.Attempts) != 1 {
		t.Fatalf("expected first attempt to be recorded, res=%#v", res)
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
