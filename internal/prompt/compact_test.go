package prompt

import "testing"

func TestCompactMessagesKeepsNewestWithinBudget(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "old old old"},
		{Role: "assistant", Content: "middle middle"},
		{Role: "user", Content: "new"},
	}
	got := CompactMessages(msgs, 20)
	if got.Omitted == 0 {
		t.Fatalf("expected omission: %#v", got)
	}
	if got.Messages[len(got.Messages)-1].Content != "new" {
		t.Fatalf("newest message not kept: %#v", got.Messages)
	}
	if got.Messages[0].Role != "system" {
		t.Fatalf("missing compaction marker: %#v", got.Messages)
	}
}

func TestCompactMessagesNoopWithinBudget(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "short"}}
	got := CompactMessages(msgs, 100)
	if got.Omitted != 0 || len(got.Messages) != 1 {
		t.Fatalf("got %#v", got)
	}
}
