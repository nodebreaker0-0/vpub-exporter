package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrAuth marks an RPC response as a credentials problem (HTTP 401 or
// "Must be authenticated" body). The bridge_rpc collector uses errors.Is
// to bump the auth_error status counter (FR-005 보강 / R-004).
var ErrAuth = errors.New("rpc auth error")

// HTTPProbe issues a single eth_blockNumber JSON-RPC POST.
// Read-only: never sends a signed transaction / eth_sendRawTransaction /
// eth_sendTransaction. Only eth_blockNumber (FR-005, plan.md timeout 5s).
type HTTPProbe struct {
	Client  *http.Client
	Timeout time.Duration
}

// NewHTTPProbe constructs a probe with a 5s default timeout.
// The client gets a hard timeout; idle connections are limited so a slow
// RPC cannot starve scrapes.
func NewHTTPProbe() *HTTPProbe {
	t := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}
	return &HTTPProbe{
		Client:  &http.Client{Transport: t, Timeout: 5 * time.Second},
		Timeout: 5 * time.Second,
	}
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  string          `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Probe sends eth_blockNumber and returns wall-clock latency.
// Returns error on: network timeout, non-200 HTTP, non-empty JSON-RPC error,
// or malformed result. Never returns a hex parsing error (we don't care about
// the value — just whether the endpoint answers).
func (p *HTTPProbe) Probe(ctx context.Context, url string) (time.Duration, error) {
	if p.Client == nil {
		*p = *NewHTTPProbe()
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := p.Client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return latency, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		// 401 — most common case for expired alchemy / quicknode keys.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return latency, fmt.Errorf("%w: http 401 (%s)", ErrAuth, snippet(body))
	}
	if resp.StatusCode != http.StatusOK {
		// Some providers return 200 + a JSON body saying "Must be authenticated!"
		// instead of 401. Sniff a small prefix.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if looksLikeAuthBody(body) {
			return latency, fmt.Errorf("%w: http %d (%s)", ErrAuth, resp.StatusCode, snippet(body))
		}
		return latency, fmt.Errorf("rpc http %d", resp.StatusCode)
	}

	// 200 path — body may still contain "Must be authenticated!".
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return latency, fmt.Errorf("read rpc body: %w", err)
	}
	if looksLikeAuthBody(bodyBytes) {
		return latency, fmt.Errorf("%w: 200 body suggests auth required (%s)", ErrAuth, snippet(bodyBytes))
	}
	var rr rpcResp
	if err := json.Unmarshal(bodyBytes, &rr); err != nil {
		return latency, fmt.Errorf("decode rpc body: %w", err)
	}
	if rr.Error != nil {
		return latency, fmt.Errorf("rpc error code=%d", rr.Error.Code)
	}
	if rr.Result == "" {
		return latency, fmt.Errorf("rpc empty result")
	}
	return latency, nil
}

// looksLikeAuthBody scans the first 1 KiB for the literal "Must be authenticated"
// message returned by alchemy / quicknode / chainstack when keys expire.
func looksLikeAuthBody(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	head := b
	if len(head) > 1024 {
		head = head[:1024]
	}
	return strings.Contains(string(head), "Must be authenticated")
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// Compile-time assertion.
var _ RPCProbe = (*HTTPProbe)(nil)
