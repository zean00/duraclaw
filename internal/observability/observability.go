package observability

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

func Logger() *slog.Logger { return slog.Default() }

type Counters struct {
	mu     sync.RWMutex
	values map[string]int64
}

func NewCounters() *Counters { return &Counters{values: map[string]int64{}} }

func (c *Counters) Inc(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[name]++
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
