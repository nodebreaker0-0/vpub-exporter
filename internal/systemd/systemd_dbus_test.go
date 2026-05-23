//go:build linux
// +build linux

package systemd

import (
	"context"
	"errors"
	"testing"
)

type fakeConn struct {
	props    map[string]interface{}
	err      error
	closed   bool
	unitSeen string
}

func (f *fakeConn) GetUnitPropertiesContext(_ context.Context, unit string) (map[string]interface{}, error) {
	f.unitSeen = unit
	return f.props, f.err
}
func (f *fakeConn) Close() { f.closed = true }

func TestDBusProbe_IsActive(t *testing.T) {
	cases := []struct {
		name      string
		state     interface{}
		want      bool
		expectErr bool
	}{
		{"active", "active", true, false},
		{"inactive", "inactive", false, false},
		{"failed", "failed", false, false},
		{"missing", nil, false, true},
		{"wrong-type", 42, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeConn{props: map[string]interface{}{"ActiveState": c.state}}
			p := newDBusProbeWithConn("v-publisher.service", f)
			got, err := p.IsActive()
			if (err != nil) != c.expectErr {
				t.Fatalf("err = %v, expectErr=%v", err, c.expectErr)
			}
			if !c.expectErr && got != c.want {
				t.Errorf("IsActive = %v, want %v", got, c.want)
			}
			if f.unitSeen != "v-publisher.service" {
				t.Errorf("unit asked = %q", f.unitSeen)
			}
		})
	}
}

func TestDBusProbe_MainPID_NRestarts(t *testing.T) {
	f := &fakeConn{props: map[string]interface{}{
		"ActiveState": "active",
		"MainPID":     uint32(1234),
		"NRestarts":   uint32(2),
	}}
	p := newDBusProbeWithConn("v-publisher.service", f)

	pid, err := p.MainPID()
	if err != nil || pid != 1234 {
		t.Errorf("MainPID = %d, %v, want 1234, nil", pid, err)
	}
	n, err := p.NRestarts()
	if err != nil || n != 2 {
		t.Errorf("NRestarts = %d, %v, want 2, nil", n, err)
	}
}

func TestDBusProbe_PropertyTypes(t *testing.T) {
	for _, v := range []interface{}{uint32(7), uint64(7), int32(7), int64(7), int(7)} {
		got, err := uintToInt(v)
		if err != nil || got != 7 {
			t.Errorf("uintToInt(%T %v) = %d, %v", v, v, got, err)
		}
	}
	if _, err := uintToInt(nil); err == nil {
		t.Error("nil should error")
	}
	if _, err := uintToInt("nope"); err == nil {
		t.Error("string should error")
	}
}

func TestDBusProbe_DBusError(t *testing.T) {
	f := &fakeConn{err: errors.New("bus closed")}
	p := newDBusProbeWithConn("v-publisher.service", f)
	if _, err := p.IsActive(); err == nil {
		t.Fatal("expected error")
	}
}

func TestDBusProbe_Close(t *testing.T) {
	f := &fakeConn{}
	p := newDBusProbeWithConn("v-publisher.service", f)
	p.Close()
	if !f.closed {
		t.Error("Close should propagate")
	}
}

// Compile-time assertion: *DBusProbe satisfies ServiceProbe.
var _ ServiceProbe = (*DBusProbe)(nil)
