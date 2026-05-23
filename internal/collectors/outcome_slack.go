package collectors

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/slackapi"
)

// OutcomeSlackCollector exports vpub_outcome_slack_msg_24h.
// FR-010. Rate-limit safety: on error, KEEP the previous cached value
// (do not zero the gauge — that would create a false "all clear" signal).
type OutcomeSlackCollector struct {
	client    slackapi.Slack
	token     string
	channel   string
	timeout   time.Duration

	gauge prometheus.Gauge

	mu      sync.Mutex
	lastVal float64
	hasVal  bool
}

func NewOutcomeSlackCollector(reg prometheus.Registerer, client slackapi.Slack, token, channel string) *OutcomeSlackCollector {
	c := &OutcomeSlackCollector{
		client:  client,
		token:   token,
		channel: channel,
		timeout: 5 * time.Second,
		gauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Subsystem: "outcome",
			Name:      "slack_msg_24h",
			Help:      "Approximate number of messages in the outcome_actions Slack channel over the last 24h. FR-010.",
		}),
	}
	reg.MustRegister(c.gauge)
	return c
}

func (c *OutcomeSlackCollector) CollectorName() string { return "outcome_slack" }

func (c *OutcomeSlackCollector) Tick(ctx context.Context) (ErrorKind, error) {
	if c.token == "" || c.channel == "" {
		return "", nil
	}
	pctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	n, err := c.client.History24h(pctx, c.token, c.channel)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		// Keep previous value (FR-010 fallback). On the very first call this
		// means staying at 0 — alert rule has a `for: 30m` to absorb that.
		if c.hasVal {
			c.gauge.Set(c.lastVal)
		}
		return KindAPI, err
	}
	c.lastVal = float64(n)
	c.hasVal = true
	c.gauge.Set(c.lastVal)
	return "", nil
}

func (c *OutcomeSlackCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
