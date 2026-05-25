package collectors

import (
	"context"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logtail"
)

// VisorLogCollector tails the visor's own log directory (NOT the per-component
// log subdirs) and emits two counters that 30s scrape sampling otherwise misses:
//
//   - vpub_visor_child_restart_total{component=...}
//       Counter incremented every time visor logs
//       `INFO visor: restarting process binary_path=".../<child>" ...`.
//       Detects the spawn → exit-2 → respawn loop where vpub_child_count
//       briefly returns to 3 within each scrape interval (R-022b).
//
//   - vpub_visor_crit_total
//       Counter incremented every time visor logs a CRIT line
//       (e.g. `CRIT visor: critical error managed process exited unexpectedly`).
//       Catches visor-self CRIT lines that the outcome-voter logmtime
//       collector never sees (different log dir).
//
// Pair these with the existing min_over_time-based VpubChildMissing rule
// (R-022a) for a full coverage of the restart-loop class.
type VisorLogCollector struct {
	visorLogDir string
	tailer      logtail.Tailer

	childRestart *prometheus.CounterVec
	visorCrit    prometheus.Counter
}

// VisorChildRestartPattern matches lines like:
//
//	2026-05-25T18:25:29.818967 INFO  visor: restarting process binary_path="/home/ubuntu/v-publisher/reference-oracle-publisher" height=1 n_restarts=14
//
// Capture group 1 = binary_path; we extract the final filename for the component label.
var VisorChildRestartPattern = regexp.MustCompile(
	`INFO\s+visor: restarting process\s+binary_path="[^"]*/([^/"]+)"`,
)

// VisorCritPattern catches every visor-self CRIT/ERROR line. We deliberately
// keep this broad so future bug-class lines (`critical error visor run failed`,
// `critical error managed process exited unexpectedly`, etc.) all roll up
// into the same counter.
var VisorCritPattern = regexp.MustCompile(
	`(CRIT|ERROR)\s+visor:`,
)

// validRestartChild — same cardinality guard as DownloadLogsCollector.
var validRestartChild = map[string]bool{
	string(config.ComponentBridgeVoter):            true,
	string(config.ComponentOutcomeVoter):           true,
	string(config.ComponentReferenceOraclePublish): true,
}

func NewVisorLogCollector(reg prometheus.Registerer, visorLogDir string, t logtail.Tailer) *VisorLogCollector {
	c := &VisorLogCollector{
		visorLogDir: visorLogDir,
		tailer:      t,
		childRestart: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "visor",
			Name:      "child_restart_total",
			Help:      "Cumulative visor 'restarting process' log lines per child component. R-022b — catches restart loops that 30s scrape sampling misses.",
		}, []string{"component"}),
		visorCrit: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "visor",
			Name:      "crit_total",
			Help:      "Cumulative visor-self CRIT/ERROR log lines. R-022b.",
		}),
	}
	reg.MustRegister(c.childRestart, c.visorCrit)
	// Pre-create child series at 0 so /metrics shows them even before the
	// first restart line lands.
	for name := range validRestartChild {
		c.childRestart.WithLabelValues(name)
	}
	return c
}

func (c *VisorLogCollector) CollectorName() string { return "visor_log" }

// Start subscribes to the visor log dir with both patterns. Each match
// bumps the appropriate counter — lines that match neither are dropped.
func (c *VisorLogCollector) Start(ctx context.Context, em *ExporterMetrics) {
	patterns := []*regexp.Regexp{VisorChildRestartPattern, VisorCritPattern}
	ch, err := c.tailer.Subscribe(ctx, c.visorLogDir, patterns)
	if err != nil {
		em.CollectionErrors.WithLabelValues(c.CollectorName(), string(KindIO)).Inc()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			// Restart match wins over generic CRIT match — restart lines
			// don't contain "CRIT" so order doesn't actually matter, but
			// keeps the intent obvious.
			if sub := VisorChildRestartPattern.FindStringSubmatch(m.Line); len(sub) >= 2 {
				component := sub[1]
				if validRestartChild[component] {
					c.childRestart.WithLabelValues(component).Inc()
				}
				continue
			}
			if VisorCritPattern.MatchString(m.Line) {
				c.visorCrit.Inc()
			}
		}
	}
}
