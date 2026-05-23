package logfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAt(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestLatestMtime_PicksNewestAmongRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	writeAt(t, filepath.Join(dir, "20260520"), now.Add(-3*24*time.Hour))
	writeAt(t, filepath.Join(dir, "20260521"), now.Add(-2*24*time.Hour))
	writeAt(t, filepath.Join(dir, "20260523"), now.Add(-30*time.Minute))
	writeAt(t, filepath.Join(dir, "20260522"), now.Add(-24*time.Hour))

	mt, name, err := OSDirStat{}.LatestMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "20260523" {
		t.Errorf("name = %q, want 20260523", name)
	}
	if !mt.Equal(now.Add(-30 * time.Minute)) {
		t.Errorf("mtime = %v, want %v", mt, now.Add(-30*time.Minute))
	}
}

func TestLatestMtime_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	mt, name, err := OSDirStat{}.LatestMtime(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !mt.IsZero() || name != "" {
		t.Errorf("mt=%v name=%q, want zero / empty", mt, name)
	}
}

func TestLatestMtime_MissingDirReturnsError(t *testing.T) {
	_, _, err := OSDirStat{}.LatestMtime("/this/does/not/exist/at/all")
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestLatestMtime_IgnoresSubdirsAndIrregularFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)

	// regular file
	writeAt(t, filepath.Join(dir, "today.log"), now.Add(-5*time.Minute))
	// subdir with newer mtime — must be ignored
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sub, now, now); err != nil {
		t.Fatal(err)
	}

	mt, name, err := OSDirStat{}.LatestMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "today.log" {
		t.Errorf("name = %q, want today.log (subdir ignored)", name)
	}
	if !mt.Equal(now.Add(-5 * time.Minute)) {
		t.Errorf("mtime = %v, want %v", mt, now.Add(-5*time.Minute))
	}
}

func TestLatestMtime_DuplicateMtimeStable(t *testing.T) {
	// Two files with identical mtime — implementation is allowed to pick either,
	// but must be deterministic for a given filesystem ordering. We just verify
	// it returns ONE of them, not both / neither.
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	writeAt(t, filepath.Join(dir, "a"), now)
	writeAt(t, filepath.Join(dir, "b"), now)

	_, name, err := OSDirStat{}.LatestMtime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "a" && name != "b" {
		t.Errorf("name = %q, want a or b", name)
	}
}
