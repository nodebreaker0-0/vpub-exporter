// vpub-exporter — Prometheus exporter for hl-validator-publisher.
//
// Constitution direct binding:
//   - I.   Outside-the-Box: this binary runs separately from publisher.
//   - II.  No Side Effects: read-only. Never start/stop/restart publisher.
//   - III. Convention: vpub_ metric prefix, alert_level 5 enum.
//   - IV.  Secrets: env only — never logged, never in metric labels.
//   - V.   Non-Blocking Scrape: /metrics reads in-memory cache only.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vpubcoll "github.com/bharvest/vpub-exporter/internal/collectors"
	"github.com/bharvest/vpub-exporter/internal/config"
	"github.com/bharvest/vpub-exporter/internal/logfs"
	"github.com/bharvest/vpub-exporter/internal/logtail"
	"github.com/bharvest/vpub-exporter/internal/procs"
	"github.com/bharvest/vpub-exporter/internal/rpc"
	"github.com/bharvest/vpub-exporter/internal/slackapi"
	"github.com/bharvest/vpub-exporter/internal/systemd"
)

func main() {
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadFromOS()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	log.Printf("vpub-exporter starting: listen=%s service=%s visor_log=%s component_log=%s scrape=%s",
		cfg.ListenAddr, cfg.ServiceName, cfg.VisorLogDir, cfg.ComponentLogDir, cfg.ScrapeInterval)
	if cfg.HasSlack() {
		log.Printf("slack: enabled (token=****redacted)")
	}
	if cfg.HasBridgeRPC() {
		log.Printf("bridge rpc: %d providers (URLs redacted)", len(cfg.BridgeRPCNames))
	}
	if cfg.HasBinaryRemote() {
		log.Printf("binary remote tracking: enabled")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		promcollectors.NewGoCollector(),
		promcollectors.NewProcessCollector(promcollectors.ProcessCollectorOpts{}),
	)
	exMetrics := vpubcoll.NewExporterMetrics(reg)

	ctx, cancel := signalContext()
	defer cancel()

	// Tier 0 collectors (Phase 3 / US1 / MVP).
	probe, closeProbe := serviceProbe(ctx, cfg.ServiceName)
	defer closeProbe()
	lister := procs.New()
	stat := logfs.New()
	svc := vpubcoll.NewServiceCollector(reg, probe, lister, cfg.ServiceName)
	logmt := vpubcoll.NewLogMtimeCollector(reg, stat, cfg.VisorLogDir, cfg.ComponentLogDir)

	// FR-001 (5s) / FR-002 (10s) / FR-003+004 (30s) — see contracts/metrics.md.
	// Service tick handles up + child_count + restart_total; pick the tightest
	// requested cadence (5s).
	go svc.Start(ctx, exMetrics, 5*time.Second)
	go logmt.Start(ctx, exMetrics, cfg.ScrapeInterval)

	// Tier 1 collectors (Phase 4 / US2). Each is wired only when the
	// corresponding config is present — partial deployment is supported.
	if cfg.HasBridgeRPC() {
		brc := vpubcoll.NewBridgeRPCCollector(reg, rpc.NewHTTPProbe(), cfg.BridgeRPCNames, cfg.BridgeRPCURLs)
		go brc.Start(ctx, exMetrics, cfg.ScrapeInterval) // 30s
	}
	// Log tailers share one OS impl (PollingTailer needs LatestFileFn — we
	// give it logfs.New().LatestMtime, ignoring the returned mtime).
	tailLatest := func(dir string) (string, error) {
		_, name, err := stat.LatestMtime(dir)
		if err != nil || name == "" {
			return "", err
		}
		return filepath.Join(dir, name), nil
	}
	tailer := logtail.NewPolling(tailLatest)
	tailer.PollInterval = 2 * time.Second

	voteLogs := vpubcoll.NewVoteLogsCollector(reg, cfg, stat, tailer)
	outcomeLogs := vpubcoll.NewOutcomeLogsCollector(reg, cfg, tailer)
	go voteLogs.Start(ctx, exMetrics)
	go outcomeLogs.Start(ctx, exMetrics)

	if cfg.HasSlack() {
		slack := slackapi.NewHTTPClient()
		sh := vpubcoll.NewSlackHealthCollector(reg, slack, cfg.SlackBotToken)
		go sh.Start(ctx, exMetrics, 60*time.Second)
		if cfg.OutcomeChannel != "" {
			osCol := vpubcoll.NewOutcomeSlackCollector(reg, slack, cfg.SlackBotToken, cfg.OutcomeChannel)
			go osCol.Start(ctx, exMetrics, 5*time.Minute)
		}
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: 8,
		Timeout:             3 * time.Second, // FR-017 / SC-003
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel2()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Printf("vpub-exporter stopped cleanly")
	return nil
}

// signalContext returns a context canceled on SIGTERM/SIGINT.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

// serviceProbe constructs a real dbus probe, falling back to a probe that
// always errors so the service collector still ticks (and vpub_service_up
// stays at 0, which is the correct alarm signal).
// The returned close func is safe to call even when the fallback is used.
func serviceProbe(ctx context.Context, unit string) (systemd.ServiceProbe, func()) {
	p, err := systemd.NewDBusProbe(ctx, unit)
	if err != nil {
		log.Printf("systemd probe init failed (vpub_service_up will stay 0): %v", err)
		return errProbe{err: err}, func() {}
	}
	return p, p.Close
}

type errProbe struct{ err error }

func (e errProbe) IsActive() (bool, error) { return false, e.err }
func (e errProbe) MainPID() (int, error)   { return 0, e.err }
func (e errProbe) NRestarts() (int, error) { return 0, e.err }
