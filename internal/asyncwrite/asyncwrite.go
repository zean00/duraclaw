package asyncwrite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"duraclaw/internal/db"
	"duraclaw/internal/observability"
)

type Store interface {
	EnqueueAsyncWrite(ctx context.Context, spec db.AsyncWriteSpec) (int64, error)
	ClaimAsyncWriteJobs(ctx context.Context, owner string, limit int, leaseFor time.Duration) ([]db.AsyncWriteJob, error)
	CompleteAsyncWriteJob(ctx context.Context, id int64, state string, errText *string) error
	ReleaseAsyncWriteJob(ctx context.Context, id int64, delay time.Duration, errText string) error
	AddObservabilityEvent(ctx context.Context, customerID, runID, eventType string, payload any) error
	EffectiveRuntimeLimits(ctx context.Context, customerID, agentInstanceID string) (db.EffectiveRuntimeLimits, error)
}

type Writer struct {
	store       Store
	counters    *observability.Counters
	otlp        observability.OTLPExporter
	owner       string
	limit       int
	leaseFor    time.Duration
	retryFor    time.Duration
	maxAttempts int
	queue       chan db.AsyncWriteSpec
}

func NewWriter(store Store, owner string, buffer int) *Writer {
	if owner == "" {
		owner = "duraclaw-async-write"
	}
	if buffer <= 0 {
		buffer = db.DefaultRuntimeLimits().AsyncBufferSize
	}
	return &Writer{store: store, owner: owner, limit: 50, leaseFor: time.Minute, retryFor: 30 * time.Second, maxAttempts: 5, queue: make(chan db.AsyncWriteSpec, buffer)}
}

func (w *Writer) WithCounters(counters *observability.Counters) *Writer {
	w.counters = counters
	return w
}

func (w *Writer) WithOTLPExporter(exporter observability.OTLPExporter) *Writer {
	w.otlp = exporter
	return w
}

func (w *Writer) Enqueue(ctx context.Context, spec db.AsyncWriteSpec) {
	if w == nil || w.store == nil {
		return
	}
	spec = w.prepare(ctx, spec)
	select {
	case w.queue <- spec:
		w.inc("async_write_enqueued_total")
	default:
		if _, err := w.store.EnqueueAsyncWrite(ctx, spec); err != nil {
			w.inc("async_write_dropped_total")
			return
		}
		w.inc("async_write_enqueued_total")
	}
}

func (w *Writer) RunOnce(ctx context.Context) (int, error) {
	if w == nil || w.store == nil {
		return 0, fmt.Errorf("async writer requires store")
	}
	flushed := 0
	for {
		select {
		case spec := <-w.queue:
			if _, err := w.store.EnqueueAsyncWrite(ctx, spec); err != nil {
				w.inc("async_write_dropped_total")
				return flushed, err
			}
			flushed++
		default:
			goto claim
		}
	}
claim:
	jobs, err := w.store.ClaimAsyncWriteJobs(ctx, w.owner, w.limit, w.leaseFor)
	if err != nil {
		return flushed, err
	}
	for _, job := range jobs {
		if err := w.apply(ctx, job); err != nil {
			if w.maxAttempts > 0 && job.Attempts >= w.maxAttempts {
				msg := err.Error()
				_ = w.store.CompleteAsyncWriteJob(context.Background(), job.ID, "failed", &msg)
				w.inc("async_write_failed_terminal_total")
				return flushed, err
			}
			_ = w.store.ReleaseAsyncWriteJob(context.Background(), job.ID, w.retryFor, err.Error())
			w.inc("async_write_failed_total")
			return flushed, err
		}
		if err := w.store.CompleteAsyncWriteJob(ctx, job.ID, stateAfterApply(job), nil); err != nil {
			return flushed, err
		}
		flushed++
		w.inc("async_write_flushed_total")
	}
	return flushed, nil
}

func (w *Writer) Loop(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Writer) prepare(ctx context.Context, spec db.AsyncWriteSpec) db.AsyncWriteSpec {
	if spec.State == "" {
		spec.State = "queued"
	}
	raw, _ := json.Marshal(spec.Payload)
	limits, _ := w.store.EffectiveRuntimeLimits(ctx, spec.CustomerID, spec.AgentInstanceID)
	if limits.MaxAsyncPayloadBytes > 0 && len(raw) > limits.MaxAsyncPayloadBytes {
		spec.State = "dropped"
		spec.Payload = map[string]any{"dropped": true, "original_bytes": len(raw), "job_type": spec.JobType}
		w.inc("async_write_dropped_total")
		return spec
	}
	if limits.AsyncDegradeThresholdBytes > 0 && len(raw) > limits.AsyncDegradeThresholdBytes {
		spec.State = "queued"
		spec.TargetTable = "degraded"
		spec.Payload = map[string]any{
			"event_type": "async_write_degraded",
			"payload": map[string]any{
				"degraded":       true,
				"original_bytes": len(raw),
				"job_type":       spec.JobType,
			},
		}
		w.inc("async_write_degraded_total")
	}
	return spec
}

func (w *Writer) apply(ctx context.Context, job db.AsyncWriteJob) error {
	switch job.JobType {
	case "observability_event":
		var payload struct {
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		if payload.EventType == "" {
			payload.EventType = "async_write"
		}
		var body any = map[string]any{}
		if len(payload.Payload) > 0 {
			body = payload.Payload
		}
		runID := ""
		if job.RunID != nil {
			runID = *job.RunID
		}
		return w.store.AddObservabilityEvent(ctx, job.CustomerID, runID, payload.EventType, body)
	case "otlp_event":
		var payload struct {
			Name       string         `json:"name"`
			Attributes map[string]any `json:"attributes"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		if payload.Name == "" {
			payload.Name = "duraclaw.async_write"
		}
		if payload.Attributes == nil {
			payload.Attributes = map[string]any{}
		}
		payload.Attributes["customer_id"] = job.CustomerID
		if job.RunID != nil {
			payload.Attributes["run_id"] = *job.RunID
		}
		return w.otlp.ExportEvent(ctx, payload.Name, payload.Attributes)
	default:
		return nil
	}
}

func (w *Writer) inc(name string) {
	if w.counters != nil {
		w.counters.Inc(name)
	}
}

func stateAfterApply(job db.AsyncWriteJob) string {
	if job.TargetTable == "degraded" {
		return "degraded"
	}
	if job.State == "dropped" {
		return "dropped"
	}
	return "completed"
}
