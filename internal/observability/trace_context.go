package observability

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

var traceParentPattern = regexp.MustCompile(`^[\da-f]{2}-([\da-f]{32})-([\da-f]{16})-[\da-f]{2}$`)

type TraceContext struct {
	TraceID     string `json:"trace_id,omitempty"`
	SpanID      string `json:"span_id,omitempty"`
	TraceParent string `json:"traceparent,omitempty"`
}

func TraceContextFromHeaders(header http.Header) TraceContext {
	traceParent := strings.TrimSpace(header.Get("Traceparent"))
	if traceParent == "" {
		traceParent = strings.TrimSpace(header.Get("traceparent"))
	}
	if match := traceParentPattern.FindStringSubmatch(strings.ToLower(traceParent)); len(match) == 3 {
		return TraceContext{TraceID: match[1], SpanID: match[2], TraceParent: strings.ToLower(traceParent)}
	}
	traceID := strings.TrimSpace(header.Get("X-Trace-ID"))
	if traceID == "" {
		traceID = strings.TrimSpace(header.Get("X-Duraclaw-Trace-ID"))
	}
	return TraceContext{TraceID: traceID}
}

func NewTraceParent(traceID string) string {
	traceID = normalizeTraceID(traceID)
	if traceID == "" {
		traceID = randomHex(16)
	}
	spanID := randomHex(8)
	if spanID == "" {
		spanID = "0000000000000001"
	}
	return "00-" + traceID + "-" + spanID + "-01"
}

func normalizeTraceID(traceID string) string {
	traceID = strings.ToLower(strings.TrimSpace(traceID))
	traceID = strings.ReplaceAll(traceID, "-", "")
	if len(traceID) != 32 {
		return ""
	}
	for _, r := range traceID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	if traceID == "00000000000000000000000000000000" {
		return ""
	}
	return traceID
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
