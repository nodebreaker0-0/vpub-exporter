//go:build linux
// +build linux

package systemd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
)

// unitConn is the slim subset of dbus.Conn that we use. Extracted as an
// interface so tests can swap in a fake (see systemd_dbus_test.go).
type unitConn interface {
	GetUnitPropertiesContext(ctx context.Context, unit string) (map[string]interface{}, error)
	Close()
}

// DBusProbe queries unit state over the system bus.
// Constitution II — strictly read-only methods (no StartUnit/StopUnit).
type DBusProbe struct {
	unit    string
	conn    unitConn
	timeout time.Duration
}

// NewDBusProbe opens a system-bus connection and returns a probe for unit.
// Caller is responsible for calling Close when done.
func NewDBusProbe(ctx context.Context, unit string) (*DBusProbe, error) {
	c, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("dbus connect: %w", err)
	}
	return &DBusProbe{unit: unit, conn: c, timeout: 5 * time.Second}, nil
}

// newDBusProbeWithConn — test helper.
func newDBusProbeWithConn(unit string, c unitConn) *DBusProbe {
	return &DBusProbe{unit: unit, conn: c, timeout: 5 * time.Second}
}

func (p *DBusProbe) Close() {
	if p.conn != nil {
		p.conn.Close()
	}
}

func (p *DBusProbe) props() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	return p.conn.GetUnitPropertiesContext(ctx, p.unit)
}

// IsActive — FR-001. true iff ActiveState == "active".
func (p *DBusProbe) IsActive() (bool, error) {
	m, err := p.props()
	if err != nil {
		return false, err
	}
	v, ok := m["ActiveState"].(string)
	if !ok {
		return false, errors.New("ActiveState property missing")
	}
	return v == "active", nil
}

// MainPID — used to count children (FR-002). 0 if unit is not running.
func (p *DBusProbe) MainPID() (int, error) {
	m, err := p.props()
	if err != nil {
		return 0, err
	}
	return uintToInt(m["MainPID"])
}

// NRestarts — FR-004.
// systemd reports a uint32 here; we expose it as Counter at the metrics layer.
func (p *DBusProbe) NRestarts() (int, error) {
	m, err := p.props()
	if err != nil {
		return 0, err
	}
	return uintToInt(m["NRestarts"])
}

func uintToInt(v interface{}) (int, error) {
	switch x := v.(type) {
	case uint32:
		return int(x), nil
	case uint64:
		return int(x), nil
	case int32:
		return int(x), nil
	case int64:
		return int(x), nil
	case int:
		return x, nil
	case nil:
		return 0, errors.New("property missing")
	default:
		return 0, fmt.Errorf("unexpected property type %T", v)
	}
}
