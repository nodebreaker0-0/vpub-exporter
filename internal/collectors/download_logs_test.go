package collectors

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
	"github.com/nodebreaker0-0/vpub-exporter/internal/logtail"
)

// Lines as they actually appear in /home/admin/v-publisher/log/visor/YYYYMMDD
// on the testnet machine (LSN-D13958). Old build (2026-05-22) used target name
// `visor`; new build (2026-06-06 mainnet upgrade) emits the full module path
// `validator_publisher::visor`. R-025: both must match.
const (
	// Old build (target=visor).
	bridgeDownloadLine = `2026-05-22T06:00:08.038142 INFO  visor: downloading new binary self.binary_name="bridge-voter" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/bridge-voter/1" height=1`
	outcomeDownloadLine = `2026-05-22T06:00:08.009784 INFO  visor: downloading new binary self.binary_name="outcome-voter" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/outcome-voter/2" height=2`
	oracleDownloadLine = `2026-05-22T06:00:08.023116 INFO  visor: downloading new binary self.binary_name="reference-oracle-publisher" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/reference-oracle-publisher/1" height=1`
	// New build (validator_publisher::visor module path, R-025).
	bridgeDownloadLineNew = `2026-06-06T11:50:04.674145 INFO  validator_publisher::visor: downloading new binary self.binary_name="bridge-voter" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/bridge-voter/10" height=10`
	outcomeDownloadLineNew = `2026-06-06T11:50:04.670582 INFO  validator_publisher::visor: downloading new binary self.binary_name="outcome-voter" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/outcome-voter/24" height=24`
	oracleDownloadLineNew = `2026-06-06T11:50:04.676313 INFO  validator_publisher::visor: downloading new binary self.binary_name="reference-oracle-publisher" binary_url="https://binaries.hyperliquid-testnet.xyz/validator-publisher/reference-oracle-publisher/11" height=11`
	// "visor" itself should never appear as self.binary_name — guard against that anyway.
	bogusVisorLine = `2026-05-22T06:00:08.000000 INFO  visor: downloading new binary self.binary_name="visor" binary_url="..." height=1`
	// Non-matching lines (must not affect gauges).
	noiseLine = `2026-05-22T06:00:10.603755 INFO  visor: spawning new process binary_path="/home/admin/v-publisher/bridge-voter" height=1`
)

func TestDownloadLogs_RecordsTimestampsPerComponent(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)

	visorDir := "/v/log"
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		visorDir: {
			{Line: bridgeDownloadLine, Pattern: VisorDownloadPattern, At: now},
			{Line: outcomeDownloadLine, Pattern: VisorDownloadPattern, At: now.Add(1 * time.Second)},
			{Line: oracleDownloadLine, Pattern: VisorDownloadPattern, At: now.Add(2 * time.Second)},
			{Line: noiseLine, Pattern: VisorDownloadPattern, At: now.Add(3 * time.Second)},
			{Line: bogusVisorLine, Pattern: VisorDownloadPattern, At: now.Add(4 * time.Second)},
		},
	}}

	c := NewDownloadLogsCollector(reg, visorDir, tailer)
	em := NewExporterMetrics(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go c.Start(ctx, em)
	<-ctx.Done()

	wantBridge := float64(now.Unix())
	wantOutcome := float64(now.Add(1 * time.Second).Unix())
	wantOracle := float64(now.Add(2 * time.Second).Unix())

	if v := testutil.ToFloat64(c.downloadStarted.WithLabelValues(string(config.ComponentBridgeVoter))); v != wantBridge {
		t.Errorf("bridge-voter = %v, want %v", v, wantBridge)
	}
	if v := testutil.ToFloat64(c.downloadStarted.WithLabelValues(string(config.ComponentOutcomeVoter))); v != wantOutcome {
		t.Errorf("outcome-voter = %v, want %v", v, wantOutcome)
	}
	if v := testutil.ToFloat64(c.downloadStarted.WithLabelValues(string(config.ComponentReferenceOraclePublish))); v != wantOracle {
		t.Errorf("reference-oracle-publisher = %v, want %v", v, wantOracle)
	}

	// "visor" must NEVER appear as a label — guard against label cardinality
	// blowup if HF ever logs a misnamed component.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "vpub_binary_download_started_unix" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "component" && l.GetValue() == string(config.ComponentVisor) {
					t.Errorf("visor must not appear as a download_started component label")
				}
			}
		}
	}
}

func TestDownloadLogs_PatternMatchesProductionLines(t *testing.T) {
	// Belt-and-suspenders against regex drift. If HF changes the log format,
	// this test fails fast and we re-derive the regex from fresh logs.
	// R-025: old `INFO  visor:` AND new `INFO  validator_publisher::visor:` both match.
	cases := []struct {
		line string
		want string
	}{
		// Old build.
		{bridgeDownloadLine, "bridge-voter"},
		{outcomeDownloadLine, "outcome-voter"},
		{oracleDownloadLine, "reference-oracle-publisher"},
		// New build (R-025).
		{bridgeDownloadLineNew, "bridge-voter"},
		{outcomeDownloadLineNew, "outcome-voter"},
		{oracleDownloadLineNew, "reference-oracle-publisher"},
	}
	for _, tc := range cases {
		sub := VisorDownloadPattern.FindStringSubmatch(tc.line)
		if len(sub) < 2 {
			t.Errorf("no match: %q", tc.line)
			continue
		}
		if sub[1] != tc.want {
			t.Errorf("component = %q, want %q (line=%q)", sub[1], tc.want, tc.line)
		}
	}
}
