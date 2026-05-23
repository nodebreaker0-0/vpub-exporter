package rpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newProbe() *HTTPProbe { p := NewHTTPProbe(); p.Timeout = 500 * time.Millisecond; p.Client.Timeout = 500 * time.Millisecond; return p }

func TestProbe_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "eth_blockNumber") {
			t.Errorf("body lacks eth_blockNumber: %s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1234abcd"}`))
	}))
	defer srv.Close()

	lat, err := newProbe().Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if lat <= 0 || lat > time.Second {
		t.Errorf("suspicious latency: %v", lat)
	}
}

func TestProbe_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))
	defer srv.Close()

	_, err := newProbe().Probe(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestProbe_JSONRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"server busy"}}`))
	}))
	defer srv.Close()

	_, err := newProbe().Probe(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on rpc error response")
	}
	if !strings.Contains(err.Error(), "-32000") {
		t.Errorf("error should mention code: %v", err)
	}
}

func TestProbe_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":""}`))
	}))
	defer srv.Close()

	_, err := newProbe().Probe(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on empty result")
	}
}

func TestProbe_Timeout(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-done
	}))
	defer srv.Close()
	defer close(done)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := newProbe().Probe(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestProbe_BadURL(t *testing.T) {
	_, err := newProbe().Probe(context.Background(), "http://127.0.0.1:1") // refused
	if err == nil {
		t.Fatal("expected connect error")
	}
}
