//go:build linux
// +build linux

package procs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcFSLister walks /proc and counts processes whose PPID matches the parent.
// Read-only — only ever calls Stat/ReadDir/ReadFile.
type ProcFSLister struct {
	// Root is the path to mount-point of procfs. Tests can point this at a
	// temporary directory holding a synthetic /proc layout.
	Root string
}

// New returns a lister rooted at /proc.
func New() *ProcFSLister { return &ProcFSLister{Root: "/proc"} }

// CountChildren counts entries under Root that are PID dirs with PPID == parentPID.
// Returns (0, nil) if parentPID is 0 (unit not running).
func (l *ProcFSLister) CountChildren(parentPID int) (int, error) {
	if parentPID <= 0 {
		return 0, nil
	}
	root := l.Root
	if root == "" {
		root = "/proc"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		ppid, err := readPPID(filepath.Join(root, e.Name(), "stat"))
		if err != nil {
			// Process disappeared between ReadDir and ReadFile — skip.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		if ppid == parentPID {
			n++
		}
	}
	return n, nil
}

// readPPID parses field 4 of /proc/<pid>/stat.
// Format: pid (comm) state ppid ...
// comm may contain spaces and parens, so we split on the LAST ')'.
func readPPID(statPath string) (int, error) {
	b, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}
	s := string(b)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, errors.New("malformed /proc/*/stat")
	}
	rest := strings.TrimSpace(s[idx+1:])
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return 0, errors.New("not enough fields in stat after comm")
	}
	// fields[0] = state, fields[1] = ppid
	return strconv.Atoi(fields[1])
}

// Compile-time assertion.
var _ ChildLister = (*ProcFSLister)(nil)
