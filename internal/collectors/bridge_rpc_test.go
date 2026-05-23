package collectors

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/bharvest/vpub-exporter/internal/rpc"
)

type fakeRPC struct {
	results map[string]struct {
		lat time.Duration
		err error
	}
	calls atomic.Int32
}

func (f *fakeRPC) Probe(_ context.Context, url string) (time.Duration, error) {
	f.calls.Add(1)
	r, ok := f.results[url]
	if !ok {
		return 0, errors.New("no result configured")
	}
	return r.lat, r.err
}

func TestBridgeRPC_SevenRPCsThreeDown(t *testing.T) {
	names := []string{"alchemy", "infura", "quicknode", "chainstack", "ankr", "drpc", "custom1"}
	urls := map[string]string{}
	for i, n := range names {
		urls[n] = "https://" + n
		_ = i
	}
	f := &fakeRPC{results: map[string]struct {
		lat time.Duration
		err error
	}{
		urls["alchemy"]:    {lat: 80 * time.Millisecond, err: nil},
		urls["infura"]:     {lat: 120 * time.Millisecond, err: nil},
		urls["quicknode"]:  {lat: 90 * time.Millisecond, err: nil},
		urls["chainstack"]: {lat: 150 * time.Millisecond, err: nil},
		urls["ankr"]:       {lat: 0, err: errors.New("connect refused")},
		urls["drpc"]:       {lat: 0, err: errors.New("dns")},
		urls["custom1"]:    {lat: 0, err: context.DeadlineExceeded},
	}}

	reg := prometheus.NewRegistry()
	c := NewBridgeRPCCollector(reg, f, names, urls)
	c.parallelProbes = 7
	if _, err := c.Tick(context.Background()); err == nil {
		t.Fatal("expected aggregate error (3 RPCs down)")
	}
	// up gauge: 4 alive, 3 down
	mfs, _ := reg.Gather()
	var ups int
	for _, mf := range mfs {
		if mf.GetName() != "vpub_bridge_rpc_up" {
			continue
		}
		for _, m := range mf.Metric {
			if m.GetGauge().GetValue() == 1 {
				ups++
			}
		}
	}
	if ups != 4 {
		t.Errorf("alive up gauges = %d, want 4", ups)
	}
	// check_total: at least 1 timeout (custom1)
	var timeouts float64
	for _, mf := range mfs {
		if mf.GetName() != "vpub_bridge_rpc_check_total" {
			continue
		}
		for _, m := range mf.Metric {
			isTO := false
			for _, l := range m.Label {
				if l.GetName() == "status" && l.GetValue() == "timeout" {
					isTO = true
				}
			}
			if isTO {
				timeouts += m.GetCounter().GetValue()
			}
		}
	}
	if timeouts < 1 {
		t.Errorf("timeout count = %v, want ≥1", timeouts)
	}
}

func TestBridgeRPC_AuthErrorIncrementsAuthStatus(t *testing.T) {
	// R-004 / FR-005 보강: rpc.ErrAuth → status="auth_error" counter.
	urls := map[string]string{"alchemy": "https://alchemy"}
	f := &fakeRPC{results: map[string]struct {
		lat time.Duration
		err error
	}{
		urls["alchemy"]: {lat: 50 * time.Millisecond, err: fmt.Errorf("%w: http 401", rpc.ErrAuth)},
	}}
	reg := prometheus.NewRegistry()
	c := NewBridgeRPCCollector(reg, f, []string{"alchemy"}, urls)
	_, _ = c.Tick(context.Background())

	mfs, _ := reg.Gather()
	var auth float64
	for _, mf := range mfs {
		if mf.GetName() != "vpub_bridge_rpc_check_total" {
			continue
		}
		for _, m := range mf.Metric {
			isAuth := false
			for _, l := range m.Label {
				if l.GetName() == "status" && l.GetValue() == "auth_error" {
					isAuth = true
				}
			}
			if isAuth {
				auth += m.GetCounter().GetValue()
			}
		}
	}
	if auth < 1 {
		t.Errorf("auth_error counter = %v, want ≥1", auth)
	}
	if got := upValue(t, reg, "alchemy"); got != 0 {
		t.Errorf("up after auth fail = %v, want 0", got)
	}
}

func TestBridgeRPC_EmptyConfig_NoOp(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewBridgeRPCCollector(reg, &fakeRPC{}, nil, nil)
	kind, err := c.Tick(context.Background())
	if err != nil || kind != "" {
		t.Errorf("empty config tick: kind=%q err=%v", kind, err)
	}
}

func TestBridgeRPC_SuccessClearsPreviousDown(t *testing.T) {
	urls := map[string]string{"alchemy": "u"}
	r := &fakeRPC{results: map[string]struct {
		lat time.Duration
		err error
	}{
		"u": {lat: 50 * time.Millisecond, err: errors.New("first fail")},
	}}
	reg := prometheus.NewRegistry()
	c := NewBridgeRPCCollector(reg, r, []string{"alchemy"}, urls)

	// First tick: failure → up=0
	_, _ = c.Tick(context.Background())
	if got := upValue(t, reg, "alchemy"); got != 0 {
		t.Errorf("after fail up = %v, want 0", got)
	}
	// Second tick: success → up=1
	r.results["u"] = struct {
		lat time.Duration
		err error
	}{lat: 50 * time.Millisecond, err: nil}
	_, _ = c.Tick(context.Background())
	if got := upValue(t, reg, "alchemy"); got != 1 {
		t.Errorf("after recover up = %v, want 1", got)
	}
}

func upValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "vpub_bridge_rpc_up" {
			continue
		}
		for _, m := range mf.Metric {
			for _, l := range m.Label {
				if l.GetName() == "name" && l.GetValue() == name {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return -1
}

// Ensures dto package usage stays compiled (some tests above may not exercise it directly).
var _ = dto.MetricType_GAUGE
