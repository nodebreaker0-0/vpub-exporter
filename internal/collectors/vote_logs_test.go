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
	// R-003: bridge ok / fail / disagree, oracle ok / fail all distinct patterns.
	bridgeOK := regexp.MustCompile(`bridge_voter::runner: scanned .* votes_sent=[1-9]\d*`)
	bridgeFail := regexp.MustCompile(`CRIT\s+validator_publisher::bridge_voter::runner: critical error vote failed`)
	disagree := regexp.MustCompile(`WARN\s+validator_publisher::bridge_voter::runner: RPC failed`)
	oracleOK := regexp.MustCompile(`reference_oracle_publisher: oracle action sent`)
	oracleFail := regexp.MustCompile(`exchange_client: hyperliquid response status=[45]\d\d`)

	cfg := &config.Config{
		ComponentLogDir:        "/clog",
		VoteOKPatterns:         []string{bridgeOK.String()},
		VoteFailPatterns:       []string{bridgeFail.String()},
		DisagreementPatterns:   []string{disagree.String()},
		OracleVoteOKPatterns:   []string{oracleOK.String()},
		OracleVoteFailPatterns: []string{oracleFail.String()},
	}

	bridgeDir := "/clog/bridge-voter"
	oracleDir := "/clog/reference-oracle-publisher"

	now := time.Unix(1_700_000_000, 0)
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		bridgeDir: {
			{Line: "ts INFO  validator_publisher::bridge_voter::runner: scanned from_block=1 to_block=2 votes_sent=3 votes_failed=0", Pattern: bridgeOK, At: now},
			{Line: "ts CRIT  validator_publisher::bridge_voter::runner: critical error vote failed for deposit foo", Pattern: bridgeFail, At: now.Add(1 * time.Second)},
			{Line: "ts WARN  validator_publisher::bridge_voter::runner: RPC failed alchemy", Pattern: disagree, At: now.Add(2 * time.Second)},
			{Line: "ts INFO  validator_publisher::bridge_voter::runner: scanned from_block=2 to_block=3 votes_sent=1", Pattern: bridgeOK, At: now.Add(3 * time.Second)},
		},
		oracleDir: {
			{Line: "ts INFO  validator_publisher::reference_oracle_publisher: oracle action sent", Pattern: oracleOK, At: now.Add(10 * time.Second)},
			{Line: "ts INFO  validator_publisher::hyperliquid::exchange_client: hyperliquid response status=500", Pattern: oracleFail, At: now.Add(11 * time.Second)},
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

	// R-003: bridge ok counter stays at 0 (cumulative line — only advances timestamp).
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("ok")); v != 0 {
		t.Errorf("bridge ok counter = %v, want 0 (R-003: timestamp-only)", v)
	}
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("bridge fail = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.bridgeDisagreeTot); v != 1 {
		t.Errorf("disagree = %v, want 1", v)
	}
	// oracle: separate counters increment normally.
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("ok")); v != 1 {
		t.Errorf("oracle ok = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("oracle fail = %v, want 1", v)
	}
	// last_vote_success_unix gauges advance to latest matching event.
	if v := testutil.ToFloat64(c.bridgeLastOK); v != float64(now.Add(3*time.Second).Unix()) {
		t.Errorf("bridge last OK = %v, want %d", v, now.Add(3*time.Second).Unix())
	}
	if v := testutil.ToFloat64(c.oracleLastOK); v != float64(now.Add(10*time.Second).Unix()) {
		t.Errorf("oracle last OK = %v, want %d", v, now.Add(10*time.Second).Unix())
	}
}

func TestVoteLogs_InitialGaugesAreExporterStart(t *testing.T) {
	cfg := &config.Config{
		ComponentLogDir:        "/clog",
		VoteOKPatterns:         []string{`ok`},
		VoteFailPatterns:       []string{`fail`},
		DisagreementPatterns:   []string{`disagree`},
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
