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

func TestPolling_SkipsHTMLContinuationLines(t *testing.T) {
	// Regression (testnet 5/22): 502 Bad Gateway HTML body dropped multi-line
	// into the log file. The word "Bad" appeared 52 times in 9.7h, all of
	// them inside the HTML continuation. With LinePrefix = timestamp regex,
	// none of those should match a Bad-Gateway-shaped pattern.
	dir := t.TempDir()
	path := filepath.Join(dir, "20260522")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fl := &fakeLatest{}
	fl.set(path)

	tailer := &PollingTailer{
		LatestFileFn: fl.fn,
		PollInterval: 20 * time.Millisecond,
		LinePrefix:   PublisherTimestampPrefix,
	}
	// Pattern is intentionally generic (would match anything containing "Bad").
	patterns := []*regexp.Regexp{regexp.MustCompile(`Bad`)}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ch, err := tailer.Subscribe(ctx, dir, patterns)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)

	// Real publisher log line (has timestamp prefix, contains "Bad")
	appendLine(t, path, "2026-05-22T10:00:00Z INFO normal Bad token oops\n")
	// HTML 502 response dumped as continuation (no timestamp prefix)
	appendLine(t, path, "<html>\n")
	appendLine(t, path, "<head><title>502 Bad Gateway</title></head>\n")
	appendLine(t, path, "<body>nginx Bad Gateway</body>\n")
	appendLine(t, path, "</html>\n")

	lines := collect(ch, time.Now().Add(300*time.Millisecond))
	if len(lines) != 1 {
		t.Fatalf("emit count = %d, want exactly 1 (only the timestamped line); lines = %v", len(lines), lines)
	}
	if lines[0] != "2026-05-22T10:00:00Z INFO normal Bad token oops" {
		t.Errorf("unexpected line: %q", lines[0])
	}
}

func TestPolling_LinePrefixNilStillEmits(t *testing.T) {
	// Backward-compat: leaving LinePrefix nil disables the guard.
	dir := t.TempDir()
	path := filepath.Join(dir, "20260522")
	_ = os.WriteFile(path, nil, 0o644)
	fl := &fakeLatest{}
	fl.set(path)
	tailer := &PollingTailer{LatestFileFn: fl.fn, PollInterval: 20 * time.Millisecond}
	patterns := []*regexp.Regexp{regexp.MustCompile(`anything`)}

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	ch, _ := tailer.Subscribe(ctx, dir, patterns)
	time.Sleep(60 * time.Millisecond)
	appendLine(t, path, "no-timestamp-here anything matches\n")
	lines := collect(ch, time.Now().Add(300*time.Millisecond))
	if len(lines) != 1 {
		t.Errorf("nil LinePrefix should emit; got %d lines", len(lines))
	}
}

func TestParseLineTimestamp(t *testing.T) {
	// R-025: Match.At must come from the line itself, not the drain wall clock.
	// Format: 2026-06-06T11:50:04.670582 INFO ...
	cases := []struct {
		name string
		line string
		want time.Time
		ok   bool
	}{
		{
			"microseconds",
			"2026-06-06T11:50:04.670582 INFO  validator_publisher::visor: downloading ...",
			time.Date(2026, 6, 6, 11, 50, 4, 670582000, time.UTC),
			true,
		},
		{
			"no fractional",
			"2026-05-26T05:51:25 INFO  visor: downloading ...",
			time.Date(2026, 5, 26, 5, 51, 25, 0, time.UTC),
			true,
		},
		{
			"empty",
			"",
			time.Time{},
			false,
		},
		{
			"no timestamp prefix",
			"INFO this is a continuation line with no timestamp",
			time.Time{},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseLineTimestamp(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPolling_AtFromLineTimestamp(t *testing.T) {
	// R-025: drain should set Match.At to the parsed line timestamp, not
	// time.Now(). Test by appending a line whose timestamp is far in the past
	// — Match.At must equal that past time, not the read wall clock.
	dir := t.TempDir()
	path := filepath.Join(dir, "20260606")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fl := &fakeLatest{}
	fl.set(path)
	tailer := &PollingTailer{
		LatestFileFn: fl.fn,
		PollInterval: 20 * time.Millisecond,
		LinePrefix:   PublisherTimestampPrefix,
	}
	patterns := []*regexp.Regexp{regexp.MustCompile(`downloading new binary`)}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ch, err := tailer.Subscribe(ctx, dir, patterns)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)

	// Old publisher build format.
	line := "2026-06-06T11:50:04.670582 INFO  visor: downloading new binary self.binary_name=\"outcome-voter\" height=24\n"
	appendLine(t, path, line)

	deadline := time.Now().Add(300 * time.Millisecond)
	var got Match
	gotSome := false
	for time.Now().Before(deadline) {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before match")
			}
			got = m
			gotSome = true
		case <-time.After(50 * time.Millisecond):
		}
		if gotSome {
			break
		}
	}
	if !gotSome {
		t.Fatal("no match received")
	}
	want := time.Date(2026, 6, 6, 11, 50, 4, 670582000, time.UTC)
	if !got.At.Equal(want) {
		t.Errorf("Match.At = %v, want %v (line timestamp, not wall clock)", got.At, want)
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
