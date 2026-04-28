package scheduler

import (
	"testing"
	"time"
)

func TestNextCronAndIdempotencyKey(t *testing.T) {
	from := time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)
	next, err := Next("*/5 * * * *", from)
	if err != nil {
		t.Fatal(err)
	}
	if next.Minute() != 5 {
		t.Fatalf("next minute=%d", next.Minute())
	}
	k1 := IdempotencyKey("job-1", next)
	k2 := IdempotencyKey("job-1", next)
	if k1 != k2 || len(k1) == 0 {
		t.Fatalf("keys not deterministic: %q %q", k1, k2)
	}
}
