package observability

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

func Logger() *slog.Logger { return slog.Default() }

type Counters struct {
	mu        sync.RWMutex
	values    map[string]int64
	durations map[string]durationValue
}

type durationValue struct {
	Count      int64
	SumSeconds float64
	Buckets    map[float64]int64
}

var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

func NewCounters() *Counters {
	return &Counters{values: map[string]int64{}, durations: map[string]durationValue{}}
}

func (c *Counters) Inc(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[name]++
}

func (c *Counters) ObserveDuration(name string, d time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.durations[name]
	v.Count++
	seconds := d.Seconds()
	v.SumSeconds += seconds
	if v.Buckets == nil {
		v.Buckets = map[float64]int64{}
	}
	for _, bucket := range durationBuckets {
		if seconds <= bucket {
			v.Buckets[bucket]++
		}
	}
	c.durations[name] = v
}

func (c *Counters) Snapshot() map[string]int64 {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]int64, len(c.values))
	for k, v := range c.values {
		out[k] = v
	}
	return out
}

func (c *Counters) DurationSnapshot() map[string]durationValue {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]durationValue, len(c.durations))
	for k, v := range c.durations {
		if v.Buckets != nil {
			buckets := make(map[float64]int64, len(v.Buckets))
			for bucket, count := range v.Buckets {
				buckets[bucket] = count
			}
			v.Buckets = buckets
		}
		out[k] = v
	}
	return out
}

func (c *Counters) PrometheusText() string {
	snapshot := c.Snapshot()
	names := make([]string, 0, len(snapshot))
	for name := range snapshot {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "duraclaw_%s %d\n", sanitizeMetricName(name), snapshot[name])
	}
	durations := c.DurationSnapshot()
	durationNames := make([]string, 0, len(durations))
	for name := range durations {
		durationNames = append(durationNames, name)
	}
	sort.Strings(durationNames)
	for _, name := range durationNames {
		metric := sanitizeMetricName(name)
		value := durations[name]
		for _, bucket := range durationBuckets {
			fmt.Fprintf(&b, "duraclaw_%s_bucket{le=\"%g\"} %d\n", metric, bucket, value.Buckets[bucket])
		}
		fmt.Fprintf(&b, "duraclaw_%s_bucket{le=\"+Inf\"} %d\n", metric, value.Count)
		fmt.Fprintf(&b, "duraclaw_%s_count %d\n", metric, value.Count)
		fmt.Fprintf(&b, "duraclaw_%s_sum %.6f\n", metric, value.SumSeconds)
	}
	return b.String()
}

func sanitizeMetricName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
