package collectors

import (
	"context"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bharvest/vpub-exporter/internal/config"
	"github.com/bharvest/vpub-exporter/internal/logfs"
	"github.com/bharvest/vpub-exporter/internal/logtail"
)

// VoteLogsCollector tails bridge-voter and reference-oracle-publisher logs,
// counts ok/fail vote attempts and disagreement events, and tracks last
// success timestamps.
//
// FR-006 / FR-007 / FR-008 / contracts/metrics.md §B.
//
// Patterns come from config (env override per FR-020). Defaults live in
// internal/config/config.go — they are intentionally generic until
// research.md R-003 confirms the actual log strings.
type VoteLogsCollector struct {
	cfg      *config.Config
	stat     logfs.LogDirStat
	tailer   logtail.Tailer

	// Metric handles.
	bridgeVoteTot     *prometheus.CounterVec
	oracleVoteTot     *prometheus.CounterVec
	bridgeDisagreeTot prometheus.Counter
	bridgeLastOK      prometheus.Gauge
	oracleLastOK      prometheus.Gauge

	mu sync.Mutex
}

func NewVoteLogsCollector(reg prometheus.Registerer, cfg *config.Config, stat logfs.LogDirStat, t logtail.Tailer) *VoteLogsCollector {
	c := &VoteLogsCollector{
		cfg:    cfg,
		stat:   stat,
		tailer: t,
		bridgeVoteTot: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "vote_total",
			Help:      "Cumulative bridge vote submissions by status. FR-006.",
		}, []string{"status"}),
		oracleVoteTot: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "oracle",
			Name:      "vote_total",
			Help:      "Cumulative reference-oracle vote submissions by status. FR-008.",
		}, []string{"status"}),
		bridgeDisagreeTot: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "rpc_disagreement_total",
			Help:      "Cumulative bridge RPC disagreement events. FR-006.",
		}),
		bridgeLastOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "last_vote_success_unix",
			Help:      "Unix seconds of the last successful bridge vote (initial = exporter start). FR-007.",
		}),
		oracleLastOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "oracle",
			Name:      "last_vote_success_unix",
			Help:      "Unix seconds of the last successful oracle vote (initial = exporter start). FR-007.",
		}),
	}
	reg.MustRegister(c.bridgeVoteTot, c.oracleVoteTot, c.bridgeDisagreeTot, c.bridgeLastOK, c.oracleLastOK)
	// Initial values per metrics.md ("초기값 = exporter start time").
	now := float64(time.Now().Unix())
	c.bridgeLastOK.Set(now)
	c.oracleLastOK.Set(now)
	// Pre-create status series.
	c.bridgeVoteTot.WithLabelValues("ok")
	c.bridgeVoteTot.WithLabelValues("fail")
	c.oracleVoteTot.WithLabelValues("ok")
	c.oracleVoteTot.WithLabelValues("fail")
	return c
}

func (c *VoteLogsCollector) CollectorName() string { return "vote_logs" }

// Start subscribes the tailer to bridge and oracle log dirs and consumes matches.
// Returns when ctx is canceled.
//
// R-003: bridge and oracle use SEPARATE pattern sets (oracle ok/fail are
// independent of bridge ok/fail — they live in different Rust modules with
// distinct log formats).
func (c *VoteLogsCollector) Start(ctx context.Context, em *ExporterMetrics) {
	bridgeOK, _ := logtail.CompilePatterns(c.cfg.VoteOKPatterns)
	bridgeFail, _ := logtail.CompilePatterns(c.cfg.VoteFailPatterns)
	disagree, _ := logtail.CompilePatterns(c.cfg.DisagreementPatterns)
	oracleOK, _ := logtail.CompilePatterns(c.cfg.OracleVoteOKPatterns)
	oracleFail, _ := logtail.CompilePatterns(c.cfg.OracleVoteFailPatterns)

	// R-001: children live under ComponentLogDir, not VisorLogDir.
	bridgeDir := filepath.Join(c.cfg.ComponentLogDir, string(config.ComponentBridgeVoter))
	oracleDir := filepath.Join(c.cfg.ComponentLogDir, string(config.ComponentReferenceOraclePublish))

	bridgeCh, err := c.tailer.Subscribe(ctx, bridgeDir, combine(bridgeOK, bridgeFail, disagree))
	if err != nil {
		em.CollectionErrors.WithLabelValues(c.CollectorName(), string(KindIO)).Inc()
		return
	}
	oracleCh, err := c.tailer.Subscribe(ctx, oracleDir, combine(oracleOK, oracleFail, nil))
	if err != nil {
		em.CollectionErrors.WithLabelValues(c.CollectorName(), string(KindIO)).Inc()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-bridgeCh:
			if !ok {
				bridgeCh = nil
				if oracleCh == nil {
					return
				}
				continue
			}
			c.classifyBridge(m, bridgeOK, bridgeFail, disagree)
		case m, ok := <-oracleCh:
			if !ok {
				oracleCh = nil
				if bridgeCh == nil {
					return
				}
				continue
			}
			c.classifyOracle(m, oracleOK, oracleFail)
		}
	}
}

// classifyBridge applies disagreement → ok → fail precedence to one match.
//
// R-003: bridge ok ("scanned ... votes_sent=N") matches the cumulative summary
// line, NOT individual vote events. We therefore use it ONLY to advance the
// last_vote_success_unix gauge — incrementing the counter on every scan tick
// would overstate vote volume. Counter for `status="ok"` stays at 0 by design.
func (c *VoteLogsCollector) classifyBridge(m logtail.Match, okPats, failPats, disagreePats []*regexp.Regexp) {
	if lineMatchesAny(m.Line, disagreePats) {
		c.bridgeDisagreeTot.Inc()
		return
	}
	if lineMatchesAny(m.Line, okPats) {
		c.bridgeLastOK.Set(float64(m.At.Unix()))
		return
	}
	if lineMatchesAny(m.Line, failPats) {
		c.bridgeVoteTot.WithLabelValues("fail").Inc()
		return
	}
}

func (c *VoteLogsCollector) classifyOracle(m logtail.Match, okPats, failPats []*regexp.Regexp) {
	if lineMatchesAny(m.Line, okPats) {
		c.oracleVoteTot.WithLabelValues("ok").Inc()
		c.oracleLastOK.Set(float64(m.At.Unix()))
		return
	}
	if lineMatchesAny(m.Line, failPats) {
		c.oracleVoteTot.WithLabelValues("fail").Inc()
		return
	}
}

func combine(a, b, c []*regexp.Regexp) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(a)+len(b)+len(c))
	out = append(out, a...)
	out = append(out, b...)
	out = append(out, c...)
	return out
}

func lineMatchesAny(line string, set []*regexp.Regexp) bool {
	for _, p := range set {
		if p.MatchString(line) {
			return true
		}
	}
	return false
}
