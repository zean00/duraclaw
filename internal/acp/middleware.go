package acp

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (h *Handler) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if h.counters != nil {
			h.counters.Inc(fmt.Sprintf("http_status_%d", rec.status))
		}
		if h.logger != nil {
			h.logger.InfoContext(r.Context(), "http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", r.Header.Get("X-Request-ID"),
			)
		}
	})
}

func (h *Handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.requireAuth && h.adminToken == "" {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("admin authorization is not configured"))
			return
		}
		if h.adminToken != "" && !bearerTokenEqual(r.Header.Get("Authorization"), h.adminToken) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("admin authorization required"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireACP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.requireAuth && h.acpToken == "" {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("acp authorization is not configured"))
			return
		}
		if h.acpToken != "" && !bearerTokenEqual(r.Header.Get("Authorization"), h.acpToken) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("acp authorization required"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) withRequestHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
			w.Header().Set("X-Request-ID", reqID)
		}
		next.ServeHTTP(w, r)
	})
}

func bearerTokenEqual(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	if len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
