package binary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newProbe() *HTTPProbe {
	p := NewHTTPProbe()
	p.Timeout = 500 * time.Millisecond
	p.Client.Timeout = 500 * time.Millisecond
	return p
}

func TestLocalMtime_ReadsRealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "visor")
	if err := os.WriteFile(path, []byte("dummy"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}
	got, err := newProbe().LocalMtime(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("LocalMtime = %v, want %v", got, want)
	}
}

func TestLocalMtime_MissingFile(t *testing.T) {
	_, err := newProbe().LocalMtime("/nope/never")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRemoteLastModified_Success(t *testing.T) {
	var (
		methods atomic.Int32
		want    = time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method = %q, want HEAD (never GET)", r.Method)
		}
		methods.Add(1)
		w.Header().Set("Last-Modified", want.Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got, err := newProbe().RemoteLastModified(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if methods.Load() != 1 {
		t.Errorf("HEAD calls = %d, want 1", methods.Load())
	}
}

func TestRemoteLastModified_MissingHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := newProbe().RemoteLastModified(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error when Last-Modified missing")
	}
}

func TestRemoteLastModified_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := newProbe().RemoteLastModified(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestRemoteLastModified_MalformedHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Last-Modified", "not-a-date")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, err := newProbe().RemoteLastModified(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestRemoteLastModified_Timeout(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-hold
	}))
	defer srv.Close()
	defer close(hold)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := newProbe().RemoteLastModified(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRemoteLastModified_RefusedConnection(t *testing.T) {
	_, err := newProbe().RemoteLastModified(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected connect error")
	}
}
