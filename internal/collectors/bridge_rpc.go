package collectors

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/rpc"
)

// BridgeRPCCollector probes every Arbitrum RPC the bridge-voter is configured with.
// FR-005 / contracts/metrics.md §B (bridge_rpc_*).
//
// Constitution V: probes run in a worker pool — at most ParallelProbes at a
// time. The /metrics handler reads gauges; never blocks on HTTP.
type BridgeRPCCollector struct {
	probe          rpc.RPCProbe
	names          []string
	urls           map[string]string
	timeout        time.Duration
	parallelProbes int

	up        *prometheus.GaugeVec
	latency   *prometheus.HistogramVec
	checkTot  *prometheus.CounterVec

	mu sync.Mutex
}

func NewBridgeRPCCollector(reg prometheus.Registerer, probe rpc.RPCProbe, names []string, urls map[string]string) *BridgeRPCCollector {
	c := &BridgeRPCCollector{
		probe:          probe,
		names:          names,
		urls:           urls,
		timeout:        5 * time.Second,
		parallelProbes: 3,
		up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "rpc_up",
			Help:      "Whether each Arbitrum RPC responded to eth_blockNumber on the last tick. FR-005.",
		}, []string{"name"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "rpc_latency_seconds",
			Help:      "eth_blockNumber response latency per RPC. FR-005.",
			Buckets:   []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10},
		}, []string{"name"}),
		checkTot: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "rpc_check_total",
			Help:      "Cumulative RPC check outcomes by name and status. FR-005.",
		}, []string{"name", "status"}),
	}
	reg.MustRegister(c.up, c.latency, c.checkTot)
	// Pre-create series for every configured RPC so missing data is visible.
	for _, name := range names {
		c.up.WithLabelValues(name).Set(0)
		c.checkTot.WithLabelValues(name, "ok")
		c.checkTot.WithLabelValues(name, "fail")
		c.checkTot.WithLabelValues(name, "timeout")
		c.checkTot.WithLabelValues(name, "auth_error") // R-004 — 401 / Must be authenticated
	}
	return c
}

func (c *BridgeRPCCollector) CollectorName() string { return "bridge_rpc" }

// Tick probes all configured RPCs in parallel and updates metrics.
func (c *BridgeRPCCollector) Tick(ctx context.Context) (ErrorKind, error) {
	if len(c.names) == 0 {
		return "", nil // disabled
	}
	sem := make(chan struct{}, c.parallelProbes)
	var wg sync.WaitGroup
	var firstErr error
	var firstKind ErrorKind
	var mu sync.Mutex

	for _, n := range c.names {
		name := n
		url := c.urls[name]
		if url == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			pctx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()

			latency, err := c.probe.Probe(pctx, url)
			c.latency.WithLabelValues(name).Observe(latency.Seconds())
			if err != nil {
				c.up.WithLabelValues(name).Set(0)
				kind := KindAPI
				status := "fail"
				switch {
				case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
					kind = KindTimeout
					status = "timeout"
				case errors.Is(err, rpc.ErrAuth):
					kind = KindAPI
					status = "auth_error"
				}
				c.checkTot.WithLabelValues(name, status).Inc()
				mu.Lock()
				if firstErr == nil {
					firstErr, firstKind = err, kind
				}
				mu.Unlock()
				return
			}
			c.up.WithLabelValues(name).Set(1)
			c.checkTot.WithLabelValues(name, "ok").Inc()
		}()
	}
	wg.Wait()
	return firstKind, firstErr
}

func (c *BridgeRPCCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
