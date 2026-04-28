package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInitOTelDisabled(t *testing.T) {
	runtime, err := InitOTel(context.Background(), OTelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOTelEndpointURLAppendsSignalPath(t *testing.T) {
	if got := otelEndpointURL("http://collector:4318", "/v1/traces"); got != "http://collector:4318/v1/traces" {
		t.Fatalf("got %q", got)
	}
	if got := otelEndpointURL("http://collector:4318/v1/traces", "/v1/traces"); got != "http://collector:4318/v1/traces" {
		t.Fatalf("got %q", got)
	}
}

func TestInstrumentHTTPPropagatesRequest(t *testing.T) {
	handler := InstrumentHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context() == nil {
			t.Fatal("missing context")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestInitOTelAcceptsConfiguredEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	runtime, err := InitOTel(ctx, OTelConfig{Endpoint: server.URL, ServiceName: "duraclaw-test", ExportInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, span := StartSpan(ctx, "test.span")
	span.End()
	_ = runtime.Shutdown(ctx)
}
