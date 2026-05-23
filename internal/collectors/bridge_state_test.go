package collectors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func writeState(t *testing.T, dir, name string, block int64, mtime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := []byte(`{"last_scanned_block": ` + itoa(block) + `, "transactions": {"0xabc": "queued"}}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int64) string {
	// Avoid strconv import dance — the test file uses tiny numbers.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestBridgeState_HappyPath(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	path := writeState(t, dir, "bridge-voter-testnet-state.json", 12345678, now)

	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, path)
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if v := testutil.ToFloat64(c.lastBlock); v != 12345678 {
		t.Errorf("last_scanned_block = %v, want 12345678", v)
	}
	if v := testutil.ToFloat64(c.mtime); v != float64(now.Unix()) {
		t.Errorf("mtime = %v, want %d", v, now.Unix())
	}
}

func TestBridgeState_BlockAdvances(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	path := writeState(t, dir, "bridge-voter-testnet-state.json", 100, now)

	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, path)
	_, _ = c.Tick(context.Background())

	later := now.Add(60 * time.Second)
	writeState(t, dir, "bridge-voter-testnet-state.json", 200, later)
	_, _ = c.Tick(context.Background())

	if v := testutil.ToFloat64(c.lastBlock); v != 200 {
		t.Errorf("last_scanned_block = %v, want 200", v)
	}
	if v := testutil.ToFloat64(c.mtime); v != float64(later.Unix()) {
		t.Errorf("mtime = %v, want %d", v, later.Unix())
	}
}

func TestBridgeState_MissingFile(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, "/nope/state.json")
	kind, err := c.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindIO {
		t.Errorf("kind = %q, want io", kind)
	}
}

func TestBridgeState_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, path)
	kind, err := c.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if kind != KindParse {
		t.Errorf("kind = %q, want parse", kind)
	}
}

func TestBridgeState_DisabledWhenPathEmpty(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, "")
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatal("disabled tick should not error")
	}
	if v := testutil.ToFloat64(c.lastBlock); v != 0 {
		t.Errorf("disabled lastBlock = %v, want 0", v)
	}
}

func TestBridgeState_DoesNotReadConfigJSON(t *testing.T) {
	// Constitution IV regression: config.json sits in the same directory but
	// MUST NOT be opened by this collector. We verify by placing a poisoned
	// config.json (would fail JSON parse) and a valid state.json — collector
	// must succeed reading only state.json.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("THIS IS NOT JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_500, 0)
	path := writeState(t, dir, "bridge-voter-mainnet-state.json", 9876543210, now)

	reg := prometheus.NewRegistry()
	c := NewBridgeStateCollector(reg, path)
	if _, err := c.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v (config.json must not be touched)", err)
	}
	if v := testutil.ToFloat64(c.lastBlock); v != 9876543210 {
		t.Errorf("last_scanned_block = %v", v)
	}
}
