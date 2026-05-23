package collectors

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/nodebreaker0-0/vpub-exporter/internal/slackapi"
)

// SlackHealthCollector exports vpub_slack_api_ok (FR-011).
// auth.test ok=true → 1, else 0. Network failure → 0 (the alert is
// "slack alarms are NOT getting through" — being unable to reach the API
// is the same end result as an invalid token).
type SlackHealthCollector struct {
	client  slackapi.Slack
	token   string
	timeout time.Duration

	ok prometheus.Gauge
}

func NewSlackHealthCollector(reg prometheus.Registerer, client slackapi.Slack, token string) *SlackHealthCollector {
	c := &SlackHealthCollector{
		client:  client,
		token:   token,
		timeout: 5 * time.Second,
		ok: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: MetricNamespace,
			Name:      "slack_api_ok",
			Help:      "1 if Slack auth.test returned ok=true on the last check, else 0. FR-011.",
		}),
	}
	reg.MustRegister(c.ok)
	return c
}

func (c *SlackHealthCollector) CollectorName() string { return "slack_health" }

func (c *SlackHealthCollector) Tick(ctx context.Context) (ErrorKind, error) {
	if c.token == "" {
		// No token configured — collector disabled. Leave gauge at 0 so the
		// alert fires (or rather: deployment without a token is misconfig).
		c.ok.Set(0)
		return "", nil
	}
	pctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	ok, err := c.client.AuthTest(pctx, c.token)
	if err != nil {
		c.ok.Set(0)
		return KindAPI, err
	}
	if ok {
		c.ok.Set(1)
	} else {
		c.ok.Set(0)
	}
	return "", nil
}

func (c *SlackHealthCollector) Start(ctx context.Context, em *ExporterMetrics, interval time.Duration) {
	RunTicker(ctx, em, c.CollectorName(), interval, c.Tick)
}
