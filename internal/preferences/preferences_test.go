package preferences

import (
	"encoding/json"
	"testing"

	"duraclaw/internal/db"
)

func TestConditionMatches(t *testing.T) {
	raw := json.RawMessage(`{"season":"summer","location":["home","office"]}`)
	if !ConditionMatches(raw, map[string]any{"season": "summer", "location": "office"}) {
		t.Fatalf("expected match")
	}
	if ConditionMatches(raw, map[string]any{"season": "winter", "location": "office"}) {
		t.Fatalf("expected no match")
	}
}

func TestMatchIncludesUnconditionalPreferences(t *testing.T) {
	list := []db.Preference{
		{ID: "always", Condition: json.RawMessage(`{}`)},
		{ID: "winter", Condition: json.RawMessage(`{"season":"winter"}`)},
	}
	got := Match(list, map[string]any{"season": "summer"})
	if len(got) != 1 || got[0].ID != "always" {
		t.Fatalf("got=%#v", got)
	}
}
