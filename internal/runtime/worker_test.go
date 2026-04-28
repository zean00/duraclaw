package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"duraclaw/internal/db"
	"duraclaw/internal/outbound"
)

func TestExtractTextFromContentParts(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": "hello"},
			{"type": "location", "data": map[string]any{"lat": 1}},
			{"type": "text", "text": "world"},
		},
	})
	got := extractText(raw)
	if got != "hello\nworld" {
		t.Fatalf("got %q", got)
	}
}

func TestArtifactRefsFromContentParts(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "artifact_ref", "data": map[string]any{"artifact_id": "a1"}},
			{"type": "text", "text": "ignore"},
			{"type": "artifact_ref", "data": map[string]any{"artifact_id": "a2"}},
		},
	})
	got := artifactRefs(raw)
	if len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Fatalf("got %#v", got)
	}
}

func TestProviderContentPartsFromMultimodalInput(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": "ignored because fallback already carries context"},
			{"type": "image_url", "data": map[string]any{"url": "https://example.test/image.png", "detail": "low"}},
			{"type": "file", "data": map[string]any{"file_data": "data:application/pdf;base64,abc", "filename": "doc.pdf"}},
			{"type": "input_audio", "data": map[string]any{"data": "abc", "format": "mp3"}},
			{"type": "video_url", "data": map[string]any{"url": "https://example.test/video.mp4"}},
		},
	})
	got := providerContentParts(raw, "hello")
	if len(got) != 6 {
		t.Fatalf("got=%#v", got)
	}
	if got[1].Type != "text" || got[2].ImageURL == nil || got[3].File == nil || got[4].InputAudio == nil || got[5].VideoURL == nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestProviderContentPartsPreservesSingleNonTextInput(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{{"type": "image_url", "data": map[string]any{"url": "https://example.test/image.png"}}},
	})
	got := providerContentParts(raw, "")
	if len(got) != 1 || got[0].ImageURL == nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestProviderContentPartsDropsTextOnlyInput(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"parts": []map[string]any{{"type": "text", "text": "hello"}}})
	if got := providerContentParts(raw, ""); got != nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestMessageTextFromStoredAssistantContent(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": "first"},
			{"type": "text", "text": "second"},
		},
	})
	got := messageText(raw)
	if got != "first\nsecond" {
		t.Fatalf("got %q", got)
	}
}

func TestWorkflowOutputTextPrefersText(t *testing.T) {
	got := workflowOutputText(map[string]any{"text": "  hello  ", "other": true})
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

type fakeOutboundStore struct {
	intent db.OutboundIntent
}

func (s *fakeOutboundStore) CreateOutboundIntent(_ context.Context, intent db.OutboundIntent) (string, int64, error) {
	s.intent = intent
	return "intent-1", 1, nil
}

func TestEmitFinalOutbound(t *testing.T) {
	store := &fakeOutboundStore{}
	w := (&Worker{}).WithOutbound(outbound.NewService(store))
	err := w.emitFinalOutbound(context.Background(), &db.Run{
		ID: "run-1", CustomerID: "c", UserID: "u", SessionID: "s",
	}, "msg-1", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if store.intent.Type != "message" || store.intent.RunID == nil || *store.intent.RunID != "run-1" {
		t.Fatalf("intent=%#v", store.intent)
	}
	var payload map[string]any
	if err := json.Unmarshal(store.intent.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["message_id"] != "msg-1" {
		t.Fatalf("payload=%#v", payload)
	}
}
