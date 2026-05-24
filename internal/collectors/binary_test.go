package collectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
)

// fakeBinaryProbe implements binary.Probe. After R-019 a single probe is shared
// across all components — LocalMtime gets called once per component per tick.
type fakeBinaryProbe struct {
	// Per-path local mtime / error. Missing key = use defaults.
	byPath    map[string]time.Time
	byPathErr map[string]error

	// Remote (only visor URL is hit).
	remoteMtime time.Time
	remoteErr   error

	localCalls  map[string]int
	remoteCalls int
}

func newFakeProbe() *fakeBinaryProbe {
	return &fakeBinaryProbe{
		byPath:     map[string]time.Time{},
		byPathErr:  map[string]error{},
		localCalls: map[string]int{},
	}
}

func (f *fakeBinaryProbe) LocalMtime(path string) (time.Time, error) {
	f.localCalls[path]++
	if e, ok := f.byPathErr[path]; ok {
		return time.Time{}, e
	}
	if t, ok := f.byPath[path]; ok {
		return t, nil
	}
	return time.Time{}, errors.New("ENOENT")
}

func (f *fakeBinaryProbe) RemoteLastModified(_ context.Context, _ string) (time.Time, error) {
	f.remoteCalls++
	return f.remoteMtime, f.remoteErr
}

func allFourTargets() map[config.ComponentName]string {
	return map[config.ComponentName]string{
		config.ComponentVisor:                  "/v/visor",
		config.ComponentBridgeVoter:            "/v/bridge-voter",
		config.ComponentOutcomeVoter:           "/v/outcome-voter",
		config.ComponentReferenceOraclePublish: "/v/reference-oracle-publisher",
	}
}

func TestBinary_HappyPath_AllComponentsLabeled(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	p := newFakeProbe()
	for _, path := range allFourTargets() {
		p.byPath[path] = now.Add(-2 * time.Hour)
	}
	p.remoteMtime = now

	c := NewBinaryCollector(reg, p, allFourTargets(), "https://example/visor")
	if _, err := c.TickLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TickRemote(context.Background()); err != nil {
		t.Fatal(err)
	}

	// All 4 components must have local mtime set.
	for _, name := range []config.ComponentName{
		config.ComponentVisor,
		config.ComponentBridgeVoter,
		config.ComponentOutcomeVoter,
		config.ComponentReferenceOraclePublish,
	} {
		g := c.localMtime.WithLabelValues(string(name))
		if v := testutil.ToFloat64(g); v != float64(now.Add(-2*time.Hour).Unix()) {
			t.Errorf("local_mtime{component=%s} = %v", name, v)
		}
	}

	// Only visor has remote mtime + ok.
	visorRemote := c.remoteMtime.WithLabelValues(string(config.ComponentVisor))
	if v := testutil.ToFloat64(visorRemote); v != float64(now.Unix()) {
		t.Errorf("remote_mtime{visor} = %v", v)
	}
	visorOK := c.remoteOK.WithLabelValues(string(config.ComponentVisor))
	if v := testutil.ToFloat64(visorOK); v != 1 {
		t.Errorf("remote_ok{visor} = %v, want 1", v)
	}
}

func TestBinary_LocalErrorKeepsPreviousValue(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	targets := map[config.ComponentName]string{
		config.ComponentVisor: "/v/visor",
	}
	p := newFakeProbe()
	p.byPath["/v/visor"] = now
	c := NewBinaryCollector(reg, p, targets, "")

	// Round 1: success
	_, _ = c.TickLocal(context.Background())
	v1 := testutil.ToFloat64(c.localMtime.WithLabelValues(string(config.ComponentVisor)))
	if v1 != float64(now.Unix()) {
		t.Fatalf("round1 = %v", v1)
	}

	// Round 2: missing file → KindIO returned, gauge value preserved
	delete(p.byPath, "/v/visor")
	p.byPathErr["/v/visor"] = errors.New("ENOENT")
	kind, err := c.TickLocal(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindIO {
		t.Errorf("kind = %q, want io", kind)
	}
	if v := testutil.ToFloat64(c.localMtime.WithLabelValues(string(config.ComponentVisor))); v != v1 {
		t.Errorf("local_mtime preserved? got %v, want %v", v, v1)
	}
}

func TestBinary_OneComponentMissingDoesNotBlindOthers(t *testing.T) {
	// R-019 invariant: if child file is missing, visor still gets stat'd.
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	p := newFakeProbe()
	p.byPath["/v/visor"] = now
	p.byPathErr["/v/bridge-voter"] = errors.New("ENOENT")
	targets := map[config.ComponentName]string{
		config.ComponentVisor:       "/v/visor",
		config.ComponentBridgeVoter: "/v/bridge-voter",
	}
	c := NewBinaryCollector(reg, p, targets, "")

	// Errors are reported but loop continues.
	if _, err := c.TickLocal(context.Background()); err == nil {
		t.Fatal("expected ENOENT error")
	}
	v := testutil.ToFloat64(c.localMtime.WithLabelValues(string(config.ComponentVisor)))
	if v != float64(now.Unix()) {
		t.Errorf("visor mtime not set despite child error: %v", v)
	}
}

func TestBinary_RemoteErrorFlipsOKAndPreservesMtime(t *testing.T) {
	reg := prometheus.NewRegistry()
	good := time.Unix(1_700_000_000, 0)
	p := newFakeProbe()
	p.remoteMtime = good
	c := NewBinaryCollector(reg, p, allFourTargets(), "https://example/visor")
	visor := string(config.ComponentVisor)

	_, _ = c.TickRemote(context.Background())
	if v := testutil.ToFloat64(c.remoteOK.WithLabelValues(visor)); v != 1 {
		t.Fatalf("first ok = %v", v)
	}
	v1 := testutil.ToFloat64(c.remoteMtime.WithLabelValues(visor))

	p.remoteErr = context.DeadlineExceeded
	kind, err := c.TickRemote(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindAPI {
		t.Errorf("kind = %q", kind)
	}
	if v := testutil.ToFloat64(c.remoteOK.WithLabelValues(visor)); v != 0 {
		t.Errorf("remote_ok = %v, want 0 on error", v)
	}
	if v := testutil.ToFloat64(c.remoteMtime.WithLabelValues(visor)); v != v1 {
		t.Errorf("remote_mtime preserved? got %v, want %v", v, v1)
	}
}

func TestBinary_RemoteDisabledWhenURLEmpty(t *testing.T) {
	reg := prometheus.NewRegistry()
	p := newFakeProbe()
	p.remoteMtime = time.Unix(99, 0)
	c := NewBinaryCollector(reg, p, allFourTargets(), "")
	if _, err := c.TickRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.remoteCalls != 0 {
		t.Errorf("RemoteLastModified should NOT be called when URL empty (got %d)", p.remoteCalls)
	}
}

func TestBinary_UpdateAvailableSemantics_VisorOnly(t *testing.T) {
	// The visor update rule expr is:
	//   (remote_mtime{component="visor"} - local_mtime{component="visor"}) > 60
	// This test validates the labeled gauges feed that expr correctly.
	reg := prometheus.NewRegistry()
	base := time.Unix(1_700_000_000, 0)
	p := newFakeProbe()
	p.byPath["/v/visor"] = base
	p.remoteMtime = base.Add(120 * time.Second)
	c := NewBinaryCollector(reg, p, map[config.ComponentName]string{
		config.ComponentVisor: "/v/visor",
	}, "https://example/visor")
	_, _ = c.TickLocal(context.Background())
	_, _ = c.TickRemote(context.Background())

	visor := string(config.ComponentVisor)
	delta := testutil.ToFloat64(c.remoteMtime.WithLabelValues(visor)) -
		testutil.ToFloat64(c.localMtime.WithLabelValues(visor))
	if delta != 120 {
		t.Errorf("remote - local = %v, want 120 (rule threshold > 60)", delta)
	}
}
