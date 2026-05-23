//go:build !linux
// +build !linux

package systemd

import (
	"context"
	"errors"
)

// On non-Linux dev hosts (macOS, etc.) the dbus binding is not available.
// We still provide a DBusProbe symbol so the rest of the code compiles,
// but every call fails. Production binaries are built for linux/amd64
// (Makefile target build-linux) — this stub exists only to keep the
// developer feedback loop fast.

type DBusProbe struct{ unit string }

func NewDBusProbe(_ context.Context, unit string) (*DBusProbe, error) {
	return &DBusProbe{unit: unit}, errors.New("systemd dbus not supported on this platform")
}

func (p *DBusProbe) Close()                       {}
func (p *DBusProbe) IsActive() (bool, error)      { return false, errNotLinux }
func (p *DBusProbe) MainPID() (int, error)        { return 0, errNotLinux }
func (p *DBusProbe) NRestarts() (int, error)      { return 0, errNotLinux }

var errNotLinux = errors.New("systemd dbus probe only works on linux")
