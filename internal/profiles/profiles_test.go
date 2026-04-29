package profiles

import (
	"testing"
	"time"
)

func TestMergeMetadataStoresProfileEnvelope(t *testing.T) {
	got := MergeMetadata(map[string]any{"keep": true}, &Result{
		Profile:  map[string]any{"display_name": "Sahal", "email": "s@example.test"},
		Metadata: map[string]any{"etag": "v1"},
	}, "http", time.Date(2026, 4, 29, 1, 2, 3, 0, time.UTC))
	if got["keep"] != true {
		t.Fatalf("metadata was not preserved: %#v", got)
	}
	profile := got["profile"].(map[string]any)
	if profile["display_name"] != "Sahal" {
		t.Fatalf("profile=%#v", profile)
	}
	source := got["profile_source"].(map[string]any)
	if source["provider"] != "http" || source["etag"] != "v1" {
		t.Fatalf("source=%#v", source)
	}
}

func TestAllowedProfileFiltersFields(t *testing.T) {
	got := AllowedProfile(map[string]any{
		"profile": map[string]any{"display_name": "Sahal", "email": "s@example.test", "timezone": "Asia/Jakarta"},
	}, []string{"display_name", "timezone"})
	if len(got) != 2 || got["display_name"] != "Sahal" || got["timezone"] != "Asia/Jakarta" {
		t.Fatalf("filtered=%#v", got)
	}
	if _, ok := got["email"]; ok {
		t.Fatalf("email should not be included: %#v", got)
	}
}
