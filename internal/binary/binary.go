// Package binary tracks the publisher binary's local vs remote mtime.
// Read-only (HEAD only). Phase 5 (T060).
package binary

import (
	"context"
	"time"
)

// Probe inspects the local file and the announce URL.
// HTTP HEAD only — never GET (we do not download or replace the binary).
type Probe interface {
	LocalMtime(path string) (time.Time, error)
	RemoteLastModified(ctx context.Context, url string) (time.Time, error)
}
