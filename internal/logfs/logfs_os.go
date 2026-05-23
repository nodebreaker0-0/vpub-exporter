package logfs

import (
	"os"
	"time"
)

// OSDirStat scans a directory and returns the regular file with the largest
// mtime. Read-only — only Stat / ReadDir calls.
// R-002: filename pattern (YYYYMMDD) is not enforced. Picking the newest
// mtime among regular files handles any rotation scheme — including the
// 00:00 UTC rollover race noted in spec.md Edge Cases.
type OSDirStat struct{}

func New() *OSDirStat { return &OSDirStat{} }

// LatestMtime returns the newest file's mtime and basename.
// If dir does not exist: returns (zero, "", error) — alert rule must
// surface this as "log dir missing" via vpub_exporter_collection_errors_total.
// If dir exists but is empty: returns (zero, "", nil).
func (OSDirStat) LatestMtime(dir string) (time.Time, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, "", err
	}
	var (
		bestT time.Time
		bestN string
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// Symlink with no target / file disappeared mid-scan — skip.
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if info.ModTime().After(bestT) {
			bestT = info.ModTime()
			bestN = e.Name()
		}
	}
	if bestN == "" {
		return time.Time{}, "", nil
	}
	return bestT, bestN, nil
}

// Compile-time assertion.
var _ LogDirStat = (*OSDirStat)(nil)
