package collectors

import (
	"context"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bharvest/vpub-exporter/internal/config"
	"github.com/bharvest/vpub-exporter/internal/logfs"
)

// LogMtimeCollector exports vpub_component_log_mtime_seconds {component=...}.
// FR-003 / data-model.md Component.latest_log_mtime.
//
// Layout assumption (data-model.md):
//   - visor logs sit DIRECTLY under LogDir (no per-component subdir).
//   - the other three components each have their own subdir named after the
//     component (e.g. <LogDir>/bridge-voter/).
type LogMtimeCollector struct {
	stat   logfs.LogDirStat
	logDir string

	mtime *prometheus.GaugeVec
}

// NewLogMtimeCollector registers the gauge vec on reg.
func NewLogMtimeCollector(reg prometheus.Registerer, stat logfs.LogDirStat, logDir string) *LogMtimeCollector {
	c := &LogMtimeCollector{
		stat:   stat,
		logDir: logDir,
		mtime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Name:      "component_log_mtime_seconds",
			Help:      "Unix seconds of the newest log file mtime for each publisher component. FR-003.",
		}, []string{"component"}),
	}
	reg.MustRegister(c.mtime)
	// Pre-create one zero series per known component so the alert rule's
	// `time() - vpub_component_log_mtime_seconds > 300` does not silently
	// miss a fresh, never-logged component.
	for _, name := range config.AllComponents {
		c.mtime.WithLabelValues(string(name)).Set(0)
	}
	return c
}

// dirFor returns the absolute path for the given component (data-model.md).
func (c *LogMtimeCollector) dirFor(comp config.ComponentName) string {
	if comp == config.ComponentVisor {
		return c.logDir
	}
	return filepath.Join(c.logDir, string(comp))
}

// CollectorName for self-metrics.
func (c *LogMtimeCollector) CollectorName() string { return "logmtime" }

// Tick stats each of the 4 component directories. Best-effort: if one fails,
// others still update. Returns the first encountered error.
func (c *LogMtimeCollector) Tick(_ context.Context) (ErrorKind, error) {
	var firstErr error
	var firstKind ErrorKind
	for _, comp := range config.AllComponents {
		dir := c.dirFor(comp)
		mt, _, err := c.stat.LatestMtime(dir)
		if err != nil {
			if firstErr == nil {
				firstErr, firstKind = err, KindIO
			}
			// Leave the previous value in place — Prometheus consumers see the
			// stale value go further past the alert threshold, which is the
			// correct behavior for a missing log dir.
			continue
		}
		if mt.IsZero() {
			// Empty dir: report 0 so alert rule fires (time() - 0 = huge).
			c.mtime.WithLabelValues(string(comp)).Set(0)
			continue
		}
		c.mtime.WithLabelValues(string(comp)).Set(float64(mt.Unix()))
	}
	return firstKind, firstErr
}

// Start launches the ticker.
func (c *LogMtimeCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
