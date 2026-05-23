// Package logtail tails publisher log files line-by-line and emits matches
// of supplied regexes. Read-only (no fsync, no write). Phase 4 (T040).
package logtail

import (
	"context"
	"regexp"
	"time"
)

// Match is one regex hit on a log line.
type Match struct {
	File    string
	Line    string
	Pattern *regexp.Regexp
	At      time.Time
}

// Tailer follows a file (with rotation re-open) and emits matches.
// Implementations MUST open files O_RDONLY only.
type Tailer interface {
	// Subscribe starts a goroutine tailing file. The returned channel
	// is closed when ctx is canceled or a fatal error occurs.
	Subscribe(ctx context.Context, file string, patterns []*regexp.Regexp) (<-chan Match, error)
}
