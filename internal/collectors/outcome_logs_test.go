package collectors

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bharvest/vpub-exporter/internal/config"
	"github.com/bharvest/vpub-exporter/internal/logtail"
)

func TestOutcomeLogs_WarnAndCritCounted(t *testing.T) {
	warnPat := regexp.MustCompile(`(?i)\bwarn\b`)
	critPat := regexp.MustCompile(`(?i)\bcrit`)

	cfg := &config.Config{
		ComponentLogDir:  "/clog",
		LogWarnPatterns:  []string{warnPat.String()},
		LogCritPatterns:  []string{critPat.String()},
	}

	dir := "/clog/outcome-voter"
	now := time.Unix(1_700_000_000, 0)
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		dir: {
			{Line: "x warn y", Pattern: warnPat, At: now},
			{Line: "fatal crit z", Pattern: critPat, At: now.Add(1 * time.Second)},
			{Line: "another warn", Pattern: warnPat, At: now.Add(2 * time.Second)},
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
