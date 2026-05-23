// Package slackapi probes the publisher's Slack workspace.
// Only two read-only endpoints are used: auth.test, conversations.history.
// Token is supplied per-call from config — never stored globally, never logged.
// Phase 4 (T042).
package slackapi

import "context"

// Slack exposes the read-only methods the exporter needs. Implementations
// MUST NOT call any write methods (chat.postMessage, conversations.create, etc.).
type Slack interface {
	// AuthTest reports whether the bot token is valid (ok=true).
	AuthTest(ctx context.Context, token string) (bool, error)
	// History24h returns the count of messages in channelID over the last 24h.
	History24h(ctx context.Context, token, channelID string) (int, error)
}
