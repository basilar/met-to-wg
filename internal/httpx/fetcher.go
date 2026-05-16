// Package httpx wraps a *http.Client behind an interface so the processor can
// fetch HTML pages in production and a fake in tests.
package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Doer abstracts *http.Client. Anything that can RoundTrip-equivalent will do.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Fetcher GETs a URL and returns the body. The Doer is injectable for tests.
type Fetcher struct {
	Client    Doer
	UserAgent string
}

// New returns a Fetcher backed by a *http.Client with a sensible timeout.
func New(timeout time.Duration, userAgent string) *Fetcher {
	return &Fetcher{
		Client:    &http.Client{Timeout: timeout},
		UserAgent: userAgent,
	}
}

// Get retrieves the URL and returns the response body. Callers must close it.
func (f *Fetcher) Get(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("get %s: status %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}
