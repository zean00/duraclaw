package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOTLPExporterPostsTracesAndMetrics(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("X-Test") != "ok" {
			t.Fatalf("missing header")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	exporter := OTLPExporter{Endpoint: server.URL, Headers: map[string]string{"X-Test": "ok"}}
	if err := exporter.ExportEvent(context.Background(), "run.started", map[string]any{"trace_id": "abc"}); err != nil {
		t.Fatal(err)
	}
	counters := NewCounters()
	counters.Inc("worker_runs_completed")
	if err := exporter.ExportCounters(context.Background(), counters); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/v1/traces" || paths[1] != "/v1/metrics" {
		t.Fatalf("paths=%#v", paths)
	}
}
