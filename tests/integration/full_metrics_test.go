package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nodebreaker0-0/vpub-exporter/internal/binary"
	vpubcoll "github.com/nodebreaker0-0/vpub-exporter/internal/collectors"
	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/slackapi"
)

// expectedMetrics — every vpub_* metric name that MUST appear on /metrics
// once all collectors are wired in. If a new metric is added it must be
// added here too; if one disappears, this test catches the regression.
//
// Scope is restricted to Tier 2 (R-019) and exporter self-metrics — the
// Tier 0/1 collectors require systemd / live log dirs / Arbitrum RPC to
// register their series, which we do not stand up in the in-process test.
// Coverage for those is via per-package unit tests (`internal/collectors/*_test.go`).
//
// Sourced from contracts/metrics.md §C and §D.
var expectedMetrics = []string{
	// Tier 2 — per-component (R-019)
	"vpub_binary_local_mtime_unix",
	"vpub_binary_remote_mtime_unix",
	"vpub_binary_remote_check_ok",
	"vpub_binary_download_started_unix",
	// Exporter self
	"vpub_exporter_collection_duration_seconds",
	"vpub_exporter_collection_errors_total",
}

// TestFullMetrics_AllCollectorsExpose verifies that, given a registry with
// every collector booted, the metrics surface includes every name we promise
// in contracts/metrics.md. Collector-internal ticks may fail (no live RPC /
// no systemd), but registration alone is enough — gauges with no observed
// value still appear as "# HELP / # TYPE" lines on /metrics.
func TestFullMetrics_AllCollectorsExpose(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		promcollectors.NewGoCollector(),
		promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{}),
	)
	em := vpubcoll.NewExporterMetrics(reg)

	// Force-touch one collection error so the *_total counter has a sample
	// and shows up on /metrics (counters without an observation are missing
	// from the text format).
	em.CollectionErrors.WithLabelValues("boot", "io").Inc()
	em.CollectionDuration.WithLabelValues("boot").Observe(0.001)

	// Tier 2 — multi-component binary tracker. We need real files so the
	// gauges actually pick up a value (otherwise the GaugeVec stays empty
	// and the metric name disappears from /metrics text format).
	dir := t.TempDir()
	mkfile := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	bc := vpubcoll.NewBinaryCollector(reg, binary.NewHTTPProbe(),
		map[config.ComponentName]string{
			config.ComponentVisor:                  mkfile("visor"),
			config.ComponentBridgeVoter:            mkfile("bridge-voter"),
			config.ComponentOutcomeVoter:           mkfile("outcome-voter"),
			config.ComponentReferenceOraclePublish: mkfile("reference-oracle-publisher"),
		},
		// Empty URL → TickRemote noop. We seed remote_mtime/check_ok directly below.
		"")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = bc.TickLocal(ctx)

	// Seed remote_mtime / remote_check_ok via the public gauges so the
	// series exist on /metrics without standing up a fake HTTP server.
	bc.SeedRemoteForTest("visor")

	// Slack health collector — register only, no live token.
	sh := vpubcoll.NewSlackHealthCollector(reg, slackapi.NewHTTPClient(), "xoxb-FAKE")
	_, _ = sh.Tick(ctx)

	// download_started gauge needs at least one observation. Use the
	// public seed helper rather than spinning up a real visor log file.
	vpubcoll.SeedDownloadStartedForTest(reg, "bridge-voter")

	body := scrapeMetrics(t, reg)

	missing := []string{}
	for _, name := range expectedMetrics {
		// Look for "# HELP <name> " or the metric line itself.
		if !strings.Contains(body, "# HELP "+name+" ") &&
			!strings.Contains(body, "\n"+name+"{") &&
			!strings.Contains(body, "\n"+name+" ") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("missing metrics on /metrics:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestFullMetrics_AllSeriesUseVpubPrefix(t *testing.T) {
	reg := prometheus.NewRegistry()
	em := vpubcoll.NewExporterMetrics(reg)
	em.CollectionErrors.WithLabelValues("boot", "io").Inc()

	bc := vpubcoll.NewBinaryCollector(reg, binary.NewHTTPProbe(),
		map[config.ComponentName]string{config.ComponentVisor: "/tmp/no"},
		"")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = bc.TickLocal(ctx)

	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i > 0 {
			name = line[:i]
		}
		if !strings.HasPrefix(name, "vpub_") {
			// Allow Go and process self-metrics — those are not registered
			// here. Anything else means a custom metric escaped the prefix.
			t.Errorf("non-vpub_ series leaked onto /metrics: %q", name)
		}
	}
}
