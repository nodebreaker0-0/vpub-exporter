package collectors

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/binary"
)

// BinaryCollector exposes Tier 2 / US3 metrics:
//   - vpub_binary_local_mtime_unix    (Gauge)  FR-012
//   - vpub_binary_remote_mtime_unix   (Gauge)  FR-013
//   - vpub_binary_remote_check_ok     (Gauge)  FR-013
//
// Detection budget (사용자 결정 2026-05-23): HEAD 1m + expr 60s + for 1m
// → max ~3분 안에 새 binary 감지. 기존 (10m HEAD + >3600 + 30m for = 41분)
// 대비 14× 빠름. trade-off: 매분 HEAD → 추가 트래픽은 6 req/h 정도로 무시 가능.
type BinaryCollector struct {
	probe         binary.Probe
	localPath     string
	remoteURL     string
	remoteTimeout time.Duration

	localMtime  prometheus.Gauge
	remoteMtime prometheus.Gauge
	remoteOK    prometheus.Gauge

	mu          sync.Mutex
	lastRemote  float64
	hasRemote   bool
}

// NewBinaryCollector registers the three gauges. remoteURL may be "" — in
// that case Tick performs only the local stat (no remote calls).
func NewBinaryCollector(reg prometheus.Registerer, probe binary.Probe, localPath, remoteURL string) *BinaryCollector {
	c := &BinaryCollector{
		probe:         probe,
		localPath:     localPath,
		remoteURL:     remoteURL,
		remoteTimeout: 10 * time.Second,
		localMtime: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "local_mtime_unix",
			Help:      "Unix seconds mtime of the locally-installed publisher binary. FR-012.",
		}),
		remoteMtime: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "remote_mtime_unix",
			Help:      "Unix seconds Last-Modified of the upstream publisher binary URL. FR-013.",
		}),
		remoteOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "remote_check_ok",
			Help:      "1 if the last HEAD against VPUB_BINARY_URL succeeded, else 0. FR-013.",
		}),
	}
	reg.MustRegister(c.localMtime, c.remoteMtime, c.remoteOK)
	return c
}

func (c *BinaryCollector) CollectorName() string { return "binary" }

// TickLocal stats the local binary path. Cheap — no network.
func (c *BinaryCollector) TickLocal(_ context.Context) (ErrorKind, error) {
	mt, err := c.probe.LocalMtime(c.localPath)
	if err != nil {
		// Keep previous gauge value; missing binary is itself reportable
		// only via vpub_exporter_collection_errors_total{collector=binary,kind=io}.
		return KindIO, err
	}
	c.localMtime.Set(float64(mt.Unix()))
	return "", nil
}

// TickRemote issues HEAD against the announce URL.
// On error: keep previous remote_mtime value (don't reset to 0 — that would
// race with VpubBinaryUpdateAvailable expr against the now-stale local), but
// flip remote_check_ok to 0 so VpubBinaryRemoteCheckFail can fire.
func (c *BinaryCollector) TickRemote(ctx context.Context) (ErrorKind, error) {
	if c.remoteURL == "" {
		return "", nil // tracking disabled
	}
	cctx, cancel := context.WithTimeout(ctx, c.remoteTimeout)
	defer cancel()
	t, err := c.probe.RemoteLastModified(cctx, c.remoteURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.remoteOK.Set(0)
		// Preserve last-known remote mtime if we have one — otherwise leave at 0.
		if c.hasRemote {
			c.remoteMtime.Set(c.lastRemote)
		}
		return KindAPI, err
	}
	c.remoteOK.Set(1)
	c.lastRemote = float64(t.Unix())
	c.hasRemote = true
	c.remoteMtime.Set(c.lastRemote)
	return "", nil
}

// StartLocal / StartRemote launch independent ticker goroutines with their
// own intervals. main.go composes these to honor the documented schedule.
func (c *BinaryCollector) StartLocal(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName()+"_local", interval, c.TickLocal)
}

func (c *BinaryCollector) StartRemote(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName()+"_remote", interval, c.TickRemote)
}
