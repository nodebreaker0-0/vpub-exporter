// Package logfs stats publisher log directories without opening file contents.
// Used by the logmtime collector (FR-003).
package logfs

import "time"

// LogDirStat finds the newest file in componentDir and returns its mtime + filename.
// On empty directory, returns the zero time without error.
type LogDirStat interface {
	LatestMtime(componentDir string) (mtime time.Time, filename string, err error)
}
