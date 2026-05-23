// Package rpc probes Arbitrum RPC endpoints via eth_blockNumber.
// Read-only. Phase 4 (T041).
package rpc

import (
	"context"
	"time"
)

// RPCProbe runs a single eth_blockNumber JSON-RPC call against url.
// MUST honor ctx deadline (5s recommended per plan.md).
// MUST NOT send any signing / transaction methods.
type RPCProbe interface {
	Probe(ctx context.Context, url string) (latency time.Duration, err error)
}
