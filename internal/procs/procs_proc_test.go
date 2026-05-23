//go:build linux
// +build linux

package procs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeProc creates a fake /proc/<pid>/stat file for testing.
func writeProc(t *testing.T, root string, pid int, comm string, ppid int) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// pid (comm) state ppid pgrp ...
	content := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 0 0 0 0 0 0\n", pid, comm, ppid)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountChildren_ThreeChildren(t *testing.T) {
	root := t.TempDir()
	const parent = 1000
	writeProc(t, root, parent, "visor", 1)
	writeProc(t, root, 1001, "bridge-voter", parent)
	writeProc(t, root, 1002, "reference-oracle-publisher", parent)
	writeProc(t, root, 1003, "outcome-voter", parent)
	writeProc(t, root, 2000, "unrelated", 1)

	l := &ProcFSLister{Root: root}
	got, err := l.CountChildren(parent)
	if err != nil {
		t.Fatalf("CountChildren: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestCountChildren_NoneAlive(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 1000, "visor", 1)
	// no children
	l := &ProcFSLister{Root: root}
	got, err := l.CountChildren(1000)
	if err != nil || got != 0 {
		t.Errorf("got %d, %v", got, err)
	}
}

func TestCountChildren_PartialFailure(t *testing.T) {
	// 4 entries — 2 children, 1 with malformed stat, 1 unrelated.
	root := t.TempDir()
	writeProc(t, root, 1000, "visor", 1)
	writeProc(t, root, 1001, "bridge-voter", 1000)
	writeProc(t, root, 1002, "oracle", 1000)
	// malformed stat — should be ignored, not crash
	dir := filepath.Join(root, "1003")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "stat"), []byte("broken"), 0o644)
	writeProc(t, root, 2000, "unrelated", 1)

	l := &ProcFSLister{Root: root}
	got, err := l.CountChildren(1000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 2 {
		t.Errorf("got %d, want 2 (malformed should be silently skipped)", got)
	}
}

func TestCountChildren_ZeroParent(t *testing.T) {
	// parent=0 means unit not running — should return 0 without error and without I/O.
	l := &ProcFSLister{Root: "/does-not-exist"}
	got, err := l.CountChildren(0)
	if err != nil || got != 0 {
		t.Errorf("got %d, %v", got, err)
	}
}

func TestReadPPID_CommWithParens(t *testing.T) {
	root := t.TempDir()
	// comm "foo )( bar" contains both parens and spaces
	dir := filepath.Join(root, "42")
	_ = os.MkdirAll(dir, 0o755)
	content := "42 (foo )( bar) S 7 1 1 0 -1 0\n"
	_ = os.WriteFile(filepath.Join(dir, "stat"), []byte(content), 0o644)

	ppid, err := readPPID(filepath.Join(dir, "stat"))
	if err != nil {
		t.Fatal(err)
	}
	if ppid != 7 {
		t.Errorf("ppid = %d, want 7", ppid)
	}
}
