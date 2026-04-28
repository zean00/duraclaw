package observability

import (
	"strings"
	"testing"
)

func TestCountersPrometheusText(t *testing.T) {
	c := NewCounters()
	c.Inc("worker.completed")
	got := c.PrometheusText()
	if !strings.Contains(got, "duraclaw_worker_completed 1") {
		t.Fatalf("got %q", got)
	}
}
