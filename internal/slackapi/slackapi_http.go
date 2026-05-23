package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPClient calls the two read-only Slack endpoints we need.
// Constitution IV: token is supplied per-call from config. Never logged.
// Constitution V: every call honors ctx; 5s default timeout.
type HTTPClient struct {
	BaseURL string // override for tests
	HTTP    *http.Client
}

// NewHTTPClient constructs a client with sane defaults.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		BaseURL: "https://slack.com/api",
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
}

type authTestResp struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// AuthTest hits slack auth.test. Returns true iff ok==true.
func (c *HTTPClient) AuthTest(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("slackapi: empty token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/auth.test", nil)
	if err != nil {
		return false, err
	}
	c.setAuth(req, token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("slack auth.test http %d", resp.StatusCode)
	}
	var r authTestResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&r); err != nil {
		return false, fmt.Errorf("slack auth.test decode: %w", err)
	}
	// Per Slack docs: ok=false with an explicit "error" field is the canonical
	// failure mode (invalid_auth / token_revoked / etc.). 200 + ok=false is
	// EXPECTED for an invalid token — alert rule treats this as token invalid.
	return r.OK, nil
}

type historyResp struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages []struct {
		TS string `json:"ts"`
	} `json:"messages"`
	HasMore bool `json:"has_more"`
	// We do NOT paginate. The 24h count is best-effort. If has_more is set we
	// return the truncated count + a sentinel error so the alert rule can use
	// the fallback. (See FR-010 — "fallback 으로 직전 값 유지".)
}

// History24h calls conversations.history with oldest=now-24h, inclusive=true,
// limit=200 (one page). Returns the count of messages observed.
//
// Pagination is intentionally skipped: in practice the outcome channel never
// exceeds ~50 messages/day. If has_more=true we still return the partial count
// plus an error so the caller can apply the FR-010 fallback (keep prior value).
func (c *HTTPClient) History24h(ctx context.Context, token, channelID string) (int, error) {
	if token == "" {
		return 0, fmt.Errorf("slackapi: empty token")
	}
	if channelID == "" {
		return 0, fmt.Errorf("slackapi: empty channelID")
	}
	oldest := strconv.FormatInt(time.Now().Add(-24*time.Hour).Unix(), 10)
	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("oldest", oldest)
	form.Set("inclusive", "true")
	form.Set("limit", "200")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/conversations.history", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	c.setAuth(req, token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 429 (rate limited) lands here — collector falls back to prior cache.
		return 0, fmt.Errorf("slack conversations.history http %d", resp.StatusCode)
	}
	var r historyResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&r); err != nil {
		return 0, fmt.Errorf("slack conversations.history decode: %w", err)
	}
	if !r.OK {
		return 0, fmt.Errorf("slack conversations.history error=%q", r.Error)
	}
	n := len(r.Messages)
	if r.HasMore {
		return n, fmt.Errorf("slack conversations.history: has_more (partial count %d)", n)
	}
	return n, nil
}

func (c *HTTPClient) setAuth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

// Compile-time assertion.
var _ Slack = (*HTTPClient)(nil)
