//go:build !linux
// +build !linux

package procs

import "errors"

// ProcFSLister stub for non-Linux dev hosts. Production binary is linux/amd64
// (Makefile target build-linux).
type ProcFSLister struct{ Root string }

func New() *ProcFSLister                                 { return &ProcFSLister{Root: "/proc"} }
func (l *ProcFSLister) CountChildren(_ int) (int, error) { return 0, errNotLinux }

var errNotLinux = errors.New("/proc-based ChildLister only works on linux")
