package sessionmonitor

import (
	"encoding/json"
	"testing"
	"time"

	"duraclaw/internal/db"
)

func TestActivePatternCountsUserMessageHoursAndWeekdays(t *testing.T) {
	messages := []db.Message{
		{CreatedAt: time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2026, 4, 28, 9, 0, 0, 0, time.UTC)},
		{CreatedAt: time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC)},
	}
	got := activePattern(nil, messages, time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC))
	hours := got["hours"].(map[string]int)
	weekdays := got["weekdays"].(map[string]int)
	if hours["09"] != 2 || hours["21"] != 1 {
		t.Fatalf("hours=%#v", hours)
	}
	if weekdays["Monday"] != 1 || weekdays["Tuesday"] != 2 {
		t.Fatalf("weekdays=%#v", weekdays)
	}
	if got["last_active"] != "2026-04-28T21:00:00Z" {
		t.Fatalf("last_active=%#v", got["last_active"])
	}
}

func TestMonitoredWindowSkipsAlreadyCountedAndConcurrentMessages(t *testing.T) {
	after := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	through := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	messages := []db.Message{
		{CreatedAt: after.Add(-time.Minute)},
		{CreatedAt: after},
		{CreatedAt: after.Add(time.Minute)},
		{CreatedAt: through},
		{CreatedAt: through.Add(time.Minute)},
	}
	got := monitoredWindow(messages, &after, through)
	if len(got) != 2 || !got[0].CreatedAt.Equal(after.Add(time.Minute)) || !got[1].CreatedAt.Equal(through) {
		t.Fatalf("window=%#v", got)
	}
	pattern := activePattern(nil, got, through)
	hours := pattern["hours"].(map[string]int)
	if hours["10"] != 1 || hours["11"] != 1 {
		t.Fatalf("hours=%#v", hours)
	}
}

func TestTranscriptExtractsTextParts(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"parts": []map[string]any{{"type": "text", "text": "hello"}}})
	got := transcript([]db.Message{{Role: "user", Content: raw}})
	if got != "user: hello" {
		t.Fatalf("transcript=%q", got)
	}
}

func TestMessageTextPrefersTopLevelText(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"text":  "top",
		"parts": []map[string]any{{"type": "text", "text": "part"}},
	})
	if got := messageText(raw); got != "top" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeAndExtractJSONObject(t *testing.T) {
	if got := normalize("  Hello   WORLD "); got != "hello world" {
		t.Fatalf("normalize=%q", got)
	}
	cases := map[string]string{
		`{"ok":true}`:            `{"ok":true}`,
		"prefix {\"ok\":true} x": `{"ok":true}`,
		"no json":                "no json",
	}
	for input, want := range cases {
		if got := extractJSONObject(input); got != want {
			t.Fatalf("extractJSONObject(%q)=%q want %q", input, got, want)
		}
	}
}
