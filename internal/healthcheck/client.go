// Package healthcheck pings a healthchecks.io-style URL on each tick so we
// know the worker is alive. The endpoint is plain HTTP — any 2xx is success.
package healthcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"met-to-wg/internal/httpx"
)

// Client posts a heartbeat to a fixed URL.
type Client struct {
	URL  string
	Doer httpx.Doer
}

// New returns a Client; if url is empty Ping is a no-op (callers needn't
// nil-check). This makes "no healthcheck configured" the natural default for
// local development.
func New(url string, timeout time.Duration) *Client {
	if url == "" {
		return &Client{}
	}
	return &Client{
		URL:  url,
		Doer: &http.Client{Timeout: timeout},
	}
}

// Ping sends the heartbeat. Always best-effort: errors are returned for the
// caller to log, but the caller should not abort processing if this fails.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.URL == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return fmt.Errorf("healthcheck: build request: %w", err)
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: ping: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}
