package binary

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"
)

// HTTPProbe implements Probe.
// Read-only: only os.Stat (local) and http.HEAD (remote). The exporter never
// downloads, never overwrites, never installs — that decision is human-only
// (Constitution II).
type HTTPProbe struct {
	Client *http.Client
	// Timeout is the per-call HEAD timeout. Defaults to 10s (plan.md).
	Timeout time.Duration
}

// NewHTTPProbe returns a probe with a 10s HEAD timeout and a transport that
// keeps a small idle pool (the URL is hit once per minute — pooling not critical).
func NewHTTPProbe() *HTTPProbe {
	return &HTTPProbe{
		Client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        4,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		Timeout: 10 * time.Second,
	}
}

// LocalMtime returns the on-disk file mtime. Read-only.
func (p *HTTPProbe) LocalMtime(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}

// RemoteLastModified issues an HTTP HEAD and parses Last-Modified.
// Returns an error when the server omits the header (cannot tell upgrade
// availability) or when status != 200. NEVER follows GET — we don't want
// to accidentally download a 100MB binary every minute.
func (p *HTTPProbe) RemoteLastModified(ctx context.Context, url string) (time.Time, error) {
	if p.Client == nil {
		*p = *NewHTTPProbe()
	}
	cctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodHead, url, nil)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("HEAD %s: status %d", url, resp.StatusCode)
	}
	lm := resp.Header.Get("Last-Modified")
	if lm == "" {
		return time.Time{}, fmt.Errorf("HEAD %s: Last-Modified header missing", url)
	}
	// http.ParseTime handles RFC1123 / RFC850 / ANSI C asctime.
	t, err := http.ParseTime(lm)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Last-Modified %q: %w", lm, err)
	}
	return t, nil
}

// Compile-time assertion.
var _ Probe = (*HTTPProbe)(nil)
