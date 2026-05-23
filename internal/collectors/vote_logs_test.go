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
	okPat := regexp.MustCompile(`(?i)vote.*(submitted|ok)`)
	failPat := regexp.MustCompile(`(?i)vote.*(fail|error)`)
	disagreePat := regexp.MustCompile(`(?i)disagree`)

	cfg := &config.Config{
		LogDir: "/log",
		VoteOKPatterns:       []string{okPat.String()},
		VoteFailPatterns:     []string{failPat.String()},
		DisagreementPatterns: []string{disagreePat.String()},
	}

	bridgeDir := "/log/bridge-voter"
	oracleDir := "/log/reference-oracle-publisher"

	now := time.Unix(1_700_000_000, 0)
	tailer := &fakeTailer{emit: map[string][]logtail.Match{
		bridgeDir: {
			{Line: "vote submitted", Pattern: okPat, At: now},
			{Line: "vote failed", Pattern: failPat, At: now.Add(1 * time.Second)},
			{Line: "rpc disagree", Pattern: disagreePat, At: now.Add(2 * time.Second)},
			{Line: "vote OK", Pattern: okPat, At: now.Add(3 * time.Second)},
		},
		oracleDir: {
			{Line: "vote ok", Pattern: okPat, At: now.Add(10 * time.Second)},
			{Line: "vote error", Pattern: failPat, At: now.Add(11 * time.Second)},
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

	// bridge: ok=2, fail=1, disagree=1
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("ok")); v != 2 {
		t.Errorf("bridge ok = %v, want 2", v)
	}
	if v := testutil.ToFloat64(c.bridgeVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("bridge fail = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.bridgeDisagreeTot); v != 1 {
		t.Errorf("disagree = %v, want 1", v)
	}
	// oracle: ok=1, fail=1
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("ok")); v != 1 {
		t.Errorf("oracle ok = %v, want 1", v)
	}
	if v := testutil.ToFloat64(c.oracleVoteTot.WithLabelValues("fail")); v != 1 {
		t.Errorf("oracle fail = %v, want 1", v)
	}
	// last vote success unix gauges advanced past initial (exporter start time)
	bridgeLast := testutil.ToFloat64(c.bridgeLastOK)
	oracleLast := testutil.ToFloat64(c.oracleLastOK)
	if bridgeLast != float64(now.Add(3*time.Second).Unix()) {
		t.Errorf("bridge last OK = %v, want %d", bridgeLast, now.Add(3*time.Second).Unix())
	}
	if oracleLast != float64(now.Add(10*time.Second).Unix()) {
		t.Errorf("oracle last OK = %v, want %d", oracleLast, now.Add(10*time.Second).Unix())
	}
}

func TestVoteLogs_InitialGaugesAreExporterStart(t *testing.T) {
	cfg := &config.Config{
		LogDir:               "/log",
		VoteOKPatterns:       []string{`ok`},
		VoteFailPatterns:     []string{`fail`},
		DisagreementPatterns: []string{`disagree`},
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
