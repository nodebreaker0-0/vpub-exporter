package slackapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(srv *httptest.Server) *HTTPClient {
	return &HTTPClient{
		BaseURL: srv.URL,
		HTTP:    &http.Client{Timeout: 500 * time.Millisecond},
	}
}

func TestAuthTest_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer token: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":"b-harvest","user":"vpub-bot"}`))
	}))
	defer srv.Close()

	ok, err := newTestClient(srv).AuthTest(context.Background(), "tok")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestAuthTest_InvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()

	ok, err := newTestClient(srv).AuthTest(context.Background(), "tok")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("ok should be false on invalid_auth")
	}
}

func TestAuthTest_EmptyToken(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.AuthTest(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestHistory24h_CountsMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("channel") != "C12345" {
			t.Errorf("channel = %q", r.Form.Get("channel"))
		}
		if r.Form.Get("oldest") == "" {
			t.Errorf("oldest empty")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1"},{"ts":"2"},{"ts":"3"}],"has_more":false}`))
	}))
	defer srv.Close()

	n, err := newTestClient(srv).History24h(context.Background(), "tok", "C12345")
	if err != nil || n != 3 {
		t.Fatalf("got n=%d err=%v", n, err)
	}
}

func TestHistory24h_HasMoreReturnsPartialWithError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1"},{"ts":"2"}],"has_more":true}`))
	}))
	defer srv.Close()

	n, err := newTestClient(srv).History24h(context.Background(), "tok", "C12345")
	if err == nil {
		t.Fatal("expected has_more sentinel error")
	}
	if n != 2 {
		t.Errorf("n = %d, want 2 (partial count)", n)
	}
}

func TestHistory24h_RateLimit429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := newTestClient(srv).History24h(context.Background(), "tok", "C12345")
	if err == nil {
		t.Fatal("expected error on 429")
	}
}

func TestHistory24h_EmptyChannelID(t *testing.T) {
	c := NewHTTPClient()
	if _, err := c.History24h(context.Background(), "tok", ""); err == nil {
		t.Fatal("expected error on empty channel id")
	}
}

func TestHistory24h_OkFalseFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).History24h(context.Background(), "tok", "Cxxx")
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("err = %v, want channel_not_found", err)
	}
}
