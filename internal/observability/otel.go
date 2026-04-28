package observability

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type OTelConfig struct {
	Endpoint       string
	Headers        map[string]string
	ServiceName    string
	ExportInterval time.Duration
	Insecure       bool
}

type OTelRuntime struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
}

func InitOTel(ctx context.Context, cfg OTelConfig) (*OTelRuntime, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		otel.SetTextMapPropagator(propagation.TraceContext{})
		return &OTelRuntime{}, nil
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "duraclaw"
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes("", attribute.String("service.name", cfg.ServiceName)))
	if err != nil {
		return nil, err
	}
	traceOpts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(otelEndpointURL(cfg.Endpoint, "/v1/traces")), otlptracehttp.WithHeaders(cfg.Headers)}
	metricOpts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(otelEndpointURL(cfg.Endpoint, "/v1/metrics")), otlpmetrichttp.WithHeaders(cfg.Headers)}
	if cfg.Insecure {
		traceOpts = append(traceOpts, otlptracehttp.WithInsecure())
		metricOpts = append(metricOpts, otlpmetrichttp.WithInsecure())
	}
	traceExporter, err := otlptracehttp.New(ctx, traceOpts...)
	if err != nil {
		return nil, err
	}
	metricExporter, err := otlpmetrichttp.New(ctx, metricOpts...)
	if err != nil {
		return nil, err
	}
	if cfg.ExportInterval <= 0 {
		cfg.ExportInterval = 10 * time.Second
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(traceExporter))
	mp := metric.NewMeterProvider(metric.WithResource(res), metric.WithReader(metric.NewPeriodicReader(metricExporter, metric.WithInterval(cfg.ExportInterval))))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otelMetrics.SetMeter(mp.Meter("duraclaw"))
	return &OTelRuntime{tracerProvider: tp, meterProvider: mp}, nil
}

func otelEndpointURL(endpoint, path string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if strings.HasSuffix(endpoint, "/v1/traces") || strings.HasSuffix(endpoint, "/v1/metrics") {
		return endpoint
	}
	return endpoint + path
}

func (r *OTelRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.tracerProvider != nil {
		if err := r.tracerProvider.Shutdown(ctx); err != nil {
			return err
		}
	}
	if r.meterProvider != nil {
		return r.meterProvider.Shutdown(ctx)
	}
	return nil
}

func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx, span := otel.Tracer("duraclaw").Start(ctx, name, trace.WithAttributes(attrs...))
	return ctx, span
}

func InstrumentHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := StartSpan(ctx, "http "+r.Method+" "+r.URL.Path,
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
			attribute.String("duraclaw.request_id", r.Header.Get("X-Request-ID")),
			attribute.String("duraclaw.customer_id", r.Header.Get("X-Customer-ID")),
		)
		defer span.End()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
