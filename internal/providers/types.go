// Package providers contains LLM provider contracts adapted from PicoClaw.
//
// Portions are derived from PicoClaw's MIT-licensed provider abstractions:
// Copyright (c) Sipeed PicoClaw contributors.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Role         string        `json:"role"`
	Content      string        `json:"-"`
	ContentParts []ContentPart `json:"-"`
	ToolCalls    []ToolCall    `json:"-"`
	ToolCallID   string        `json:"-"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role       string     `json:"role"`
		Content    any        `json:"content,omitempty"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
	}
	if m.Role == "tool" {
		return json.Marshal(wire{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID})
	}
	if len(m.ContentParts) > 0 {
		return json.Marshal(wire{Role: m.Role, Content: m.ContentParts, ToolCalls: m.ToolCalls})
	}
	return json.Marshal(wire{Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls})
}

func (m Message) Text() string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	var out []string
	for _, part := range m.ContentParts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "\n")
}

type ContentPart struct {
	Type       string             `json:"type"`
	Text       string             `json:"text,omitempty"`
	ImageURL   *ImageURLContent   `json:"image_url,omitempty"`
	File       *FileContent       `json:"file,omitempty"`
	InputAudio *InputAudioContent `json:"input_audio,omitempty"`
	VideoURL   *VideoURLContent   `json:"video_url,omitempty"`
}

type ImageURLContent struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type FileContent struct {
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

type InputAudioContent struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type VideoURLContent struct {
	URL string `json:"url"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function"`
}

type ToolFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (f FunctionCall) MarshalJSON() ([]byte, error) {
	args := f.Arguments
	if args == nil {
		args = map[string]any{}
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	type wire struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	return json.Marshal(wire{Name: f.Name, Arguments: string(rawArgs)})
}

func (f *FunctionCall) UnmarshalJSON(raw []byte) error {
	var payload struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	f.Name = payload.Name
	switch args := payload.Arguments.(type) {
	case string:
		if strings.TrimSpace(args) == "" {
			f.Arguments = map[string]any{}
			return nil
		}
		if err := json.Unmarshal([]byte(args), &f.Arguments); err != nil {
			return err
		}
	case map[string]any:
		f.Arguments = args
	case nil:
		f.Arguments = map[string]any{}
	default:
		return fmt.Errorf("function arguments must be object or JSON string")
	}
	return nil
}

type UsageInfo struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
	CostMicros   int `json:"cost_micros,omitempty"`
}

func (u *UsageInfo) UnmarshalJSON(raw []byte) error {
	type usageAlias UsageInfo
	var payload struct {
		usageAlias
		PromptTokens     int `json:"prompt_tokens,omitempty"`
		CompletionTokens int `json:"completion_tokens,omitempty"`
		Cost             any `json:"cost,omitempty"`
		TotalCost        any `json:"total_cost,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	*u = UsageInfo(payload.usageAlias)
	if u.InputTokens == 0 {
		u.InputTokens = payload.PromptTokens
	}
	if u.OutputTokens == 0 {
		u.OutputTokens = payload.CompletionTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	if u.CostMicros == 0 {
		u.CostMicros = costMicros(payload.Cost)
	}
	if u.CostMicros == 0 {
		u.CostMicros = costMicros(payload.TotalCost)
	}
	return nil
}

func costMicros(raw any) int {
	switch v := raw.(type) {
	case float64:
		return int(v*1_000_000 + 0.5)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		return int(f*1_000_000 + 0.5)
	default:
		return 0
	}
}

type LLMResponse struct {
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Usage        UsageInfo  `json:"usage,omitempty"`
}

type StreamDelta struct {
	Content        string          `json:"content,omitempty"`
	ToolCalls      []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallDeltas []ToolCallDelta `json:"tool_call_deltas,omitempty"`
	FinishReason   string          `json:"finish_reason,omitempty"`
	Usage          UsageInfo       `json:"usage,omitempty"`
}

type ToolCallDelta struct {
	Index             int    `json:"index"`
	ID                string `json:"id,omitempty"`
	Type              string `json:"type,omitempty"`
	FunctionName      string `json:"function_name,omitempty"`
	FunctionArguments string `json:"function_arguments,omitempty"`
}

type CallMetadata struct {
	CustomerID      string
	UserID          string
	AgentInstanceID string
	SessionID       string
	RunID           string
	RequestID       string
	Provider        string
	Model           string
}

type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error)
	GetDefaultModel() string
}

type DurableProvider interface {
	LLMProvider
	ChatDurable(ctx context.Context, meta CallMetadata, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error)
}

type StreamingProvider interface {
	LLMProvider
	ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (<-chan StreamDelta, error)
}

type AudioTranscriptionRequest struct {
	Data           []byte
	Filename       string
	MediaType      string
	Model          string
	Language       string
	Prompt         string
	ResponseFormat string
}

type AudioTranscriptionResult struct {
	Text     string
	Usage    UsageInfo
	Metadata map[string]any
}

type AudioTranscriptionProvider interface {
	TranscribeAudio(ctx context.Context, req AudioTranscriptionRequest) (*AudioTranscriptionResult, error)
}

type FileUploadRequest struct {
	Data      []byte
	Filename  string
	MediaType string
	Purpose   string
}

type FileUploadResult struct {
	ID       string
	Filename string
	Purpose  string
	Bytes    int64
	Metadata map[string]any
}

type FileUploadProvider interface {
	UploadFile(ctx context.Context, req FileUploadRequest) (*FileUploadResult, error)
}

type ImageGenerationRequest struct {
	Prompt         string
	Model          string
	Size           string
	Quality        string
	Background     string
	ResponseFormat string
	Count          int
}

type GeneratedImage struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageGenerationResult struct {
	Images []GeneratedImage
	Usage  UsageInfo
}

type ImageGenerator interface {
	GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error)
}

type AudioGenerationRequest struct {
	Text           string
	Model          string
	Voice          string
	Instructions   string
	ResponseFormat string
	Speed          float64
}

type AudioGenerationResult struct {
	Data      []byte
	MediaType string
}

type AudioGenerator interface {
	GenerateAudio(ctx context.Context, req AudioGenerationRequest) (*AudioGenerationResult, error)
}

type VideoGenerationRequest struct {
	Prompt  string
	Model   string
	Size    string
	Seconds string
	Count   int
}

type GeneratedVideo struct {
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Model    string `json:"model,omitempty"`
	Progress int    `json:"progress,omitempty"`
	Seconds  string `json:"seconds,omitempty"`
	Size     string `json:"size,omitempty"`
	URL      string `json:"url,omitempty"`
	B64JSON  string `json:"b64_json,omitempty"`
}

type VideoGenerationResult struct {
	Videos []GeneratedVideo
}

type VideoGenerator interface {
	GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error)
}

type MockProvider struct{}

func (MockProvider) GetDefaultModel() string { return "mock/duraclaw" }

func (MockProvider) Chat(ctx context.Context, messages []Message, _ []ToolDefinition, model string, _ map[string]any) (*LLMResponse, error) {
	var last string
	var toolResult string
	for i := len(messages) - 1; i >= 0; i-- {
		text := messages[i].Text()
		if messages[i].Role == "tool" && strings.TrimSpace(text) != "" {
			toolResult = text
			break
		}
		if messages[i].Role == "user" {
			last = text
		}
	}
	if toolResult != "" {
		return &LLMResponse{Content: "Mock response after tool: " + toolResult, FinishReason: "stop"}, nil
	}
	if strings.TrimSpace(last) == "" {
		last = "I received your request."
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(last, "[[use_echo:") {
		return mockToolCall(last, "[[use_echo:", "echo", "message"), nil
	}
	if strings.Contains(last, "[[remember:") {
		return mockToolCall(last, "[[remember:", "remember", "content"), nil
	}
	if strings.Contains(last, "[[prefer:") {
		return mockToolCall(last, "[[prefer:", "save_preference", "content"), nil
	}
	if strings.Contains(last, "[[workflow:") {
		return mockToolCall(last, "[[workflow:", "duraclaw.run_workflow", "workflow_id"), nil
	}
	if strings.Contains(last, "[[ask_user:") {
		return mockToolCall(last, "[[ask_user:", "duraclaw.ask_user", "question"), nil
	}
	if strings.Contains(last, "[[list_memories]]") {
		return &LLMResponse{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []ToolCall{{
				ID:   "mock-tool-call-1",
				Type: "function",
				Function: FunctionCall{
					Name:      "list_memories",
					Arguments: map[string]any{},
				},
			}},
		}, nil
	}
	if strings.Contains(last, "[[list_preferences]]") {
		return &LLMResponse{
			Content:      "",
			FinishReason: "tool_calls",
			ToolCalls: []ToolCall{{
				ID:   "mock-tool-call-1",
				Type: "function",
				Function: FunctionCall{
					Name:      "list_preferences",
					Arguments: map[string]any{},
				},
			}},
		}, nil
	}
	return &LLMResponse{Content: "Mock response: " + last, FinishReason: "stop"}, nil
}

func mockToolCall(last, marker, name, argName string) *LLMResponse {
	start := strings.Index(last, marker) + len(marker)
	end := strings.Index(last[start:], "]]")
	args := map[string]any{}
	if end >= 0 {
		args[argName] = last[start : start+end]
	}
	return &LLMResponse{
		Content:      "",
		FinishReason: "tool_calls",
		ToolCalls: []ToolCall{{
			ID:   "mock-tool-call-1",
			Type: "function",
			Function: FunctionCall{
				Name:      name,
				Arguments: args,
			},
		}},
	}
}

func (m MockProvider) ChatDurable(ctx context.Context, _ CallMetadata, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return m.Chat(ctx, messages, tools, model, options)
}

type ModelConfig struct {
	Primary   string
	Fallbacks []string
}

type ModelRef struct {
	Provider string
	Model    string
}

func ParseModelRef(raw string, defaultProvider string) *ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if idx := strings.Index(raw, "/"); idx > 0 {
		model := strings.TrimSpace(raw[idx+1:])
		if model == "" {
			return nil
		}
		return &ModelRef{Provider: NormalizeProvider(raw[:idx]), Model: model}
	}
	return &ModelRef{Provider: NormalizeProvider(defaultProvider), Model: raw}
}

func NormalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "google":
		return "gemini"
	case "openai_compatible", "openai-compatible", "local", "local-llm":
		return "openai-compatible"
	}
	return p
}

func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}

type FallbackCandidate struct {
	Provider    string
	Model       string
	IdentityKey string
}

func (c FallbackCandidate) StableKey() string {
	if strings.TrimSpace(c.IdentityKey) != "" {
		return c.IdentityKey
	}
	return ModelKey(c.Provider, c.Model)
}

func ResolveCandidates(cfg ModelConfig, defaultProvider string) []FallbackCandidate {
	seen := map[string]bool{}
	var out []FallbackCandidate
	add := func(raw string) {
		ref := ParseModelRef(raw, defaultProvider)
		if ref == nil {
			return
		}
		key := ModelKey(ref.Provider, ref.Model)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, FallbackCandidate{Provider: ref.Provider, Model: ref.Model})
	}
	add(cfg.Primary)
	for _, fb := range cfg.Fallbacks {
		add(fb)
	}
	return out
}

type FallbackAttempt struct {
	Provider string
	Model    string
	Error    error
	Duration time.Duration
}

type FallbackResult struct {
	Response *LLMResponse
	Provider string
	Model    string
	Attempts []FallbackAttempt
}

func ExecuteFallback(ctx context.Context, candidates []FallbackCandidate, run func(context.Context, string, string) (*LLMResponse, error)) (*FallbackResult, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("fallback: no candidates configured")
	}
	res := &FallbackResult{Attempts: make([]FallbackAttempt, 0, len(candidates))}
	for _, c := range candidates {
		start := time.Now()
		resp, err := run(ctx, c.Provider, c.Model)
		elapsed := time.Since(start)
		if err == nil {
			res.Response, res.Provider, res.Model = resp, c.Provider, c.Model
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res.Attempts = append(res.Attempts, FallbackAttempt{Provider: c.Provider, Model: c.Model, Error: err, Duration: elapsed})
	}
	return nil, fmt.Errorf("fallback: all %d candidates failed", len(res.Attempts))
}
