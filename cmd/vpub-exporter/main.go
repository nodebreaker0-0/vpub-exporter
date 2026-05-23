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
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promcollectors "github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vpubcoll "github.com/bharvest/vpub-exporter/internal/collectors"
	"github.com/bharvest/vpub-exporter/internal/config"
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
	log.Printf("vpub-exporter starting: listen=%s service=%s log_dir=%s scrape=%s",
		cfg.ListenAddr, cfg.ServiceName, cfg.LogDir, cfg.ScrapeInterval)
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

	// Phase 3+ wires real collectors onto exMetrics + reg.
	_ = exMetrics

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
