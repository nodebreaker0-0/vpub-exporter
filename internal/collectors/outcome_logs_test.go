package collectors

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logtail"
)

func TestOutcomeLogs_WarnAndCritCounted(t *testing.T) {
	// R-003: outcome-voter scoped (NOT generic \bwarn\b — that would catch
	// oracle price-drift warnings).
	warnPat := regexp.MustCompile(`WARN\s+validator_publisher::outcome_voter`)
	critPat := regexp.MustCompile(`(CRIT|ERROR)\s+validator_publisher::outcome_voter`)

	cfg := &config.Config{
		ComponentLogDir: "/clog",
		LogWarnPatterns: []string{warnPat.String()},
		LogCritPatterns: []string{critPat.String()},
	}

	dir := "/clog/outcome-voter"
	now := time.Unix(1_700_000_000, 0)
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		dir: {
			{Line: "ts WARN  validator_publisher::outcome_voter: something off", Pattern: warnPat, At: now},
			{Line: "ts ERROR validator_publisher::outcome_voter: oh no", Pattern: critPat, At: now.Add(1 * time.Second)},
			{Line: "ts WARN  validator_publisher::outcome_voter: another", Pattern: warnPat, At: now.Add(2 * time.Second)},
		},
	}}

	reg := prometheus.NewRegistry()
	c := NewOutcomeLogsCollector(reg, cfg, tailer)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	em := NewExporterMetrics(prometheus.NewRegistry())
	done := make(chan struct{})
	go func() { c.Start(ctx, em); close(done) }()
	<-ctx.Done()
	<-done

	if v := testutil.ToFloat64(c.warn); v != 2 {
		t.Errorf("warn = %v, want 2", v)
	}
	if v := testutil.ToFloat64(c.crit); v != 1 {
		t.Errorf("crit = %v, want 1", v)
	}
}
