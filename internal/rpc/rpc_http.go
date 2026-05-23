package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

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

	if resp.StatusCode != http.StatusOK {
		// drain a bit of body for diagnostics — but cap to avoid leaking a long
		// RPC error string to a future log entry (we never log this; we just
		// return it to the collector).
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		return latency, fmt.Errorf("rpc http %d", resp.StatusCode)
	}

	var rr rpcResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&rr); err != nil {
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

// Compile-time assertion.
var _ RPCProbe = (*HTTPProbe)(nil)
