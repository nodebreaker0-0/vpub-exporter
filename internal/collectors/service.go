package collectors

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bharvest/vpub-exporter/internal/procs"
	"github.com/bharvest/vpub-exporter/internal/systemd"
)

// ServiceCollector exports the Tier 0 service-level metrics:
//   - vpub_service_up                  (Gauge, FR-001)
//   - vpub_child_count                 (Gauge, FR-002)
//   - vpub_service_restart_total       (Counter via Gauge, FR-004)
//
// Refresh strategy (constitution V):
//   - background goroutine ticks every interval, talks to systemd dbus + procfs,
//     and updates the underlying gauges/counter directly.
//   - /metrics handler reads the gauges' current values — zero blocking.
type ServiceCollector struct {
	probe   systemd.ServiceProbe
	lister  procs.ChildLister
	name    string

	up           prometheus.Gauge
	childCount   prometheus.Gauge
	restartTotal prometheus.Counter

	mu           sync.Mutex
	lastNRestart int // monotonic mirror of systemd NRestarts (for Counter delta)
}

// NewServiceCollector wires Prometheus collectors on reg and returns a
// Collector ready to be ticked by RunTicker.
func NewServiceCollector(reg prometheus.Registerer, probe systemd.ServiceProbe, lister procs.ChildLister, name string) *ServiceCollector {
	c := &ServiceCollector{
		probe:  probe,
		lister: lister,
		name:   name,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Name:      "service_up",
			Help:      "Whether validator-publisher.service is active (1) or not (0). FR-001.",
		}),
		childCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Name:      "child_count",
			Help:      "Number of child processes spawned by visor (normal = 3). FR-002.",
		}),
		restartTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Name:      "service_restart_total",
			Help:      "Cumulative restart count reported by systemd NRestarts. FR-004.",
		}),
	}
	reg.MustRegister(c.up, c.childCount, c.restartTotal)
	return c
}

// CollectorName is used as the collector label on exporter self-metrics.
func (c *ServiceCollector) CollectorName() string { return "service" }

// Tick refreshes all three Tier 0 service metrics. Safe to call from RunTicker.
// Errors in one source do not block the others — best-effort.
func (c *ServiceCollector) Tick(_ context.Context) (ErrorKind, error) {
	var firstErr error
	var firstKind ErrorKind

	// service_up + NRestarts both come from the same dbus probe, so we share a
	// MainPID read across them.
	active, err := c.probe.IsActive()
	if err != nil {
		firstErr = err
		firstKind = KindAPI
		c.up.Set(0)
	} else if active {
		c.up.Set(1)
	} else {
		c.up.Set(0)
	}

	pid, err := c.probe.MainPID()
	if err != nil {
		if firstErr == nil {
			firstErr, firstKind = err, KindAPI
		}
		pid = 0
	}

	if n, err := c.probe.NRestarts(); err == nil {
		c.mu.Lock()
		delta := n - c.lastNRestart
		if delta > 0 {
			c.restartTotal.Add(float64(delta))
		}
		// systemd may reset NRestarts (e.g. after reset-failed) — guard against
		// regression by only updating the mirror when n is monotonic-equivalent.
		if n >= c.lastNRestart {
			c.lastNRestart = n
		} else {
			// reset detected; rebase mirror but do not subtract from counter
			c.lastNRestart = n
		}
		c.mu.Unlock()
	} else if firstErr == nil {
		firstErr, firstKind = err, KindAPI
	}

	if pid > 0 {
		n, err := c.lister.CountChildren(pid)
		if err != nil {
			if firstErr == nil {
				firstErr, firstKind = err, KindIO
			}
			c.childCount.Set(0)
		} else {
			c.childCount.Set(float64(n))
		}
	} else {
		c.childCount.Set(0)
	}

	return firstKind, firstErr
}

// Start launches a background ticker. Returns when ctx is canceled.
func (c *ServiceCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
