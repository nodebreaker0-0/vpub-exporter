// Package collectors holds Prometheus collectors for vpub-exporter.
//
// Architecture (constitution V — Non-Blocking Scrape):
//   - each collector keeps an in-memory cache.
//   - a background goroutine ticks (per collector tick interval) and refreshes the cache.
//   - the `/metrics` HTTP handler ONLY reads the cache. Zero external I/O at scrape time.
package collectors

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricNamespace is the universal metric prefix (Constitution III / FR-019).
const MetricNamespace = "vpub"

// ExporterMetrics are the self-observability metrics shared by all collectors.
// They are registered once at startup and reused by every collector's Refresh().
type ExporterMetrics struct {
	CollectionDuration *prometheus.HistogramVec
	CollectionErrors   *prometheus.CounterVec
}

// NewExporterMetrics constructs and registers the self-metrics on reg.
// Returns the struct so collectors can call Observe() / Inc().
func NewExporterMetrics(reg prometheus.Registerer) *ExporterMetrics {
	em := &ExporterMetrics{
		CollectionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: MetricNamespace,
			Subsystem: "exporter",
			Name:      "collection_duration_seconds",
			Help:      "Time spent in one tick of each collector.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
		}, []string{"collector"}),
		CollectionErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "exporter",
			Name:      "collection_errors_total",
			Help:      "Number of collector errors by collector and kind.",
		}, []string{"collector", "kind"}),
	}
	reg.MustRegister(em.CollectionDuration, em.CollectionErrors)
	return em
}

// ErrorKind classifies refresh failures for the collection_errors counter.
type ErrorKind string

const (
	KindTimeout ErrorKind = "timeout"
	KindAPI     ErrorKind = "api"
	KindParse   ErrorKind = "parse"
	KindIO      ErrorKind = "io"
)

// TickFunc is the work a collector does on each tick.
// It MUST update the collector's internal cache and return an error only
// when something user-visible failed (counted in CollectionErrors).
type TickFunc func(ctx context.Context) (ErrorKind, error)

// RunTicker runs fn every interval until ctx is canceled.
// It calls fn once immediately (to populate cache) then on each tick.
// Each call is wrapped to record duration and error count via em.
func RunTicker(ctx context.Context, em *ExporterMetrics, name string, interval time.Duration, fn TickFunc) {
	doOne := func() {
		start := time.Now()
		kind, err := fn(ctx)
		em.CollectionDuration.WithLabelValues(name).Observe(time.Since(start).Seconds())
		if err != nil {
			if kind == "" {
				kind = KindIO
			}
			em.CollectionErrors.WithLabelValues(name, string(kind)).Inc()
		}
	}
	doOne()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			doOne()
		}
	}
}

// Cache is a tiny generic-free read-mostly box for collector state.
// Collectors typically embed sync.RWMutex directly; this helper is for
// the case where one atomic snapshot pointer fits the data shape.
type Cache struct {
	mu   sync.RWMutex
	data any
}

func (c *Cache) Store(v any) {
	c.mu.Lock()
	c.data = v
	c.mu.Unlock()
}

func (c *Cache) Load() any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.data
}
