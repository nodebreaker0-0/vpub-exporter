package collectors

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSlackHealth_OK(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{authOK: true}
	c := NewSlackHealthCollector(reg, s, "tok")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(c.ok); v != 1 {
		t.Errorf("ok = %v, want 1", v)
	}
}

func TestSlackHealth_InvalidToken(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{authOK: false}
	c := NewSlackHealthCollector(reg, s, "tok")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(c.ok); v != 0 {
		t.Errorf("ok = %v, want 0 (ok=false from server)", v)
	}
}

func TestSlackHealth_NetworkError(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{authErr: errors.New("dns")}
	c := NewSlackHealthCollector(reg, s, "tok")
	kind, err := c.Tick(context.Background())
	if err == nil || kind != KindAPI {
		t.Fatalf("err=%v kind=%q", err, kind)
	}
	if v := testutil.ToFloat64(c.ok); v != 0 {
		t.Errorf("ok = %v, want 0 (network error)", v)
	}
}

func TestSlackHealth_EmptyToken(t *testing.T) {
	reg := prometheus.NewRegistry()
	s := &fakeSlack{authOK: true}
	c := NewSlackHealthCollector(reg, s, "")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := testutil.ToFloat64(c.ok); v != 0 {
		t.Errorf("ok = %v, want 0 (empty token = disabled)", v)
	}
}
