package collectors

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeProbe implements systemd.ServiceProbe.
type fakeProbe struct {
	active     bool
	activeErr  error
	mainPID    int
	mainPIDErr error
	nRestarts  int
	nRestErr   error
}

func (f *fakeProbe) IsActive() (bool, error)  { return f.active, f.activeErr }
func (f *fakeProbe) MainPID() (int, error)    { return f.mainPID, f.mainPIDErr }
func (f *fakeProbe) NRestarts() (int, error)  { return f.nRestarts, f.nRestErr }

// fakeLister implements procs.ChildLister.
type fakeLister struct {
	count int
	err   error

	lastParent int
}

func (f *fakeLister) CountChildren(parentPID int) (int, error) {
	f.lastParent = parentPID
	return f.count, f.err
}

func newSvc(t *testing.T, probe *fakeProbe, lister *fakeLister) (*ServiceCollector, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return NewServiceCollector(reg, probe, lister, "v-publisher.service"), reg
}

func TestService_ActiveAndChildrenSet(t *testing.T) {
	probe := &fakeProbe{active: true, mainPID: 1000, nRestarts: 0}
	lister := &fakeLister{count: 3}
	svc, _ := newSvc(t, probe, lister)
	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if v := testutil.ToFloat64(svc.up); v != 1 {
		t.Errorf("up = %v, want 1", v)
	}
	if v := testutil.ToFloat64(svc.childCount); v != 3 {
		t.Errorf("child_count = %v, want 3", v)
	}
	if lister.lastParent != 1000 {
		t.Errorf("lister called with parent = %d, want 1000", lister.lastParent)
	}
}

func TestService_InactiveServiceForcesChildZero(t *testing.T) {
	probe := &fakeProbe{active: false, mainPID: 0}
	lister := &fakeLister{count: 99} // should not be called
	svc, _ := newSvc(t, probe, lister)
	if _, err := svc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if v := testutil.ToFloat64(svc.up); v != 0 {
		t.Errorf("up = %v, want 0", v)
	}
	if v := testutil.ToFloat64(svc.childCount); v != 0 {
		t.Errorf("child_count = %v, want 0 (no MainPID)", v)
	}
	if lister.lastParent != 0 {
		t.Errorf("lister should not have been called with MainPID=0; got parent=%d", lister.lastParent)
	}
}

func TestService_ActiveStateErrorSetsUpZero(t *testing.T) {
	probe := &fakeProbe{activeErr: errors.New("dbus disconnected")}
	lister := &fakeLister{count: 3}
	svc, _ := newSvc(t, probe, lister)
	kind, err := svc.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindAPI {
		t.Errorf("error kind = %q, want api", kind)
	}
	if v := testutil.ToFloat64(svc.up); v != 0 {
		t.Errorf("up = %v, want 0 on error", v)
	}
}

func TestService_RestartCounterMonotonic(t *testing.T) {
	probe := &fakeProbe{active: true, mainPID: 1, nRestarts: 0}
	lister := &fakeLister{count: 3}
	svc, _ := newSvc(t, probe, lister)

	// initial tick — counter 0
	_, _ = svc.Tick(context.Background())
	if v := testutil.ToFloat64(svc.restartTotal); v != 0 {
		t.Errorf("restart_total initial = %v, want 0", v)
	}
	// systemd reports 2 — counter += 2
	probe.nRestarts = 2
	_, _ = svc.Tick(context.Background())
	if v := testutil.ToFloat64(svc.restartTotal); v != 2 {
		t.Errorf("restart_total = %v, want 2", v)
	}
	// systemd resets to 0 (e.g. reset-failed) — counter must NOT decrease
	probe.nRestarts = 0
	_, _ = svc.Tick(context.Background())
	if v := testutil.ToFloat64(svc.restartTotal); v != 2 {
		t.Errorf("restart_total after reset = %v, want 2 (monotonic)", v)
	}
	// systemd reports 3 after rebase — only the +3 delta is added on top
	probe.nRestarts = 3
	_, _ = svc.Tick(context.Background())
	if v := testutil.ToFloat64(svc.restartTotal); v != 5 {
		t.Errorf("restart_total = %v, want 5 (2 + 3)", v)
	}
}

func TestService_MetricNamesMatchContract(t *testing.T) {
	probe := &fakeProbe{active: true, mainPID: 1, nRestarts: 0}
	lister := &fakeLister{count: 3}
	svc, reg := newSvc(t, probe, lister)
	_, _ = svc.Tick(context.Background())

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"vpub_service_up":            false,
		"vpub_child_count":           false,
		"vpub_service_restart_total": false,
	}
	for _, mf := range mfs {
		if _, ok := want[mf.GetName()]; ok {
			want[mf.GetName()] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("metric %q not exposed", n)
		}
	}
}

func TestService_ChildListerErrorDoesNotMaskOtherSources(t *testing.T) {
	probe := &fakeProbe{active: true, mainPID: 1000, nRestarts: 1}
	lister := &fakeLister{err: errors.New("readdir denied")}
	svc, _ := newSvc(t, probe, lister)
	kind, err := svc.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error from lister")
	}
	if kind != KindIO {
		t.Errorf("error kind = %q, want io", kind)
	}
	if v := testutil.ToFloat64(svc.up); v != 1 {
		t.Errorf("up should still be 1, got %v", v)
	}
	if v := testutil.ToFloat64(svc.restartTotal); v != 1 {
		t.Errorf("restart_total = %v, want 1 (lister failure must not block dbus)", v)
	}
}
