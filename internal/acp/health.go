package acp

import "net/http"

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if h.counters == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte(h.counters.PrometheusText()))
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "database": "not_configured"})
		return
	}
	if err := h.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	stats, err := h.store.QueueStats(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "database": "ok", "queues": stats})
}
