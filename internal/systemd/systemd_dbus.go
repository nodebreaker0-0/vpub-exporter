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
//
// systemd splits properties across DBus interfaces:
//   - org.freedesktop.systemd1.Unit    → ActiveState, LoadState, …
//   - org.freedesktop.systemd1.Service → MainPID, NRestarts, …
// GetUnitPropertiesContext returns only the Unit-interface ones; we need a
// second call (GetUnitTypePropertiesContext, type="Service") for service-
// specific fields. Confirmed on LSN-D13958: busctl ... .Unit MainPID returns
// "Unknown interface or property" while .Service MainPID works.
type unitConn interface {
	GetUnitPropertiesContext(ctx context.Context, unit string) (map[string]interface{}, error)
	GetUnitTypePropertiesContext(ctx context.Context, unit string, unitType string) (map[string]interface{}, error)
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

func (p *DBusProbe) unitProps() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	return p.conn.GetUnitPropertiesContext(ctx, p.unit)
}

func (p *DBusProbe) serviceProps() (map[string]interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	return p.conn.GetUnitTypePropertiesContext(ctx, p.unit, "Service")
}

// IsActive — FR-001. true iff ActiveState == "active".
// ActiveState lives on the Unit interface.
func (p *DBusProbe) IsActive() (bool, error) {
	m, err := p.unitProps()
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
// MainPID is a Service-interface property (not Unit), so we query the Service
// type explicitly. Without this we'd silently return 0 → child_count metric
// stuck at 0 — observed in production on LSN-D13958 2026-05-23.
func (p *DBusProbe) MainPID() (int, error) {
	m, err := p.serviceProps()
	if err != nil {
		return 0, err
	}
	return uintToInt(m["MainPID"])
}

// NRestarts — FR-004. Also a Service-interface property.
// systemd reports a uint32 here; we expose it as Counter at the metrics layer.
func (p *DBusProbe) NRestarts() (int, error) {
	m, err := p.serviceProps()
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
