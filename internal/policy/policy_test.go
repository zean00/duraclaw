package policy

import "testing"

func TestAllowArtifact(t *testing.T) {
	rule := ArtifactRule{MaxSizeBytes: 10, MediaTypes: map[string]bool{"text/plain": true}}
	if err := AllowArtifact(9, "text/plain", rule); err != nil {
		t.Fatal(err)
	}
	if err := AllowArtifact(11, "text/plain", rule); err == nil {
		t.Fatalf("expected size denial")
	}
	if err := AllowArtifact(1, "image/png", rule); err == nil {
		t.Fatalf("expected media type denial")
	}
}

func TestRejectRawArtifactMetadata(t *testing.T) {
	if err := RejectRawArtifactMetadata(map[string]any{"safe": "value"}); err != nil {
		t.Fatal(err)
	}
	if err := RejectRawArtifactMetadata(map[string]any{"nested": map[string]any{"base64": "AAAA"}}); err == nil {
		t.Fatalf("expected raw payload metadata denial")
	}
}
