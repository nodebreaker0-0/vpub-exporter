package logtail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PollingTailer follows the newest file in a directory.
//
// Design (research.md R-011):
//   - Open with O_RDONLY only. NEVER write / chmod / chown / chtimes the log.
//   - On each PollInterval tick:
//       (1) Ask LatestFileFn(dir) which file is current.
//       (2) If different from the open one — re-open, seek to 0 (read new file from start).
//       (3) Read all new lines from current offset; match against patterns.
//   - Rotation detection covers daily YYYYMMDD rollover + chmod-induced reopen.
//   - Truncation (file shrank) is treated as rotation in place: seek back to 0.
//
// Constitution II direct binding: this tailer never writes, never moves the
// offset of the underlying file via lseek-then-write — only reads. It is OK
// for two PollingTailer goroutines to point at the same path (independent
// offsets, no shared state with the file).
type PollingTailer struct {
	// LatestFileFn returns the absolute path of the file to tail right now.
	// Typically backed by logfs.OSDirStat — but kept as a func so tests can
	// inject a deterministic sequence (rotate at iteration N).
	LatestFileFn func(dir string) (string, error)
	// PollInterval is how often new lines and rotation are checked.
	PollInterval time.Duration

	// LinePrefix, when non-nil, filters out lines that do NOT match it.
	// testnet 5/22 실관찰: HTTP 502 Bad Gateway 응답이 멀티-라인 HTML 로
	// 로그에 그대로 떨어지는 케이스가 있어, 단순 단어 매칭이 false-positive 를
	//유발. publisher 의 모든 valid 라인은 "<RFC3339 timestamp>" 로 시작하므로,
	// 그 prefix 가 없는 라인은 직전 라인의 연장으로 보고 매칭에서 제외.
	// nil = filter off (legacy).
	LinePrefix *regexp.Regexp
}

// PublisherTimestampPrefix matches the leading "YYYY-MM-DDT" of every valid
// publisher log line. Set this as PollingTailer.LinePrefix in production.
var PublisherTimestampPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)

// NewPolling returns a tailer with sane defaults.
func NewPolling(latest func(dir string) (string, error)) *PollingTailer {
	return &PollingTailer{
		LatestFileFn: latest,
		PollInterval: 1 * time.Second,
	}
}

// Subscribe starts a goroutine that emits Match for every line matching any pattern.
// The returned channel is closed when ctx is canceled or a non-retryable error fires.
// dir is treated as a directory; the tailer picks the latest file via LatestFileFn(dir).
func (t *PollingTailer) Subscribe(ctx context.Context, dir string, patterns []*regexp.Regexp) (<-chan Match, error) {
	if t.LatestFileFn == nil {
		return nil, errors.New("logtail: LatestFileFn is nil")
	}
	if t.PollInterval <= 0 {
		t.PollInterval = 1 * time.Second
	}
	out := make(chan Match, 64)
	go t.loop(ctx, dir, patterns, out)
	return out, nil
}

func (t *PollingTailer) loop(ctx context.Context, dir string, patterns []*regexp.Regexp, out chan<- Match) {
	defer close(out)

	var (
		f          *os.File
		curPath    string
		curInode   inodeKey
		off        int64
	)
	closeF := func() {
		if f != nil {
			_ = f.Close()
			f = nil
		}
	}
	defer closeF()

	tick := time.NewTicker(t.PollInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 1) latest file?
		path, err := t.LatestFileFn(dir)
		if err == nil && path != "" {
			needReopen := false
			if f == nil || path != curPath {
				needReopen = true
			} else {
				// same path — check inode for in-place rotation
				if fi, err := os.Stat(path); err == nil {
					ik := inodeOf(fi)
					if ik != curInode {
						needReopen = true
					} else if fi.Size() < off {
						// truncation: seek to 0
						_, _ = f.Seek(0, io.SeekStart)
						off = 0
					}
				}
			}
			if needReopen {
				closeF()
				nf, openErr := os.OpenFile(path, os.O_RDONLY, 0)
				if openErr == nil {
					f = nf
					curPath = path
					off = 0
					if fi, err := nf.Stat(); err == nil {
						curInode = inodeOf(fi)
					}
				}
			}
		}

		// 2) drain new lines
		if f != nil {
			n := t.drain(ctx, f, &off, curPath, patterns, out)
			_ = n
		}

		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// drain reads from f at *off and emits matches. Updates *off to the byte position
// just past the last newline that was emitted. Partial trailing line is left for next tick.
//
// R-025 (2026-06-06): Match.At 는 라인 자체의 timestamp 를 파싱한 값을 우선
// 사용. parse 실패 또는 timestamp prefix 없는 라인이면 drain wall clock
// fallback. publisher restart / vpub-exporter restart 후 옛 라인을 처음부터
// re-read 할 때 metric 의 timestamp 가 진짜 라인 시각을 반영하도록 함
// (이전엔 reread 시점 wall clock 으로 잘못 set 되어 download_failed 알람이
// false positive 로 발화).
func (t *PollingTailer) drain(ctx context.Context, f *os.File, off *int64, path string, patterns []*regexp.Regexp, out chan<- Match) int {
	if _, err := f.Seek(*off, io.SeekStart); err != nil {
		return 0
	}
	br := bufio.NewReader(f)
	emitted := 0
	fallback := time.Now()
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 && err == nil {
			// full line including '\n'
			*off += int64(len(line))
			stripped := line[:len(line)-1]
			// Multi-line HTML / stack-trace continuation guard (R-001/E):
			// if a LinePrefix is configured and this line lacks it, skip
			// pattern matching entirely. The line was already consumed from
			// the offset, so the partial-line invariant is preserved.
			if t.LinePrefix != nil && !t.LinePrefix.MatchString(stripped) {
				continue
			}
			at := fallback
			if ts, ok := ParseLineTimestamp(stripped); ok {
				at = ts
			}
			for _, p := range patterns {
				if p.MatchString(stripped) {
					select {
					case out <- Match{File: path, Line: stripped, Pattern: p, At: at}:
						emitted++
					case <-ctx.Done():
						return emitted
					}
				}
			}
			continue
		}
		if err == io.EOF {
			// keep any partial trailing line for next tick (don't advance off past it)
			return emitted
		}
		if err != nil {
			return emitted
		}
	}
}

// ParseLineTimestamp extracts the leading "YYYY-MM-DDTHH:MM:SS(.frac)?" prefix
// from a publisher log line. publisher always emits UTC timestamps without an
// explicit timezone suffix (e.g. `2026-06-06T11:50:04.670582`). Returns
// (zero, false) if no valid prefix is found.
//
// Used by drain() to set Match.At to the actual line timestamp instead of the
// wall clock at read time — required for the metrics that downstream alerts
// compare against file mtimes (R-025).
func ParseLineTimestamp(line string) (time.Time, bool) {
	if len(line) < 19 {
		return time.Time{}, false
	}
	sp := strings.IndexByte(line, ' ')
	if sp <= 0 || sp > 32 {
		return time.Time{}, false
	}
	head := line[:sp]
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999", head, time.UTC); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", head, time.UTC); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// CompilePatterns converts a list of raw regex strings to compiled regexes,
// dropping any that fail to compile (caller can inspect the returned []error).
func CompilePatterns(raws []string) ([]*regexp.Regexp, []error) {
	var compiled []*regexp.Regexp
	var errs []error
	for _, r := range raws {
		re, err := regexp.Compile(r)
		if err != nil {
			errs = append(errs, fmt.Errorf("compile %q: %w", r, err))
			continue
		}
		compiled = append(compiled, re)
	}
	return compiled, errs
}

// inodeKey distinguishes the same path before/after rotation.
type inodeKey struct {
	dev uint64
	ino uint64
}

// inodeOf is platform-portable. inode_unix.go overrides this at init() on
// linux/darwin to read syscall.Stat_t (dev,ino). The fallback uses Size+ModTime
// as a proxy — sufficient because rename-based rotation is also detected via
// path inequality.
var inodeOf = inodeOfStub

func inodeOfStub(fi os.FileInfo) inodeKey {
	return inodeKey{dev: uint64(fi.Size()), ino: uint64(fi.ModTime().UnixNano())}
}

// Compile-time assertion: PollingTailer implements Tailer.
var _ Tailer = (*PollingTailer)(nil)

// SyncMutex placeholder — kept import alive.
type SyncMutex = sync.RWMutex
