package collectors

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/binary"
	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
)

// BinaryCollector exposes Tier 2 / US3 metrics with per-component labels (R-019).
//
//   vpub_binary_local_mtime_unix{component=visor|bridge-voter|outcome-voter|reference-oracle-publisher}   FR-012
//   vpub_binary_remote_mtime_unix{component="visor"}                                                       FR-013
//   vpub_binary_remote_check_ok{component="visor"}                                                         FR-013
//
// Detection budget (사용자 결정 2026-05-23): HEAD 1m + expr 60s + for 1m
// → max ~3분 안에 새 binary 감지. 기존 (10m HEAD + >3600 + 30m for = 41분)
// 대비 14× 빠름. trade-off: 매분 HEAD → 추가 트래픽은 60 req/h 정도로 무시 가능.
//
// R-019 (2026-05-24): visor 는 HF announce URL 추적 (사람 install 시그널),
// child × 3 은 file mtime 만 추적 (visor 가 자동 download; 실패 감지는
// DownloadLogsCollector + VpubChildBinaryDownloadFailed 룰이 담당).
type BinaryCollector struct {
	probe         binary.Probe
	targets       map[config.ComponentName]string // local file path per component
	remoteURL     string                          // visor announce URL ("" 면 remote 추적 비활성)
	remoteTimeout time.Duration

	localMtime  *prometheus.GaugeVec // {component}
	remoteMtime *prometheus.GaugeVec // {component="visor"} only
	remoteOK    *prometheus.GaugeVec // {component="visor"} only

	mu         sync.Mutex
	lastRemote float64
	hasRemote  bool
}

// NewBinaryCollector registers the three gauge vectors. targets must be non-empty;
// remoteURL may be "" — in that case TickRemote is a noop.
//
// targets keys SHOULD be a subset of config.AllComponents. Unknown keys are
// still tracked but won't be referenced by alert rules (which use exact label
// matches). Defaults at config layer cover all 4.
func NewBinaryCollector(reg prometheus.Registerer, probe binary.Probe, targets map[config.ComponentName]string, remoteURL string) *BinaryCollector {
	c := &BinaryCollector{
		probe:         probe,
		targets:       targets,
		remoteURL:     remoteURL,
		remoteTimeout: 10 * time.Second,
		localMtime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "local_mtime_unix",
			Help:      "Unix seconds mtime of the on-disk publisher component binary. FR-012 / R-019.",
		}, []string{"component"}),
		remoteMtime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "remote_mtime_unix",
			Help:      "Unix seconds Last-Modified of the upstream binary URL (visor only). FR-013 / R-019.",
		}, []string{"component"}),
		remoteOK: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "binary",
			Name:      "remote_check_ok",
			Help:      "1 if the last HEAD against VPUB_BINARY_URL (visor) succeeded, else 0. FR-013.",
		}, []string{"component"}),
	}
	reg.MustRegister(c.localMtime, c.remoteMtime, c.remoteOK)
	return c
}

func (c *BinaryCollector) CollectorName() string { return "binary" }

// componentsSorted returns deterministic component iteration order — keeps
// tick scheduling stable and tests reproducible.
func (c *BinaryCollector) componentsSorted() []config.ComponentName {
	out := make([]config.ComponentName, 0, len(c.targets))
	for k := range c.targets {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// TickLocal stats each component's binary path and sets the labeled gauge.
// Missing files keep the previous gauge value (a separate alert can catch
// vpub_binary_local_mtime_unix{component=X} == 0 if needed). KindIO is
// returned if any single stat fails — but the loop continues for other
// components so one missing file doesn't blind the rest.
func (c *BinaryCollector) TickLocal(_ context.Context) (ErrorKind, error) {
	var firstErr error
	for _, name := range c.componentsSorted() {
		path := c.targets[name]
		if path == "" {
			continue
		}
		mt, err := c.probe.LocalMtime(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.localMtime.WithLabelValues(string(name)).Set(float64(mt.Unix()))
	}
	if firstErr != nil {
		return KindIO, firstErr
	}
	return "", nil
}

// TickRemote issues HEAD against the visor announce URL.
// child binaries are NOT HEAD-tracked (R-019).
// On error: keep previous remote_mtime value (don't reset to 0 — that would
// race with VpubVisorBinaryUpdateAvailable expr against the now-stale local),
// but flip remote_check_ok{component="visor"} to 0 so VpubBinaryRemoteCheckFail
// can fire.
func (c *BinaryCollector) TickRemote(ctx context.Context) (ErrorKind, error) {
	if c.remoteURL == "" {
		return "", nil // tracking disabled
	}
	cctx, cancel := context.WithTimeout(ctx, c.remoteTimeout)
	defer cancel()
	t, err := c.probe.RemoteLastModified(cctx, c.remoteURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	visor := string(config.ComponentVisor)
	if err != nil {
		c.remoteOK.WithLabelValues(visor).Set(0)
		if c.hasRemote {
			c.remoteMtime.WithLabelValues(visor).Set(c.lastRemote)
		}
		return KindAPI, err
	}
	c.remoteOK.WithLabelValues(visor).Set(1)
	c.lastRemote = float64(t.Unix())
	c.hasRemote = true
	c.remoteMtime.WithLabelValues(visor).Set(c.lastRemote)
	return "", nil
}

// SeedRemoteForTest sets remote_mtime_unix + remote_check_ok=1 for the given
// component WITHOUT issuing HTTP. Used by integration tests that need the
// series to appear on /metrics without standing up a fake HTTP server.
func (c *BinaryCollector) SeedRemoteForTest(component string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remoteMtime.WithLabelValues(component).Set(0)
	c.remoteOK.WithLabelValues(component).Set(1)
}

// StartLocal / StartRemote launch independent ticker goroutines with their
// own intervals. main.go composes these to honor the documented schedule.
func (c *BinaryCollector) StartLocal(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName()+"_local", interval, c.TickLocal)
}

func (c *BinaryCollector) StartRemote(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName()+"_remote", interval, c.TickRemote)
}
