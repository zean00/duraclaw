package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type OTLPExporter struct {
	Endpoint   string
	Headers    map[string]string
	HTTPClient *http.Client
}

func (e OTLPExporter) Enabled() bool {
	return strings.TrimSpace(e.Endpoint) != ""
}

func (e OTLPExporter) ExportEvent(ctx context.Context, name string, attrs map[string]any) error {
	if !e.Enabled() {
		return nil
	}
	payload := map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{{"key": "service.name", "value": map[string]any{"stringValue": "duraclaw"}}}},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "duraclaw"},
				"spans": []map[string]any{{
					"traceId":           stringAttr(attrs, "trace_id"),
					"spanId":            randomHex(8),
					"name":              name,
					"kind":              1,
					"startTimeUnixNano": fmt.Sprintf("%d", time.Now().UnixNano()),
					"endTimeUnixNano":   fmt.Sprintf("%d", time.Now().UnixNano()),
					"attributes":        otlpAttributes(attrs),
				}},
			}},
		}},
	}
	return e.postJSON(ctx, "/v1/traces", payload)
}

func (e OTLPExporter) ExportCounters(ctx context.Context, counters *Counters) error {
	if !e.Enabled() || counters == nil {
		return nil
	}
	payload := map[string]any{
		"resourceMetrics": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{{"key": "service.name", "value": map[string]any{"stringValue": "duraclaw"}}}},
			"scopeMetrics": []map[string]any{{
				"scope":   map[string]any{"name": "duraclaw"},
				"metrics": otlpCounterMetrics(counters.Snapshot()),
			}},
		}},
	}
	return e.postJSON(ctx, "/v1/metrics", payload)
}

func (e OTLPExporter) postJSON(ctx context.Context, path string, payload any) error {
	raw, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(e.Endpoint, "/")
	if strings.HasSuffix(endpoint, "/v1/traces") || strings.HasSuffix(endpoint, "/v1/metrics") {
		path = ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range e.Headers {
		if strings.TrimSpace(key) != "" && value != "" {
			req.Header.Set(key, value)
		}
	}
	client := e.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("otlp export status %d", resp.StatusCode)
	}
	return nil
}

func otlpAttributes(attrs map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for key, value := range attrs {
		out = append(out, map[string]any{"key": key, "value": otlpValue(value)})
	}
	return out
}

func otlpCounterMetrics(values map[string]int64) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	now := fmt.Sprintf("%d", time.Now().UnixNano())
	for name, value := range values {
		out = append(out, map[string]any{
			"name":        sanitizeMetricName(name),
			"description": name,
			"sum": map[string]any{
				"aggregationTemporality": 2,
				"isMonotonic":            false,
				"dataPoints": []map[string]any{{
					"asInt":             fmt.Sprintf("%d", value),
					"timeUnixNano":      now,
					"startTimeUnixNano": now,
				}},
			},
		})
	}
	return out
}

func otlpValue(value any) map[string]any {
	switch v := value.(type) {
	case bool:
		return map[string]any{"boolValue": v}
	case int:
		return map[string]any{"intValue": fmt.Sprintf("%d", v)}
	case int64:
		return map[string]any{"intValue": fmt.Sprintf("%d", v)}
	case float64:
		return map[string]any{"doubleValue": v}
	default:
		return map[string]any{"stringValue": fmt.Sprint(v)}
	}
}

func stringAttr(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	return fmt.Sprint(attrs[key])
}
