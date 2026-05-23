package collectors

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/nodebreaker0-0/vpub-exporter/internal/config"
)

// fakeStat lets us inject deterministic mtimes for the 4 component dirs.
type fakeStat struct {
	mtimes map[string]time.Time
	err    map[string]error
}

func (f *fakeStat) LatestMtime(dir string) (time.Time, string, error) {
	if e, ok := f.err[dir]; ok {
		return time.Time{}, "", e
	}
	if mt, ok := f.mtimes[dir]; ok {
		return mt, filepath.Base(dir) + "-latest", nil
	}
	return time.Time{}, "", nil
}

func gaugeValByLabel(t *testing.T, reg *prometheus.Registry, metricName, labelName, labelValue string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != metricName {
			continue
		}
		for _, m := range mf.Metric {
			for _, l := range m.Label {
				if l.GetName() == labelName && l.GetValue() == labelValue {
					return m.GetGauge().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

func TestLogMtime_AllFourComponentsSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	stat := &fakeStat{mtimes: map[string]time.Time{}}
	c := NewLogMtimeCollector(reg, stat, "/visorlog", "/complog")

	// Before Tick: pre-created zero series for all 4 components.
	for _, comp := range config.AllComponents {
		if _, ok := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", string(comp)); !ok {
			t.Errorf("missing pre-created series for component=%s", comp)
		}
	}
	_ = c
}

func TestLogMtime_VisorAndComponentDirsAreSeparate(t *testing.T) {
	// R-001: visor logs and child component logs live on different trees.
	reg := prometheus.NewRegistry()
	now := time.Unix(1_700_000_000, 0)
	stat := &fakeStat{
		mtimes: map[string]time.Time{
			"/visorlog":                            now,
			"/complog/bridge-voter":               now.Add(-1 * time.Minute),
			"/complog/reference-oracle-publisher": now.Add(-2 * time.Minute),
			"/complog/outcome-voter":              now.Add(-3 * time.Minute),
		},
	}
	c := NewLogMtimeCollector(reg, stat, "/visorlog", "/complog")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if v, _ := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", "visor"); v != float64(now.Unix()) {
		t.Errorf("visor mtime = %v, want %d", v, now.Unix())
	}
	if v, _ := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", "bridge-voter"); v != float64(now.Add(-1*time.Minute).Unix()) {
		t.Errorf("bridge-voter mtime = %v", v)
	}
}

func TestLogMtime_DirErrorKeepsPreviousValue(t *testing.T) {
	reg := prometheus.NewRegistry()
	stat := &fakeStat{
		mtimes: map[string]time.Time{"/visorlog": time.Unix(1_700_000_000, 0)},
		err:    map[string]error{},
	}
	c := NewLogMtimeCollector(reg, stat, "/visorlog", "/complog")

	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	v1, _ := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", "visor")
	if v1 != 1_700_000_000 {
		t.Errorf("visor v1 = %v", v1)
	}

	stat.err["/visorlog"] = errors.New("eperm")
	kind, err := c.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindIO {
		t.Errorf("kind = %q, want io", kind)
	}
	v2, _ := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", "visor")
	if v2 != v1 {
		t.Errorf("visor v2 = %v, want unchanged v1 = %v (preserve last good)", v2, v1)
	}
}

func TestLogMtime_EmptyDirYieldsZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	stat := &fakeStat{mtimes: map[string]time.Time{}} // all dirs return zero
	c := NewLogMtimeCollector(reg, stat, "/visorlog", "/complog")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, comp := range config.AllComponents {
		v, ok := gaugeValByLabel(t, reg, "vpub_component_log_mtime_seconds", "component", string(comp))
		if !ok {
			t.Errorf("missing series for %s", comp)
		}
		if v != 0 {
			t.Errorf("%s = %v, want 0 (empty dir)", comp, v)
		}
	}
}

func TestLogMtime_LabelSetMatchesContract(t *testing.T) {
	reg := prometheus.NewRegistry()
	stat := &fakeStat{mtimes: map[string]time.Time{}}
	_ = NewLogMtimeCollector(reg, stat, "/visorlog", "/complog")
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "vpub_component_log_mtime_seconds" {
			continue
		}
		seen := map[string]bool{}
		for _, m := range mf.Metric {
			for _, l := range m.Label {
				if l.GetName() == "component" {
					seen[l.GetValue()] = true
				}
			}
		}
		want := map[string]bool{"visor": true, "bridge-voter": true, "reference-oracle-publisher": true, "outcome-voter": true}
		for k := range want {
			if !seen[k] {
				t.Errorf("missing label component=%s", k)
			}
		}
		for k := range seen {
			if !want[k] {
				t.Errorf("unexpected label component=%s (cardinality leak)", k)
			}
		}
	}
}

// Quietly assert that prometheus library returned consistent dto types.
var _ = dto.MetricType_GAUGE
