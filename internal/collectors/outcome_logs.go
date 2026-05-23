package collectors

import (
	"context"
	"path/filepath"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bharvest/vpub-exporter/internal/config"
	"github.com/bharvest/vpub-exporter/internal/logtail"
)

// OutcomeLogsCollector tails outcome-voter logs and increments two counters
// (warn / crit) per matched line. FR-009.
type OutcomeLogsCollector struct {
	cfg    *config.Config
	tailer logtail.Tailer

	warn prometheus.Counter
	crit prometheus.Counter
}

func NewOutcomeLogsCollector(reg prometheus.Registerer, cfg *config.Config, t logtail.Tailer) *OutcomeLogsCollector {
	c := &OutcomeLogsCollector{
		cfg:    cfg,
		tailer: t,
		warn: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "outcome",
			Name:      "log_warn_total",
			Help:      "Cumulative WARN-level lines in outcome-voter logs. FR-009.",
		}),
		crit: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: MetricNamespace,
			Subsystem: "outcome",
			Name:      "log_crit_total",
			Help:      "Cumulative CRIT/ERROR-level lines in outcome-voter logs. FR-009.",
		}),
	}
	reg.MustRegister(c.warn, c.crit)
	return c
}

func (c *OutcomeLogsCollector) CollectorName() string { return "outcome_logs" }

func (c *OutcomeLogsCollector) Start(ctx context.Context, em *ExporterMetrics) {
	warnPats, _ := logtail.CompilePatterns(c.cfg.LogWarnPatterns)
	critPats, _ := logtail.CompilePatterns(c.cfg.LogCritPatterns)

	// R-001: outcome-voter lives under ComponentLogDir.
	dir := filepath.Join(c.cfg.ComponentLogDir, string(config.ComponentOutcomeVoter))
	all := combine(warnPats, critPats, nil)
	ch, err := c.tailer.Subscribe(ctx, dir, all)
	if err != nil {
		em.CollectionErrors.WithLabelValues(c.CollectorName(), string(KindIO)).Inc()
		return
	}
	warnSet := warnPats
	critSet := critPats
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			c.classify(m, warnSet, critSet)
		}
	}
}

func (c *OutcomeLogsCollector) classify(m logtail.Match, warn, crit []*regexp.Regexp) {
	// crit takes precedence (a line with both "ERROR" and "warn" counts as crit).
	if lineMatchesAny(m.Line, crit) {
		c.crit.Inc()
		return
	}
	if lineMatchesAny(m.Line, warn) {
		c.warn.Inc()
		return
	}
}
