package collectors

import (
	"context"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logfs"
)

// LogMtimeCollector exports vpub_component_log_mtime_seconds {component=...}.
// FR-003 / data-model.md Component.latest_log_mtime.
//
// Layout (R-001 confirmed, 2026-05-23):
//   - visor logs live in VisorLogDir (--log-dir argument to validator-publisher.service).
//   - bridge-voter / reference-oracle-publisher / outcome-voter each live in
//     ComponentLogDir/<name>/ — visor hard-codes /tmp/validator-publisher and
//     never honors --log-dir for child components.
type LogMtimeCollector struct {
	stat            logfs.LogDirStat
	visorLogDir     string
	componentLogDir string

	mtime *prometheus.GaugeVec
}

// NewLogMtimeCollector registers the gauge vec on reg.
func NewLogMtimeCollector(reg prometheus.Registerer, stat logfs.LogDirStat, visorLogDir, componentLogDir string) *LogMtimeCollector {
	c := &LogMtimeCollector{
		stat:            stat,
		visorLogDir:     visorLogDir,
		componentLogDir: componentLogDir,
		mtime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Name:      "component_log_mtime_seconds",
			Help:      "Unix seconds of the newest log file mtime for each publisher component. FR-003.",
		}, []string{"component"}),
	}
	reg.MustRegister(c.mtime)
	for _, name := range config.AllComponents {
		c.mtime.WithLabelValues(string(name)).Set(0)
	}
	return c
}

// dirFor returns the absolute path for the given component.
// R-001: visor and child components live on DIFFERENT filesystem trees.
func (c *LogMtimeCollector) dirFor(comp config.ComponentName) string {
	if comp == config.ComponentVisor {
		return c.visorLogDir
	}
	return filepath.Join(c.componentLogDir, string(comp))
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
