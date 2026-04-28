package observability

import (
	"strings"
	"testing"
	"time"
)

func TestCountersPrometheusText(t *testing.T) {
	c := NewCounters()
	c.Inc("worker.completed")
	c.Add("model_token_input_total", 3)
	got := c.PrometheusText()
	if !strings.Contains(got, "duraclaw_worker_completed 1") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "duraclaw_model_token_input_total 3") {
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

func TestCountersRecordWithOTelRecorder(t *testing.T) {
	c := NewCounters()
	c.Add("test_counter_total", 2)
	c.ObserveDuration("test_duration_seconds", time.Millisecond)
	if c.Snapshot()["test_counter_total"] != 2 {
		t.Fatalf("snapshot=%#v", c.Snapshot())
	}
	if c.DurationSnapshot()["test_duration_seconds"].Count != 1 {
		t.Fatalf("durations=%#v", c.DurationSnapshot())
	}
}
