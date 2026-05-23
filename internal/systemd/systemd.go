// Package systemd defines the read-only systemd unit probe interface used
// by the service collector. Concrete dbus impl lives in systemd_dbus.go.
package systemd

// ServiceProbe queries validator-publisher.service status without modifying it.
// Implementations MUST NOT call start/stop/restart on the unit
// (Constitution II — No Side Effects on Publisher).
type ServiceProbe interface {
	// IsActive reports whether ActiveState == "active".
	IsActive() (bool, error)
	// MainPID returns systemd-reported MainPID, or 0 if not running.
	MainPID() (int, error)
	// NRestarts returns the cumulative systemd-counted restart count.
	NRestarts() (int, error)
}
