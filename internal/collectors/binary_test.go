package collectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeProbe implements binary.Probe (LocalMtime + RemoteLastModified).
type fakeBinaryProbe struct {
	localMtime  time.Time
	localErr    error
	remoteMtime time.Time
	remoteErr   error

	localCalls  int
	remoteCalls int
}

func (f *fakeBinaryProbe) LocalMtime(_ string) (time.Time, error) {
	f.localCalls++
	return f.localMtime, f.localErr
}
func (f *fakeBinaryProbe) RemoteLastModified(_ context.Context, _ string) (time.Time, error) {
	f.remoteCalls++
	return f.remoteMtime, f.remoteErr
}

func TestBinary_HappyPath(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	p := &fakeBinaryProbe{
		localMtime:  now.Add(-2 * time.Hour),
		remoteMtime: now,
	}
	c := NewBinaryCollector(reg, p, "/local/visor", "https://example/visor")
	if _, err := c.TickLocal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TickRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(c.localMtime); v != float64(now.Add(-2*time.Hour).Unix()) {
		t.Errorf("local_mtime = %v", v)
	}
	if v := testutil.ToFloat64(c.remoteMtime); v != float64(now.Unix()) {
		t.Errorf("remote_mtime = %v", v)
	}
	if v := testutil.ToFloat64(c.remoteOK); v != 1 {
		t.Errorf("remote_ok = %v, want 1", v)
	}
}

func TestBinary_LocalErrorKeepsPreviousValue(t *testing.T) {
	reg := prometheus.NewRegistry()
	p := &fakeBinaryProbe{localMtime: time.Unix(1_700_000_000, 0)}
	c := NewBinaryCollector(reg, p, "/local/visor", "")

	// Round 1: success → 1700000000
	_, _ = c.TickLocal(context.Background())
	v1 := testutil.ToFloat64(c.localMtime)

	// Round 2: missing file → keep v1
	p.localErr = errors.New("ENOENT")
	kind, err := c.TickLocal(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindIO {
		t.Errorf("kind = %q, want io", kind)
	}
	if v := testutil.ToFloat64(c.localMtime); v != v1 {
		t.Errorf("local_mtime = %v, want unchanged %v", v, v1)
	}
}

func TestBinary_RemoteErrorFlipsOKAndPreservesMtime(t *testing.T) {
	reg := prometheus.NewRegistry()
	good := time.Unix(1_700_000_000, 0)
	p := &fakeBinaryProbe{remoteMtime: good}
	c := NewBinaryCollector(reg, p, "/local/visor", "https://example/visor")

	// Round 1: success → mtime + ok=1
	_, _ = c.TickRemote(context.Background())
	if v := testutil.ToFloat64(c.remoteOK); v != 1 {
		t.Fatalf("first ok = %v", v)
	}
	v1 := testutil.ToFloat64(c.remoteMtime)

	// Round 2: HEAD timeout → ok=0, mtime preserved
	p.remoteErr = context.DeadlineExceeded
	kind, err := c.TickRemote(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindAPI {
		t.Errorf("kind = %q", kind)
	}
	if v := testutil.ToFloat64(c.remoteOK); v != 0 {
		t.Errorf("remote_ok = %v, want 0 on error", v)
	}
	if v := testutil.ToFloat64(c.remoteMtime); v != v1 {
		t.Errorf("remote_mtime = %v, want preserved %v", v, v1)
	}
}

func TestBinary_RemoteDisabledWhenURLEmpty(t *testing.T) {
	reg := prometheus.NewRegistry()
	p := &fakeBinaryProbe{remoteMtime: time.Unix(99, 0)}
	c := NewBinaryCollector(reg, p, "/local/visor", "")
	if _, err := c.TickRemote(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.remoteCalls != 0 {
		t.Errorf("RemoteLastModified should NOT be called when URL empty (got %d)", p.remoteCalls)
	}
}

func TestBinary_UpdateAvailableSemantics(t *testing.T) {
	// remote_mtime - local_mtime > 60 == "upgrade available"
	// This test validates the gauge values feeding the alert rule expr.
	reg := prometheus.NewRegistry()
	base := time.Unix(1_700_000_000, 0)
	p := &fakeBinaryProbe{
		localMtime:  base,
		remoteMtime: base.Add(120 * time.Second),
	}
	c := NewBinaryCollector(reg, p, "/local/visor", "https://example/visor")
	_, _ = c.TickLocal(context.Background())
	_, _ = c.TickRemote(context.Background())

	delta := testutil.ToFloat64(c.remoteMtime) - testutil.ToFloat64(c.localMtime)
	if delta != 120 {
		t.Errorf("remote - local = %v, want 120 (rule threshold > 60)", delta)
	}
}
