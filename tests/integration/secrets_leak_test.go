package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nodebreaker0-0/vpub-exporter/internal/binary"
	vpubcoll "github.com/nodebreaker0-0/vpub-exporter/internal/collectors"
	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/slackapi"
)

// Constitution IV regression gate.
//
// We inject highly-specific fake secrets that must NEVER appear in:
//   1. /metrics body (any line — HELP / TYPE / labels / values)
//   2. metric label values
//   3. metric HELP text
//   4. anywhere on the registry's text output
//
// The values are crafted so a single substring match guarantees a real leak
// (no false-positive from a real prometheus label).
const (
	leakSlackToken   = "xoxb-LEAK-9999999999-9999999999-Z9Z9Z9Z9Z9Z9Z9Z9Z9Z9Z9Z9"
	leakBearer       = "Bearer LEAK-7777777777777777777777777"
	leakAgentKey     = "0xLEAK000000000000000000000000000000000000000000000000000000000001"
	leakRPCURL       = "https://leak-rpc-7777.example/v9/LEAK-API-KEY-9999"
	leakPagerDutyKey = "leak-pd-1234567890abcdef1234567890abcdef"
)

// leakNeedles enumerates every fake secret string we injected. Tests scan
// for these exact substrings — any occurrence inside the metrics body is a
// hard fail.
var leakNeedles = []string{
	leakSlackToken,
	leakBearer,
	leakAgentKey,
	leakRPCURL,
	leakPagerDutyKey,
}

// Additional generic patterns that should never escape (defense in depth).
// A leak via an unexpected new metric/label would also catch these.
var leakRegexen = []*regexp.Regexp{
	regexp.MustCompile(`xoxb-[A-Za-z0-9-]+`),         // Slack bot token
	regexp.MustCompile(`xapp-[A-Za-z0-9-]+`),         // Slack app token
	regexp.MustCompile(`0x[a-fA-F0-9]{40}\b`),        // ETH-style address (40 hex)
	regexp.MustCompile(`0x[a-fA-F0-9]{64}\b`),        // 32-byte private key
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]+`),
	regexp.MustCompile(`(?i)alchemy\.com/v2/[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)infura\.io/v3/[A-Za-z0-9_-]+`),
}

// bootSecretsRegistry stands up a registry with every collector that could
// plausibly leak. We don't have a publisher running, so the upstream calls
// will all fail — but the gauges still get registered and exposed.
func bootSecretsRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	em := vpubcoll.NewExporterMetrics(reg)
	_ = em

	// Binary collector with the visor announce URL and per-component paths.
	bc := vpubcoll.NewBinaryCollector(reg, binary.NewHTTPProbe(),
		map[config.ComponentName]string{
			config.ComponentVisor:                  "/tmp/no/such/visor",
			config.ComponentBridgeVoter:            "/tmp/no/such/bv",
			config.ComponentOutcomeVoter:           "/tmp/no/such/ov",
			config.ComponentReferenceOraclePublish: "/tmp/no/such/rop",
		},
		"https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = bc.TickLocal(ctx)
	_, _ = bc.TickRemote(ctx)

	// Slack health collector with the leak token. We do NOT actually start
	// the ticker (would need live HTTP); we just register the metric and
	// run a single tick which will fail on connect but must NOT echo the
	// token into any error path that surfaces through /metrics.
	sh := vpubcoll.NewSlackHealthCollector(reg, slackapi.NewHTTPClient(), leakSlackToken)
	_, _ = sh.Tick(ctx)

	return reg
}

func scrapeMetrics(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	srv := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestMetrics_NoNeedleLeaks(t *testing.T) {
	// Env injection — collector ticks may pick these up.
	t.Setenv("VPUB_SLACK_BOT_TOKEN", leakSlackToken)
	t.Setenv("VPUB_RPC_ALCHEMY_URL", leakRPCURL)
	t.Setenv("VPUB_AGENT_KEY", leakAgentKey)
	t.Setenv("PAGERDUTY_API_KEY", leakPagerDutyKey)

	reg := bootSecretsRegistry(t)
	body := scrapeMetrics(t, reg)

	for _, n := range leakNeedles {
		if strings.Contains(body, n) {
			t.Errorf("LEAK: needle %q found in /metrics body — Constitution IV violation", n)
		}
	}
}

func TestMetrics_NoGenericSecretPatterns(t *testing.T) {
	reg := bootSecretsRegistry(t)
	body := scrapeMetrics(t, reg)

	for _, re := range leakRegexen {
		if m := re.FindString(body); m != "" {
			// Allow-list: HELP text on legitimate metrics may mention
			// "VPUB_BINARY_URL" or "VPUB_SLACK_BOT_TOKEN" by NAME (env-var
			// names are not secrets). We reject only actual values matching
			// the secret-shaped regex.
			t.Errorf("LEAK: pattern %v matched %q in /metrics body", re, m)
		}
	}
}

// Source tree scan — code itself must not embed any real-looking secret.
// Catches "left a debug token in a comment" mistakes.
func TestSourceTree_NoEmbeddedSecrets(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// skip git/bin/vendor dirs
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "vendor" || name == "bin" || name == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		// only scan Go / yaml / md / env / sh / Makefile
		switch ext := filepath.Ext(path); ext {
		case ".go", ".yaml", ".yml", ".md", ".env", ".sh", ".toml":
		default:
			if filepath.Base(path) != "Makefile" {
				return nil
			}
		}
		// skip our own test files (they intentionally embed leak fixtures)
		if strings.HasSuffix(path, "secrets_leak_test.go") {
			return nil
		}
		// skip example env file (env-var NAMES are fine; we look for VALUES)
		if strings.HasSuffix(path, "vpub-exporter.env.example") {
			return nil
		}
		// skip alertmanager.toml of monitoring repo (we don't write it)
		if strings.Contains(path, "/monitoring/config/alarmer/") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		// Look for real-looking secret values, NOT names.
		for _, re := range []*regexp.Regexp{
			regexp.MustCompile(`xoxb-\d{10,}-\d{10,}-[A-Za-z0-9]{20,}`),
			regexp.MustCompile(`0x[a-fA-F0-9]{64}\b`),
		} {
			if m := re.FindString(text); m != "" {
				offenders = append(offenders, path+": "+m)
			}
		}
		return nil
	})
	if len(offenders) > 0 {
		t.Errorf("source tree leaks (Constitution IV):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
