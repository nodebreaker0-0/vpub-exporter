package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// BridgeStateCollector reads bridge-voter-<chain>-state.json and exports the
// last scanned Arbitrum block + the file mtime as Prometheus gauges.
//
// FR-012a. Strongest bridge health signal — when this advances, the bridge
// is actually scanning. When it stalls, even if logs look healthy, the bridge
// is stuck.
//
// Constitution IV — this collector opens ONLY the configured path. config.json
// sits in the same directory and is explicitly NEVER read.
type BridgeStateCollector struct {
	path string

	lastBlock      prometheus.Gauge
	mtime          prometheus.Gauge
	explorerCursor *prometheus.GaugeVec // {explorer} — diagnostic, per-explorer cursor
}

// stateDoc mirrors the on-disk JSON. We deserialize only the fields we need;
// the larger `transactions` map is ignored (and never logged).
//
// Format (publisher upgrade ~2026-07 switched RPC scanning to explorer
// scanning): {"explorer_cursors": {"etherscan": N, "blockscout": N}, "transactions": {}}
// The old {"last_scanned_block": N} scalar is no longer emitted, so we parse
// only explorer_cursors.
type stateDoc struct {
	// Per-explorer scan cursors (etherscan/blockscout).
	ExplorerCursors map[string]int64 `json:"explorer_cursors"`
}

// maxStateFileSize bounds how much we read so a runaway state.json (e.g.
// transactions map grew to MB scale) can't OOM the exporter.
const maxStateFileSize = 16 * 1024 * 1024 // 16 MiB

func NewBridgeStateCollector(reg prometheus.Registerer, path string) *BridgeStateCollector {
	c := &BridgeStateCollector{
		path: path,
		lastBlock: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "state_last_scanned_block",
			Help:      "Lowest explorer scan cursor from state.json (min across explorer_cursors). FR-012a — strongest bridge progress signal.",
		}),
		mtime: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "state_mtime_unix",
			Help:      "Unix seconds mtime of bridge-voter-<chain>-state.json. FR-012a.",
		}),
		explorerCursor: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "state_explorer_cursor",
			Help:      "Per-explorer scan cursor from state.json explorer_cursors (etherscan/blockscout). FR-012a (diagnostic).",
		}, []string{"explorer"}),
	}
	reg.MustRegister(c.lastBlock, c.mtime, c.explorerCursor)
	return c
}

func (c *BridgeStateCollector) CollectorName() string { return "bridge_state" }

func (c *BridgeStateCollector) Tick(_ context.Context) (ErrorKind, error) {
	if c.path == "" {
		return "", nil // disabled
	}
	fi, err := os.Stat(c.path)
	if err != nil {
		return KindIO, fmt.Errorf("stat %s: %w", c.path, err)
	}
	c.mtime.Set(float64(fi.ModTime().Unix()))

	if fi.Size() > maxStateFileSize {
		return KindIO, fmt.Errorf("state file size %d exceeds %d", fi.Size(), maxStateFileSize)
	}

	// O_RDONLY is implied by os.Open. We never modify, truncate, or chmod.
	f, err := os.Open(c.path)
	if err != nil {
		return KindIO, fmt.Errorf("open %s: %w", c.path, err)
	}
	defer f.Close()

	var doc stateDoc
	if err := json.NewDecoder(io.LimitReader(f, maxStateFileSize)).Decode(&doc); err != nil {
		return KindParse, fmt.Errorf("decode %s: %w", c.path, err)
	}
	if len(doc.ExplorerCursors) == 0 {
		// No cursors = unexpected shape (format changed again?). Surface loudly
		// via the error counter instead of silently zeroing the gauge.
		return KindParse, fmt.Errorf("%s: no explorer_cursors in state file", c.path)
	}
	// last_scanned_block gauge = the slowest explorer (min): if any explorer
	// stalls, the gauge stalls and VpubBridgeStateStuck fires.
	minCur := int64(0)
	first := true
	for name, cur := range doc.ExplorerCursors {
		c.explorerCursor.WithLabelValues(name).Set(float64(cur))
		if first || cur < minCur {
			minCur, first = cur, false
		}
	}
	c.lastBlock.Set(float64(minCur))
	return "", nil
}

func (c *BridgeStateCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
