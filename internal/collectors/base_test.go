package collectors

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestNewExporterMetrics_Registers(t *testing.T) {
	reg := prometheus.NewRegistry()
	em := NewExporterMetrics(reg)
	em.CollectionDuration.WithLabelValues("svc").Observe(0.05)
	em.CollectionErrors.WithLabelValues("svc", string(KindIO)).Inc()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	want := []string{"vpub_exporter_collection_duration_seconds", "vpub_exporter_collection_errors_total"}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing metric %q in registry", w)
		}
	}
}

func TestRunTicker_FiresImmediatelyAndOnTick(t *testing.T) {
	reg := prometheus.NewRegistry()
	em := NewExporterMetrics(reg)

	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunTicker(ctx, em, "test", 30*time.Millisecond, func(ctx context.Context) (ErrorKind, error) {
			atomic.AddInt32(&calls, 1)
			return "", nil
		})
		close(done)
	}()

	// Wait until at least 3 ticks fire.
	deadline := time.After(500 * time.Millisecond)
	for atomic.LoadInt32(&calls) < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d ticks fired", atomic.LoadInt32(&calls))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	<-done
}

func TestRunTicker_RecordsErrorByKind(t *testing.T) {
	reg := prometheus.NewRegistry()
	em := NewExporterMetrics(reg)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	RunTicker(ctx, em, "svc", 30*time.Millisecond, func(ctx context.Context) (ErrorKind, error) {
		return KindAPI, errors.New("boom")
	})

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	var got float64
	for _, mf := range mfs {
		if mf.GetName() != "vpub_exporter_collection_errors_total" {
			continue
		}
		for _, m := range mf.Metric {
			if labelEq(m.Label, "collector", "svc") && labelEq(m.Label, "kind", "api") {
				got = m.GetCounter().GetValue()
			}
		}
	}
	if got < 1 {
		t.Errorf("collection_errors{collector=svc,kind=api} = %v, want ≥1", got)
	}
}

func TestCache_StoreLoad(t *testing.T) {
	c := &Cache{}
	c.Store(42)
	if v, ok := c.Load().(int); !ok || v != 42 {
		t.Errorf("Load = %v, want 42", c.Load())
	}
}

func labelEq(lbls []*dto.LabelPair, name, value string) bool {
	for _, l := range lbls {
		if l.GetName() == name && l.GetValue() == value {
			return true
		}
	}
	return false
}
