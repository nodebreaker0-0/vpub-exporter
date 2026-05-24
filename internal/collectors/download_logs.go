package collectors

import (
	"context"
	"regexp"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logtail"
)

// DownloadLogsCollector tails the visor log dir and records the timestamp of
// "downloading new binary" lines per child component (R-019 / FR-013a).
//
// Why log-based rather than HTTP HEAD on /<child>/active:
//   - HEAD tells "HF published", not "visor tried to download and failed".
//   - We want to fire only when visor's maybe_download is stuck (real failure).
//   - Reuses existing logtail infra; no extra HTTP traffic.
//
// Pair this gauge with vpub_binary_local_mtime_unix{component=<child>}:
//
//   normal:   local_mtime > download_started_unix (mtime caught up; expr negative → resolved)
//   failure:  download_started_unix - local_mtime > 60s (sustained 1m → alert fires)
//
// visor itself is NOT tracked here — visor doesn't download itself (a human
// installs it). visor's update signal is vpub_binary_remote_mtime_unix{component="visor"}.
type DownloadLogsCollector struct {
	visorLogDir string
	tailer      logtail.Tailer

	downloadStarted *prometheus.GaugeVec

	mu sync.Mutex
}

// VisorDownloadPattern matches lines like:
//
//	2026-05-22T06:00:08.009784 INFO  visor: downloading new binary self.binary_name="outcome-voter" binary_url=".../outcome-voter/1" height=1
//
// Capture group 1 = component name (bridge-voter / outcome-voter / reference-oracle-publisher).
// Visor itself never appears as self.binary_name — by design.
var VisorDownloadPattern = regexp.MustCompile(
	`INFO\s+visor: downloading new binary\s+self\.binary_name="([^"]+)"`,
)

// validDownloadChild — guard against unknown component names slipping into
// labels (cardinality protection). Matches data-model.md Component entity.
var validDownloadChild = map[string]bool{
	string(config.ComponentBridgeVoter):            true,
	string(config.ComponentOutcomeVoter):           true,
	string(config.ComponentReferenceOraclePublish): true,
}

func NewDownloadLogsCollector(reg prometheus.Registerer, visorLogDir string, t logtail.Tailer) *DownloadLogsCollector {
	c := &DownloadLogsCollector{
		visorLogDir: visorLogDir,
		tailer:      t,
		downloadStarted: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "download_started_unix",
			Help:      "Unix seconds when visor last logged 'downloading new binary' for this child component. FR-013a / R-019.",
		}, []string{"component"}),
	}
	reg.MustRegister(c.downloadStarted)
	return c
}

func (c *DownloadLogsCollector) CollectorName() string { return "download_logs" }

// SeedDownloadStartedForTest registers a fresh DownloadLogsCollector on reg
// and seeds one component sample so the series shows up on /metrics. Used
// only by tests/integration/full_metrics_test.go.
func SeedDownloadStartedForTest(reg prometheus.Registerer, component string) {
	c := NewDownloadLogsCollector(reg, "/tmp/no-such-visor-log", nil)
	c.downloadStarted.WithLabelValues(component).Set(0)
}

// Start subscribes the tailer to the visor log dir and consumes matches.
// Returns when ctx is canceled.
func (c *DownloadLogsCollector) Start(ctx context.Context, em *ExporterMetrics) {
	ch, err := c.tailer.Subscribe(ctx, c.visorLogDir, []*regexp.Regexp{VisorDownloadPattern})
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
			sub := VisorDownloadPattern.FindStringSubmatch(m.Line)
			if len(sub) < 2 {
				continue
			}
			component := sub[1]
			if !validDownloadChild[component] {
				continue
			}
			c.mu.Lock()
			c.downloadStarted.WithLabelValues(component).Set(float64(m.At.Unix()))
			c.mu.Unlock()
		}
	}
}
