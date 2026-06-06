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

// Production lines captured from mainnet host (ip-137-13-79-13) on 2026-05-25
// during the --tmp-dir version-skew restart loop.
const (
	visorRestartLineOracle = `2026-05-25T18:25:29.818967 INFO  visor: restarting process binary_path="/home/ubuntu/v-publisher/reference-oracle-publisher" height=1 n_restarts=14`
	visorRestartLineBridge = `2026-05-25T18:25:30.123456 INFO  visor: restarting process binary_path="/home/ubuntu/v-publisher/bridge-voter" height=1 n_restarts=2`
	visorRestartLineOutcome = `2026-05-25T18:25:31.789012 INFO  visor: restarting process binary_path="/home/ubuntu/v-publisher/outcome-voter" height=3 n_restarts=5`
	visorCritExited = `2026-05-25T18:25:34.833055 CRIT  visor: critical error managed process exited unexpectedly binary_name="reference-oracle-publisher" binary_path=/home/ubuntu/v-publisher/reference-oracle-publisher height=1 n_restarts=14 exit_status=exit status: 2`
	visorCritRunFailed = `2026-05-25T07:18:32.985608 CRIT  visor: critical error visor run failed error=Invalid cross-device link (os error 18)`
	// Lines that must NOT match.
	visorSpawnLine = `2026-05-25T07:19:16.250192 INFO  visor: spawning new process binary_path="/home/ubuntu/v-publisher/bridge-voter" height=1`
	visorDownloadLine = `2026-05-25T07:19:16.212130 INFO  visor: downloading new binary self.binary_name="bridge-voter" binary_url="..." height=1`

	// R-025: new publisher build emits `validator_publisher::visor::` module prefix.
	visorRestartLineOracleNew = `2026-06-06T11:50:09.000000 INFO  validator_publisher::visor: restarting process binary_path="/home/ubuntu/v-publisher/reference-oracle-publisher" height=11 n_restarts=1`
	visorCritExitedNew = `2026-06-06T11:50:15.000000 CRIT  validator_publisher::visor: critical error managed process exited unexpectedly binary_name="bridge-voter"`
)

func TestVisorLog_ChildRestartCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)

	visorDir := "/v/log"
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		visorDir: {
			{Line: visorRestartLineOracle, At: now},
			{Line: visorRestartLineOracle, At: now.Add(5 * time.Second)},
			{Line: visorRestartLineBridge, At: now.Add(6 * time.Second)},
			{Line: visorRestartLineOutcome, At: now.Add(7 * time.Second)},
			{Line: visorSpawnLine, At: now.Add(8 * time.Second)},     // must not match
			{Line: visorDownloadLine, At: now.Add(9 * time.Second)},  // must not match
		},
	}}

	c := NewVisorLogCollector(reg, visorDir, tailer)
	em := NewExporterMetrics(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go c.Start(ctx, em)
	<-ctx.Done()

	wantOracle := 2.0
	wantBridge := 1.0
	wantOutcome := 1.0

	if v := testutil.ToFloat64(c.childRestart.WithLabelValues(string(config.ComponentReferenceOraclePublish))); v != wantOracle {
		t.Errorf("oracle restart = %v, want %v", v, wantOracle)
	}
	if v := testutil.ToFloat64(c.childRestart.WithLabelValues(string(config.ComponentBridgeVoter))); v != wantBridge {
		t.Errorf("bridge-voter restart = %v, want %v", v, wantBridge)
	}
	if v := testutil.ToFloat64(c.childRestart.WithLabelValues(string(config.ComponentOutcomeVoter))); v != wantOutcome {
		t.Errorf("outcome-voter restart = %v, want %v", v, wantOutcome)
	}
}

func TestVisorLog_CritCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)

	visorDir := "/v/log"
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		visorDir: {
			{Line: visorCritExited, At: now},
			{Line: visorCritRunFailed, At: now.Add(1 * time.Second)},
			{Line: visorSpawnLine, At: now.Add(2 * time.Second)},  // not CRIT
			{Line: visorRestartLineOracle, At: now.Add(3 * time.Second)}, // restart, not CRIT
		},
	}}

	c := NewVisorLogCollector(reg, visorDir, tailer)
	em := NewExporterMetrics(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go c.Start(ctx, em)
	<-ctx.Done()

	if v := testutil.ToFloat64(c.visorCrit); v != 2.0 {
		t.Errorf("visor_crit = %v, want 2 (two CRIT lines)", v)
	}
}

func TestVisorLog_PatternRegression(t *testing.T) {
	// Belt-and-suspenders: if HF changes the log format we want a fast fail.
	cases := []struct {
		line       string
		isRestart  bool
		component  string
		isCrit     bool
	}{
		{visorRestartLineOracle, true, "reference-oracle-publisher", false},
		{visorRestartLineBridge, true, "bridge-voter", false},
		{visorRestartLineOutcome, true, "outcome-voter", false},
		{visorCritExited, false, "", true},
		{visorCritRunFailed, false, "", true},
		{visorSpawnLine, false, "", false},
		{visorDownloadLine, false, "", false},
		// R-025 new build module prefix.
		{visorRestartLineOracleNew, true, "reference-oracle-publisher", false},
		{visorCritExitedNew, false, "", true},
	}
	for _, tc := range cases {
		isRestart := false
		comp := ""
		if sub := VisorChildRestartPattern.FindStringSubmatch(tc.line); len(sub) >= 2 {
			isRestart = true
			comp = sub[1]
		}
		isCrit := VisorCritPattern.MatchString(tc.line) && !isRestart // restart 우선
		if isRestart != tc.isRestart {
			t.Errorf("isRestart = %v, want %v for line %q", isRestart, tc.isRestart, tc.line)
		}
		if isRestart && comp != tc.component {
			t.Errorf("component = %q, want %q for line %q", comp, tc.component, tc.line)
		}
		if isCrit != tc.isCrit {
			t.Errorf("isCrit = %v, want %v for line %q", isCrit, tc.isCrit, tc.line)
		}
	}
}
