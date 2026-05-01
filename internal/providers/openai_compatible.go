package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type OpenAICompatibleProvider struct {
	BaseURL      string
	APIKey       string
	DefaultModel string
	Headers      map[string]string
	HTTPClient   *http.Client
}

func (p OpenAICompatibleProvider) GetDefaultModel() string {
	if strings.TrimSpace(p.DefaultModel) != "" {
		return p.DefaultModel
	}
	return "gpt-4.1-mini"
}

func (p OpenAICompatibleProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	resp, err := p.chatRequest(ctx, messages, tools, model, options, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Choices []struct {
			Message struct {
				Content   any        `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage UsageInfo `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Choices) == 0 {
		return nil, fmt.Errorf("openai-compatible provider returned no choices")
	}
	choice := payload.Choices[0]
	return &LLMResponse{
		Content:      responseContentText(choice.Message.Content),
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Usage:        payload.Usage,
	}, nil
}

func (p OpenAICompatibleProvider) ChatDurable(ctx context.Context, _ CallMetadata, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.Chat(ctx, messages, tools, model, options)
}

func (p OpenAICompatibleProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (<-chan StreamDelta, error) {
	resp, err := p.chatRequest(ctx, messages, tools, model, options, true)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamDelta)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}
			delta, ok := parseStreamDelta([]byte(data))
			if !ok {
				continue
			}
			select {
			case out <- delta:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (p OpenAICompatibleProvider) chatRequest(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any, stream bool) (*http.Response, error) {
	if strings.TrimSpace(model) == "" {
		model = p.GetDefaultModel()
	}
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	for k, v := range options {
		body[k] = v
	}
	if stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for key, value := range p.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errPayload)
		if err := resp.Body.Close(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("openai-compatible provider status %d: %v", resp.StatusCode, errPayload)
	}
	return resp, nil
}

func parseStreamDelta(raw []byte) (StreamDelta, bool) {
	var payload struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage UsageInfo `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return StreamDelta{}, false
	}
	delta := StreamDelta{Usage: payload.Usage}
	if len(payload.Choices) > 0 {
		choice := payload.Choices[0]
		delta.Content = choice.Delta.Content
		delta.FinishReason = choice.FinishReason
		for _, call := range choice.Delta.ToolCalls {
			delta.ToolCallDeltas = append(delta.ToolCallDeltas, ToolCallDelta{
				Index:             call.Index,
				ID:                call.ID,
				Type:              call.Type,
				FunctionName:      call.Function.Name,
				FunctionArguments: call.Function.Arguments,
			})
		}
	}
	return delta, delta.Content != "" || len(delta.ToolCalls) > 0 || len(delta.ToolCallDeltas) > 0 || delta.FinishReason != "" || delta.Usage.TotalTokens > 0
}

func (p OpenAICompatibleProvider) TranscribeAudio(ctx context.Context, req AudioTranscriptionRequest) (*AudioTranscriptionResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("audio transcription requires data")
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-4o-mini-transcribe"
	}
	if strings.TrimSpace(req.ResponseFormat) == "" {
		req.ResponseFormat = "json"
	}
	fields := map[string]string{
		"model":           req.Model,
		"response_format": req.ResponseFormat,
	}
	if strings.TrimSpace(req.Language) != "" {
		fields["language"] = req.Language
	}
	if strings.TrimSpace(req.Prompt) != "" {
		fields["prompt"] = req.Prompt
	}
	resp, err := p.multipartRequest(ctx, "/audio/transcriptions", "file", req.Filename, req.MediaType, req.Data, fields)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if req.ResponseFormat == "text" || req.ResponseFormat == "srt" || req.ResponseFormat == "vtt" {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return &AudioTranscriptionResult{Text: strings.TrimSpace(string(raw))}, nil
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	text, _ := payload["text"].(string)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("audio transcription returned no text")
	}
	return &AudioTranscriptionResult{Text: text, Metadata: payload}, nil
}

func (p OpenAICompatibleProvider) UploadFile(ctx context.Context, req FileUploadRequest) (*FileUploadResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("file upload requires data")
	}
	purpose := strings.TrimSpace(req.Purpose)
	if purpose == "" {
		purpose = "user_data"
	}
	resp, err := p.multipartRequest(ctx, "/files", "file", req.Filename, req.MediaType, req.Data, map[string]string{"purpose": purpose})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Purpose  string `json:"purpose"`
		Bytes    int64  `json:"bytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.ID) == "" {
		return nil, fmt.Errorf("file upload returned no id")
	}
	return &FileUploadResult{ID: payload.ID, Filename: payload.Filename, Purpose: payload.Purpose, Bytes: payload.Bytes}, nil
}

func (p OpenAICompatibleProvider) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("image generation requires prompt")
	}
	body := map[string]any{"prompt": req.Prompt}
	if strings.TrimSpace(req.Model) != "" {
		body["model"] = req.Model
	}
	if strings.TrimSpace(req.Size) != "" {
		body["size"] = req.Size
	}
	if strings.TrimSpace(req.Quality) != "" {
		body["quality"] = req.Quality
	}
	if strings.TrimSpace(req.Background) != "" {
		body["background"] = req.Background
	}
	if strings.TrimSpace(req.ResponseFormat) != "" {
		body["response_format"] = req.ResponseFormat
	}
	if req.Count > 0 {
		body["n"] = req.Count
	}
	resp, err := p.jsonRequest(ctx, "/images/generations", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Data  []GeneratedImage `json:"data"`
		Usage UsageInfo        `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Data) == 0 {
		return nil, fmt.Errorf("image generation returned no images")
	}
	return &ImageGenerationResult{Images: payload.Data, Usage: payload.Usage}, nil
}

func (p OpenAICompatibleProvider) GenerateAudio(ctx context.Context, req AudioGenerationRequest) (*AudioGenerationResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("audio generation requires text")
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-4o-mini-tts"
	}
	if strings.TrimSpace(req.Voice) == "" {
		req.Voice = "alloy"
	}
	if strings.TrimSpace(req.ResponseFormat) == "" {
		req.ResponseFormat = "mp3"
	}
	body := map[string]any{"model": req.Model, "input": req.Text, "voice": req.Voice, "response_format": req.ResponseFormat}
	if strings.TrimSpace(req.Instructions) != "" {
		body["instructions"] = req.Instructions
	}
	if req.Speed > 0 {
		body["speed"] = req.Speed
	}
	resp, err := p.jsonRequest(ctx, "/audio/speech", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	mediaType := resp.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = audioMediaType(req.ResponseFormat)
	}
	return &AudioGenerationResult{Data: raw, MediaType: mediaType}, nil
}

func (p OpenAICompatibleProvider) GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("video generation requires prompt")
	}
	fields := map[string]string{"prompt": req.Prompt}
	if strings.TrimSpace(req.Model) != "" {
		fields["model"] = req.Model
	}
	if strings.TrimSpace(req.Size) != "" {
		fields["size"] = req.Size
	}
	if strings.TrimSpace(req.Seconds) != "" {
		fields["seconds"] = req.Seconds
	}
	if req.Count > 0 {
		fields["count"] = strconv.Itoa(req.Count)
	}
	resp, err := p.multipartFieldsRequest(ctx, "/videos", fields)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload GeneratedVideo
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.ID) == "" {
		return nil, fmt.Errorf("video generation returned no id")
	}
	return &VideoGenerationResult{Videos: []GeneratedVideo{payload}}, nil
}

func (p OpenAICompatibleProvider) multipartFieldsRequest(ctx context.Context, pathName string, fields map[string]string) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if strings.TrimSpace(value) != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := p.newRequest(ctx, http.MethodPost, pathName, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return p.do(req)
}

func (p OpenAICompatibleProvider) multipartRequest(ctx context.Context, pathName, fileField, filename, mediaType string, data []byte, fields map[string]string) (*http.Response, error) {
	if strings.TrimSpace(filename) == "" {
		filename = "artifact" + extensionForMediaType(mediaType)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeQuotes(fileField), escapeQuotes(filepath.Base(filename))))
	if strings.TrimSpace(mediaType) != "" {
		partHeader.Set("Content-Type", mediaType)
	}
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	for key, value := range fields {
		if strings.TrimSpace(value) != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := p.newRequest(ctx, http.MethodPost, pathName, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return p.do(req)
}

func (p OpenAICompatibleProvider) jsonRequest(ctx context.Context, pathName string, body map[string]any) (*http.Response, error) {
	raw, _ := json.Marshal(body)
	req, err := p.newRequest(ctx, http.MethodPost, pathName, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.do(req)
}

func (p OpenAICompatibleProvider) newRequest(ctx context.Context, method, pathName string, body io.Reader) (*http.Request, error) {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+pathName, body)
	if err != nil {
		return nil, err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for key, value := range p.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	return req, nil
}

func (p OpenAICompatibleProvider) do(req *http.Request) (*http.Response, error) {
	client := p.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errPayload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errPayload)
		if err := resp.Body.Close(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("openai-compatible provider status %d: %v", resp.StatusCode, errPayload)
	}
	return resp, nil
}

func escapeQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(value)
}

func extensionForMediaType(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/mp4":
		return ".mp4"
	case "audio/webm":
		return ".webm"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	default:
		return ""
	}
}

func audioMediaType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/L16"
	default:
		return "audio/mpeg"
	}
}

func responseContentText(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return strings.Join(out, "\n")
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

type OpenAIProvider struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	HTTPClient   *http.Client
}

func (p OpenAIProvider) GetDefaultModel() string {
	return p.compatible().GetDefaultModel()
}

func (p OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().Chat(ctx, messages, tools, model, options)
}

func (p OpenAIProvider) ChatDurable(ctx context.Context, meta CallMetadata, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().ChatDurable(ctx, meta, messages, tools, model, options)
}

func (p OpenAIProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (<-chan StreamDelta, error) {
	return p.compatible().ChatStream(ctx, messages, tools, model, options)
}

func (p OpenAIProvider) TranscribeAudio(ctx context.Context, req AudioTranscriptionRequest) (*AudioTranscriptionResult, error) {
	return p.compatible().TranscribeAudio(ctx, req)
}

func (p OpenAIProvider) UploadFile(ctx context.Context, req FileUploadRequest) (*FileUploadResult, error) {
	return p.compatible().UploadFile(ctx, req)
}

func (p OpenAIProvider) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	return p.compatible().GenerateImage(ctx, req)
}

func (p OpenAIProvider) GenerateAudio(ctx context.Context, req AudioGenerationRequest) (*AudioGenerationResult, error) {
	return p.compatible().GenerateAudio(ctx, req)
}

func (p OpenAIProvider) GenerateVideo(ctx context.Context, req VideoGenerationRequest) (*VideoGenerationResult, error) {
	return p.compatible().GenerateVideo(ctx, req)
}

func (p OpenAIProvider) compatible() OpenAICompatibleProvider {
	baseURL := p.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return OpenAICompatibleProvider{
		BaseURL:      baseURL,
		APIKey:       p.APIKey,
		DefaultModel: p.DefaultModel,
		HTTPClient:   p.HTTPClient,
	}
}

type OpenRouterProvider struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
	Referer      string
	Title        string
	HTTPClient   *http.Client
}

func (p OpenRouterProvider) GetDefaultModel() string {
	return p.compatible().GetDefaultModel()
}

func (p OpenRouterProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().Chat(ctx, messages, tools, model, options)
}

func (p OpenRouterProvider) ChatDurable(ctx context.Context, meta CallMetadata, messages []Message, tools []ToolDefinition, model string, options map[string]any) (*LLMResponse, error) {
	return p.compatible().ChatDurable(ctx, meta, messages, tools, model, options)
}

func (p OpenRouterProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any) (<-chan StreamDelta, error) {
	return p.compatible().ChatStream(ctx, messages, tools, model, options)
}

func (p OpenRouterProvider) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error) {
	return p.compatible().GenerateImage(ctx, req)
}

func (p OpenRouterProvider) GenerateAudio(ctx context.Context, req AudioGenerationRequest) (*AudioGenerationResult, error) {
	return p.compatible().GenerateAudio(ctx, req)
}

func (p OpenRouterProvider) compatible() OpenAICompatibleProvider {
	baseURL := p.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	headers := map[string]string{}
	if strings.TrimSpace(p.Referer) != "" {
		headers["HTTP-Referer"] = p.Referer
	}
	if strings.TrimSpace(p.Title) != "" {
		headers["X-Title"] = p.Title
	}
	return OpenAICompatibleProvider{
		BaseURL:      baseURL,
		APIKey:       p.APIKey,
		DefaultModel: p.DefaultModel,
		Headers:      headers,
		HTTPClient:   p.HTTPClient,
	}
}
