package collectors

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logfs"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logtail"
)

// VoteLogsCollector tails bridge-voter and reference-oracle-publisher logs,
// counts ok/fail vote attempts and per-RPC provider HTTP failures, and tracks
// last success timestamps.
//
// FR-006 / FR-007 / FR-008 / contracts/metrics.md §B.
//
// Patterns come from config (env override per FR-020). Defaults live in
// internal/config/config.go and capture groups:
//   - VoteOK         : group 1 = votes_sent (\d+)
//   - ProviderFail   : group 1 = provider name, group 2 = HTTP status code
//
// R-024 (2026-06-06 mainnet 2.6h): bridge ok counter now increments by
// captured votes_sent value (was 0-by-design in R-003; that decision was
// based on incorrect "cumulative line" assumption).
//
// R-013 (2026-06-06 mainnet): publisher does NOT emit any "disagreement"
// keyword. The old `vpub_bridge_rpc_disagreement_total` was actually counting
// `WARN ... RPC failed` (= per-provider HTTP failure). Renamed to
// `vpub_bridge_rpc_provider_fail_total{name,status_code}` for clarity.
type VoteLogsCollector struct {
	cfg    *config.Config
	stat   logfs.LogDirStat
	tailer logtail.Tailer

	// Metric handles.
	bridgeVoteTot         *prometheus.CounterVec
	oracleVoteTot         *prometheus.CounterVec
	bridgeProviderFailTot *prometheus.CounterVec
	bridgeLastOK          prometheus.Gauge
	oracleLastOK          prometheus.Gauge

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
		bridgeProviderFailTot: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "bridge",
			Name:      "rpc_provider_fail_total",
			Help:      "Cumulative bridge RPC provider HTTP failures by provider name and status code. FR-006 (R-013: replaces old rpc_disagreement_total, which was misnamed — publisher never emits a real 'disagreement' line).",
		}, []string{"name", "status_code"}),
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
	reg.MustRegister(c.bridgeVoteTot, c.oracleVoteTot, c.bridgeProviderFailTot, c.bridgeLastOK, c.oracleLastOK)
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
	providerFail, _ := logtail.CompilePatterns(c.cfg.ProviderFailPatterns)
	oracleOK, _ := logtail.CompilePatterns(c.cfg.OracleVoteOKPatterns)
	oracleFail, _ := logtail.CompilePatterns(c.cfg.OracleVoteFailPatterns)

	// R-001: children live under ComponentLogDir, not VisorLogDir.
	bridgeDir := filepath.Join(c.cfg.ComponentLogDir, string(config.ComponentBridgeVoter))
	oracleDir := filepath.Join(c.cfg.ComponentLogDir, string(config.ComponentReferenceOraclePublish))

	bridgeCh, err := c.tailer.Subscribe(ctx, bridgeDir, combine(bridgeOK, bridgeFail, providerFail))
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
			c.classifyBridge(m, bridgeOK, bridgeFail, providerFail)
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

// classifyBridge applies provider_fail → ok → fail precedence to one match.
//
// R-024 (mainnet 2026-06-06): bridge ok pattern captures votes_sent=(\d+).
// We Add(N) per scan tick. votes_sent=0 (publisher idle) is skipped — counter
// stays put AND last_vote_success_unix is NOT advanced (a tick with zero votes
// is not a vote success).
//
// R-013 (mainnet 2026-06-06): provider fail pattern captures provider name +
// HTTP status code. Increments per-(name, status_code) counter — replaces the
// misnamed rpc_disagreement_total (publisher never emits real disagreement).
func (c *VoteLogsCollector) classifyBridge(m logtail.Match, okPats, failPats, providerFailPats []*regexp.Regexp) {
	// provider_fail first: most specific (WARN line, only fires on RPC HTTP fail).
	if name, status, matched := firstCapture2(m.Line, providerFailPats); matched {
		c.bridgeProviderFailTot.WithLabelValues(name, status).Inc()
		return
	}
	// ok: capture votes_sent, Add(N) if > 0.
	if n, matched := firstCapture1Int(m.Line, okPats); matched {
		if n > 0 {
			c.bridgeVoteTot.WithLabelValues("ok").Add(float64(n))
			c.bridgeLastOK.Set(float64(m.At.Unix()))
		}
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

// firstCapture1Int returns the first regex's first capture group as int.
// Returns (0, false) if no pattern matches or capture is missing/non-numeric.
func firstCapture1Int(line string, set []*regexp.Regexp) (int, bool) {
	for _, p := range set {
		m := p.FindStringSubmatch(line)
		if len(m) >= 2 {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			return n, true
		}
	}
	return 0, false
}

// firstCapture2 returns the first regex's first two capture groups as strings.
// Returns ("", "", false) if no pattern matches or captures are missing.
func firstCapture2(line string, set []*regexp.Regexp) (string, string, bool) {
	for _, p := range set {
		m := p.FindStringSubmatch(line)
		if len(m) >= 3 {
			return m[1], m[2], true
		}
	}
	return "", "", false
}
