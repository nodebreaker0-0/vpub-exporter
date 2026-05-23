package logtail

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"
)

// fakeLatest returns the path stored in *current at call time. Lets tests
// rotate by atomically swapping the pointer.
type fakeLatest struct {
	v atomic.Value // string
}

func (f *fakeLatest) set(p string) { f.v.Store(p) }
func (f *fakeLatest) fn(_ string) (string, error) {
	if v := f.v.Load(); v != nil {
		return v.(string), nil
	}
	return "", nil
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

func collect(ch <-chan Match, until time.Time) []string {
	var out []string
	timer := time.NewTimer(time.Until(until))
	defer timer.Stop()
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, m.Line)
		case <-timer.C:
			return out
		}
	}
}

func TestPolling_EmitsMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260523")
	if err := os.WriteFile(path, []byte("preamble line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fl := &fakeLatest{}
	fl.set(path)

	tailer := &PollingTailer{LatestFileFn: fl.fn, PollInterval: 20 * time.Millisecond}
	patterns := []*regexp.Regexp{regexp.MustCompile(`(?i)vote.*ok`)}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ch, err := tailer.Subscribe(ctx, dir, patterns)
	if err != nil {
		t.Fatal(err)
	}

	// Give the tailer a tick to attach.
	time.Sleep(60 * time.Millisecond)
	appendLine(t, path, "12:00 vote ok\n")
	appendLine(t, path, "12:01 ignore me\n")
	appendLine(t, path, "12:02 VOTE OK\n")

	lines := collect(ch, time.Now().Add(300*time.Millisecond))
	if len(lines) < 2 {
		t.Fatalf("got %d matches, want ≥2 (lines = %v)", len(lines), lines)
	}
}

func TestPolling_DetectsRotation(t *testing.T) {
	dir := t.TempDir()
	day1 := filepath.Join(dir, "20260523")
	day2 := filepath.Join(dir, "20260524")
	if err := os.WriteFile(day1, []byte("first day\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(day2, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	fl := &fakeLatest{}
	fl.set(day1)

	tailer := &PollingTailer{LatestFileFn: fl.fn, PollInterval: 20 * time.Millisecond}
	patterns := []*regexp.Regexp{regexp.MustCompile(`day-2`)}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	ch, err := tailer.Subscribe(ctx, dir, patterns)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Millisecond)
	appendLine(t, day1, "still no match\n")

	// Rotate: day2 becomes current.
	fl.set(day2)
	time.Sleep(60 * time.Millisecond)
	appendLine(t, day2, "found day-2 marker\n")

	lines := collect(ch, time.Now().Add(400*time.Millisecond))
	found := false
	for _, l := range lines {
		if l == "found day-2 marker" {
			found = true
		}
	}
	if !found {
		t.Errorf("did not see rotation match; lines = %v", lines)
	}
}

func TestPolling_PartialTrailingLineHeldOver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260523")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	fl := &fakeLatest{}
	fl.set(path)

	tailer := &PollingTailer{LatestFileFn: fl.fn, PollInterval: 20 * time.Millisecond}
	patterns := []*regexp.Regexp{regexp.MustCompile(`finished`)}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ch, err := tailer.Subscribe(ctx, dir, patterns)
	if err != nil {
		t.Fatal(err)
	}

	// Write partial line (no '\n') then complete it later.
	time.Sleep(60 * time.Millisecond)
	appendLine(t, path, "partial finished")
	time.Sleep(60 * time.Millisecond)
	appendLine(t, path, " write\n")

	lines := collect(ch, time.Now().Add(300*time.Millisecond))
	if len(lines) != 1 {
		t.Fatalf("got %d matches, want exactly 1; lines = %v", len(lines), lines)
	}
}

func TestCompilePatterns_SkipsInvalid(t *testing.T) {
	regs, errs := CompilePatterns([]string{`(?i)ok`, `(unbalanced`, `vote.*ok`})
	if len(regs) != 2 {
		t.Errorf("got %d compiled, want 2", len(regs))
	}
	if len(errs) != 1 {
		t.Errorf("got %d errors, want 1", len(errs))
	}
}

func TestPolling_CtxCancelClosesChannel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260523"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	fl := &fakeLatest{}
	fl.set(filepath.Join(dir, "20260523"))
	tailer := &PollingTailer{LatestFileFn: fl.fn, PollInterval: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := tailer.Subscribe(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — good
			}
		case <-deadline:
			t.Fatal("channel not closed within 500ms")
		}
	}
}
