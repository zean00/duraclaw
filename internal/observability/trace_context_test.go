package observability

import (
	"net/http"
	"strings"
	"testing"
)

func TestTraceContextFromTraceparent(t *testing.T) {
	header := http.Header{}
	header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	got := TraceContextFromHeaders(header)
	if got.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || got.SpanID != "00f067aa0ba902b7" {
		t.Fatalf("got=%#v", got)
	}
}

func TestNewTraceParent(t *testing.T) {
	got := NewTraceParent("4bf92f3577b34da6a3ce929d0e0e4736")
	if !strings.HasPrefix(got, "00-4bf92f3577b34da6a3ce929d0e0e4736-") || !strings.HasSuffix(got, "-01") {
		t.Fatalf("got=%q", got)
	}
}
