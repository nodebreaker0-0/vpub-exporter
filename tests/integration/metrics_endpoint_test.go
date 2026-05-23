package integration

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vpubcoll "github.com/nodebreaker0-0/vpub-exporter/internal/collectors"
)

// bootRegistry mirrors what cmd/vpub-exporter/main.go does at boot, so the
// integration test exercises the real /metrics surface area for Phase 2.
// Returns the registry plus the exporter self-metrics struct (so tests can
// force observations to validate the HELP/TYPE lines on /metrics).
func bootRegistry(t *testing.T) (*prometheus.Registry, *vpubcoll.ExporterMetrics) {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		promcollectors.NewGoCollector(),
		promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{}),
	)
	em := vpubcoll.NewExporterMetrics(reg)
	return reg, em
}

func TestMetricsEndpoint_ServesGoAndProcessSelf(t *testing.T) {
	reg, _ := bootRegistry(t)
	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(body)

	want := []string{
		"go_goroutines",
		"process_start_time_seconds",
	}
	for _, s := range want {
		if !strings.Contains(text, s) {
			t.Errorf("/metrics missing %q", s)
		}
	}
}

func TestMetricsEndpoint_VpubSelfMetricsAppearAfterObservation(t *testing.T) {
	reg, em := bootRegistry(t)
	em.CollectionDuration.WithLabelValues("test").Observe(0.001)
	em.CollectionErrors.WithLabelValues("test", "io").Inc()

	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(body)

	for _, s := range []string{
		"vpub_exporter_collection_duration_seconds",
		"vpub_exporter_collection_errors_total",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("/metrics missing %q", s)
		}
	}
}

func TestMetricsEndpoint_NoNonVpubCustomMetrics(t *testing.T) {
	// Constitution III: all custom metrics use the vpub_ prefix. Built-in
	// go_/process_ are allowed because they come from prometheus client_golang
	// stock collectors. No other custom prefix should leak in.
	reg, em := bootRegistry(t)
	em.CollectionDuration.WithLabelValues("test").Observe(0.001)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, mf := range mfs {
		name := mf.GetName()
		switch {
		case strings.HasPrefix(name, "vpub_"):
		case strings.HasPrefix(name, "go_"):
		case strings.HasPrefix(name, "process_"):
		default:
			t.Errorf("unexpected metric %q (only vpub_/go_/process_ allowed)", name)
		}
	}
}

func TestMetricsEndpoint_StableUnderRepeatedScrape(t *testing.T) {
	// Phase 2 cache may be empty — must still 200 without panic.
	reg, _ := bootRegistry(t)
	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))
	defer srv.Close()

	for i := 0; i < 5; i++ {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET iter %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("iter %d status = %d", i, resp.StatusCode)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
