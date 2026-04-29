package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleProviderChat(t *testing.T) {
	var sawAuth string
	var sawCustom string
	var sawContent any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCustom = r.Header.Get("X-Test")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		if len(messages) > 0 {
			message, _ := messages[0].(map[string]any)
			sawContent = message["content"]
		}
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"content": "hello"},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		})
	}))
	defer server.Close()
	p := OpenAICompatibleProvider{BaseURL: server.URL, APIKey: "key", DefaultModel: "model", Headers: map[string]string{"X-Test": "ok"}}
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", ContentParts: []ContentPart{
		{Type: "text", Text: "hi"},
		{Type: "image_url", ImageURL: &ImageURLContent{URL: "https://example.test/image.png"}},
	}}}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("resp=%#v", resp)
	}
	if sawAuth != "Bearer key" {
		t.Fatalf("auth=%q", sawAuth)
	}
	if sawCustom != "ok" {
		t.Fatalf("custom=%q", sawCustom)
	}
	parts, ok := sawContent.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content=%#v", sawContent)
	}
	imagePart, _ := parts[1].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("imagePart=%#v", imagePart)
	}
}

func TestOpenAIProviderUsesOpenAIDefaultBaseURL(t *testing.T) {
	p := OpenAIProvider{APIKey: "key", DefaultModel: "gpt-test"}
	compatible := p.compatible()
	if compatible.BaseURL != "https://api.openai.com/v1" || compatible.DefaultModel != "gpt-test" {
		t.Fatalf("compatible=%#v", compatible)
	}
}

func TestOpenRouterProviderUsesHeadersAndDefaultBaseURL(t *testing.T) {
	p := OpenRouterProvider{APIKey: "key", DefaultModel: "openai/gpt-test", Referer: "https://duraclaw.test", Title: "Duraclaw"}
	compatible := p.compatible()
	if compatible.BaseURL != "https://openrouter.ai/api/v1" || compatible.DefaultModel != "openai/gpt-test" {
		t.Fatalf("compatible=%#v", compatible)
	}
	if compatible.Headers["HTTP-Referer"] != "https://duraclaw.test" || compatible.Headers["X-Title"] != "Duraclaw" {
		t.Fatalf("headers=%#v", compatible.Headers)
	}
}

func TestResponseContentTextHandlesArrayContent(t *testing.T) {
	got := responseContentText([]any{
		map[string]any{"type": "text", "text": "hello"},
		map[string]any{"type": "output_text", "text": "world"},
	})
	if got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAICompatibleProviderChatStream(t *testing.T) {
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawStream, _ = body["stream"].(bool)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	ch, err := (OpenAICompatibleProvider{BaseURL: server.URL, DefaultModel: "model"}).ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	var finish string
	for delta := range ch {
		got.WriteString(delta.Content)
		if delta.FinishReason != "" {
			finish = delta.FinishReason
		}
	}
	if !sawStream || got.String() != "hello" || finish != "stop" {
		t.Fatalf("sawStream=%v got=%q finish=%q", sawStream, got.String(), finish)
	}
}

func TestOpenAICompatibleProviderTranscribeAudio(t *testing.T) {
	var sawPath string
	var sawMultipart bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		sawMultipart = r.MultipartForm.Value["model"][0] == "gpt-4o-mini-transcribe"
		_ = json.NewEncoder(w).Encode(map[string]any{"text": "transcript"})
	}))
	defer server.Close()
	got, err := (OpenAICompatibleProvider{BaseURL: server.URL}).TranscribeAudio(t.Context(), AudioTranscriptionRequest{Data: []byte("audio"), Filename: "clip.mp3", MediaType: "audio/mpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if sawPath != "/audio/transcriptions" || !sawMultipart || got.Text != "transcript" {
		t.Fatalf("path=%q multipart=%v got=%#v", sawPath, sawMultipart, got)
	}
}

func TestOpenAICompatibleProviderUploadFile(t *testing.T) {
	var sawPurpose string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Fatal(err)
		}
		sawPurpose = r.MultipartForm.Value["purpose"][0]
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		if !bytes.Equal(raw, []byte("hello")) {
			t.Fatalf("file=%q", raw)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "file_1", "filename": "doc.txt", "purpose": sawPurpose, "bytes": 5})
	}))
	defer server.Close()
	got, err := (OpenAICompatibleProvider{BaseURL: server.URL}).UploadFile(t.Context(), FileUploadRequest{Data: []byte("hello"), Filename: "doc.txt", MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if sawPurpose != "user_data" || got.ID != "file_1" || got.Bytes != 5 {
		t.Fatalf("purpose=%q got=%#v", sawPurpose, got)
	}
}

func TestOpenAICompatibleProviderGenerateMedia(t *testing.T) {
	var sawImagePath, sawAudioPath, sawVideoPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			sawImagePath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": "ZmFrZQ==", "revised_prompt": "prompt"}}})
		case "/audio/speech":
			sawAudioPath = r.URL.Path
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write([]byte("wav"))
		case "/videos":
			sawVideoPath = r.URL.Path
			if err := r.ParseMultipartForm(1024); err != nil {
				t.Fatal(err)
			}
			if r.MultipartForm.Value["prompt"][0] != "make video" {
				t.Fatalf("form=%#v", r.MultipartForm.Value)
			}
			if r.MultipartForm.Value["count"][0] != "2" {
				t.Fatalf("form=%#v", r.MultipartForm.Value)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "video_1", "status": "queued", "model": "sora-2", "progress": 0})
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	p := OpenAICompatibleProvider{BaseURL: server.URL}
	image, err := p.GenerateImage(t.Context(), ImageGenerationRequest{Prompt: "draw"})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := p.GenerateAudio(t.Context(), AudioGenerationRequest{Text: "say it", ResponseFormat: "wav"})
	if err != nil {
		t.Fatal(err)
	}
	video, err := p.GenerateVideo(t.Context(), VideoGenerationRequest{Prompt: "make video", Model: "sora-2", Seconds: "4", Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if sawImagePath != "/images/generations" || len(image.Images) != 1 || image.Images[0].B64JSON != "ZmFrZQ==" || image.Images[0].RevisedPrompt != "prompt" {
		t.Fatalf("image=%#v path=%q", image, sawImagePath)
	}
	if sawAudioPath != "/audio/speech" || string(audio.Data) != "wav" || audio.MediaType != "audio/wav" {
		t.Fatalf("audio=%#v path=%q", audio, sawAudioPath)
	}
	if sawVideoPath != "/videos" || len(video.Videos) != 1 || video.Videos[0].ID != "video_1" {
		t.Fatalf("video=%#v path=%q", video, sawVideoPath)
	}
}
