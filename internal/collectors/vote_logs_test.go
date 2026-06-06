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

// fakeTailer emits a fixed sequence of Matches per subscribed directory.
type fakeTailer struct {
	emit map[string][]logtail.Match // dir → matches
}

func (f *fakeTailer) Subscribe(ctx context.Context, dir string, _ []*regexp.Regexp) (<-chan logtail.Match, error) {
	ch := make(chan logtail.Match, 16)
	go func() {
		defer close(ch)
		for _, m := range f.emit[dir] {
			select {
			case ch <- m:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return ch, nil
}

func TestVoteLogs_MixedBridgeAndOracle(t *testing.T) {
	// R-024 / R-013 (mainnet 2026-06-06):
	//   - bridge ok    : capture votes_sent=(\d+); Add(N), skip on N=0.
	//   - bridge fail  : CRIT vote failed (per-deposit event).
	//   - provider fail: WARN ... provider="X" ... status NNN — capture name + code.
	//   - oracle ok    : INFO ... oracle action sent (1 line = 1 vote).
	//   - oracle fail  : CRIT ... critical error failed to publish oracle action.
	bridgeOK := regexp.MustCompile(`bridge_voter::runner: scanned .* votes_sent=(\d+)`)
	bridgeFail := regexp.MustCompile(`CRIT\s+validator_publisher::bridge_voter::runner: critical error vote failed`)
	providerFail := regexp.MustCompile(`WARN\s+validator_publisher::bridge_voter::runner: RPC failed.*provider="([^"]+)".*status (\d{3})`)
	oracleOK := regexp.MustCompile(`reference_oracle_publisher: oracle action sent`)
	oracleFail := regexp.MustCompile(`CRIT\s+validator_publisher::reference_oracle_publisher.*: critical error failed to publish oracle action`)

	cfg := &config.Config{
		ComponentLogDir:        "/clog",
		VoteOKPatterns:         []string{bridgeOK.String()},
		VoteFailPatterns:       []string{bridgeFail.String()},
		ProviderFailPatterns:   []string{providerFail.String()},
		OracleVoteOKPatterns:   []string{oracleOK.String()},
		OracleVoteFailPatterns: []string{oracleFail.String()},
	}

	bridgeDir := "/clog/bridge-voter"
	oracleDir := "/clog/reference-oracle-publisher"

	now := time.Unix(1_700_000_000, 0)
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		bridgeDir: {
			// 3 votes sent on first scan → counter +3.
			{Line: "ts INFO  validator_publisher::bridge_voter::runner: scanned from_block=1 to_block=2 candidates_seen=3 votes_sent=3 votes_failed=0", Pattern: bridgeOK, At: now},
			// 1 CRIT vote failure → counter +1 on fail.
			{Line: "ts CRIT  validator_publisher::bridge_voter::runner: critical error vote failed for deposit foo", Pattern: bridgeFail, At: now.Add(1 * time.Second)},
			// drpc 500 → provider_fail{drpc,500} +1.
			{Line: `ts WARN  validator_publisher::bridge_voter::runner: RPC failed eth_getLogs provider="drpc" from_block=1 error=HTTP request returned status 500: ...`, Pattern: providerFail, At: now.Add(2 * time.Second)},
			// chainstack 403 → provider_fail{chainstack,403} +1.
			{Line: `ts WARN  validator_publisher::bridge_voter::runner: RPC failed eth_getLogs provider="chainstack" from_block=1 error=HTTP request returned status 403: ...`, Pattern: providerFail, At: now.Add(2*time.Second + 500*time.Millisecond)},
			// 1 more vote sent → counter += 1 (total ok = 4).
			{Line: "ts INFO  validator_publisher::bridge_voter::runner: scanned from_block=2 to_block=3 votes_sent=1", Pattern: bridgeOK, At: now.Add(3 * time.Second)},
			// Idle scan (votes_sent=0) — counter stays, last_vote NOT advanced.
			{Line: "ts INFO  validator_publisher::bridge_voter::runner: scanned from_block=3 to_block=4 votes_sent=0", Pattern: bridgeOK, At: now.Add(5 * time.Second)},
		},
		oracleDir: {
			{Line: "ts INFO  validator_publisher::reference_oracle_publisher: oracle action sent", Pattern: oracleOK, At: now.Add(10 * time.Second)},
			{Line: "ts CRIT  validator_publisher::reference_oracle_publisher: critical error failed to publish oracle action error=Hyperliquid returned exchange error: Oracle price update too often", Pattern: oracleFail, At: now.Add(11 * time.Second)},
		},
	}}

	reg := prometheus.NewRegistry()
	c := NewVoteLogsCollector(reg, cfg, nil, tailer)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	em := NewExporterMetrics(prometheus.NewRegistry())
	go func() {
		c.Start(ctx, em)
		close(done)
	}()
	<-ctx.Done()
	<-done

	// R-024: bridge ok counter += votes_sent. 3 + 1 + 0(skip) = 4.
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("ok")); v != 4 {
		t.Errorf("bridge ok counter = %v, want 4 (votes_sent 3+1+skip0)", v)
	}
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("bridge fail = %v, want 1", v)
	}
	// R-013: provider_fail per (name, status_code).
	if v := testutil.ToFloat64(c.bridgeProviderFailTot.WithLabelValues("drpc", "500")); v != 1 {
		t.Errorf("provider_fail{drpc,500} = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.bridgeProviderFailTot.WithLabelValues("chainstack", "403")); v != 1 {
		t.Errorf("provider_fail{chainstack,403} = %v, want 1", v)
	}
	// oracle: separate counters increment normally.
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("ok")); v != 1 {
		t.Errorf("oracle ok = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("oracle fail = %v, want 1", v)
	}
	// bridge last_vote: advanced to second non-zero scan (3s), NOT to the
	// idle scan (5s) — votes_sent=0 must not advance the gauge.
	if v := testutil.ToFloat64(c.bridgeLastOK); v != float64(now.Add(3*time.Second).Unix()) {
		t.Errorf("bridge last OK = %v, want %d (skip idle scan)", v, now.Add(3*time.Second).Unix())
	}
	if v := testutil.ToFloat64(c.oracleLastOK); v != float64(now.Add(10*time.Second).Unix()) {
		t.Errorf("oracle last OK = %v, want %d", v, now.Add(10*time.Second).Unix())
	}
}

func TestVoteLogs_InitialGaugesAreExporterStart(t *testing.T) {
	cfg := &config.Config{
		ComponentLogDir:        "/clog",
		VoteOKPatterns:         []string{`scanned .* votes_sent=(\d+)`},
		VoteFailPatterns:       []string{`fail`},
		ProviderFailPatterns:   []string{`provider="([^"]+)".*status (\d{3})`},
		OracleVoteOKPatterns:   []string{`oracle_ok`},
		OracleVoteFailPatterns: []string{`oracle_fail`},
	}
	reg := prometheus.NewRegistry()
	before := float64(time.Now().Unix())
	c := NewVoteLogsCollector(reg, cfg, nil, &fakeTailer{})
	after := float64(time.Now().Unix())

	if v := testutil.ToFloat64(c.bridgeLastOK); v < before || v > after {
		t.Errorf("bridge initial = %v, want in [%v, %v]", v, before, after)
	}
	if v := testutil.ToFloat64(c.oracleLastOK); v < before || v > after {
		t.Errorf("oracle initial = %v, want in [%v, %v]", v, before, after)
	}
}
