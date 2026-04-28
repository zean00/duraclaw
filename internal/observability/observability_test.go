package observability

import (
	"strings"
	"testing"
	"time"
)

func TestCountersPrometheusText(t *testing.T) {
	c := NewCounters()
	c.Inc("worker.completed")
	got := c.PrometheusText()
	if !strings.Contains(got, "duraclaw_worker_completed 1") {
		t.Fatalf("got %q", got)
	}
}

func TestCountersPrometheusDuration(t *testing.T) {
	c := NewCounters()
	c.ObserveDuration("mcp_call_duration_seconds", 1500*time.Millisecond)
	got := c.PrometheusText()
	if !strings.Contains(got, "duraclaw_mcp_call_duration_seconds_bucket{le=\"2.5\"} 1") ||
		!strings.Contains(got, "duraclaw_mcp_call_duration_seconds_count 1") ||
		!strings.Contains(got, "duraclaw_mcp_call_duration_seconds_sum 1.500000") {
		t.Fatalf("got %q", got)
	}
}
