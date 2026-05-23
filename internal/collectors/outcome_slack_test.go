package collectors

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeSlack struct {
	authOK    bool
	authErr   error
	historyN  int
	historyErr error
	historyCalls int
}

func (f *fakeSlack) AuthTest(_ context.Context, _ string) (bool, error) { return f.authOK, f.authErr }
func (f *fakeSlack) History24h(_ context.Context, _, _ string) (int, error) {
	f.historyCalls++
	return f.historyN, f.historyErr
}

func TestOutcomeSlack_HappyPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{historyN: 7}
	c := NewOutcomeSlackCollector(reg, s, "tok", "C12345")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(c.gauge); v != 7 {
		t.Errorf("gauge = %v, want 7", v)
	}
}

func TestOutcomeSlack_ErrorKeepsPreviousValue(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{historyN: 5}
	c := NewOutcomeSlackCollector(reg, s, "tok", "C12345")
	// 1st: success → 5
	_, _ = c.Tick(context.Background())
	// 2nd: rate-limit error → keep 5
	s.historyErr = errors.New("429")
	s.historyN = 0
	_, err := c.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if v := testutil.ToFloat64(c.gauge); v != 5 {
		t.Errorf("gauge = %v, want 5 (last good)", v)
	}
}

func TestOutcomeSlack_DisabledWhenEmptyConfig(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{historyN: 99}
	c := NewOutcomeSlackCollector(reg, s, "", "")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.historyCalls != 0 {
		t.Errorf("History24h should NOT be called when token/channel empty (got %d calls)", s.historyCalls)
	}
}
