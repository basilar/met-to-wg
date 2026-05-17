// Package windguru speaks Windguru's "upload" API.
//
// Wire protocol:
//
//	GET <base>?uid=<u>&salt=<s>&hash=<h>&<field=value>...
//
// where:
//
//	salt = md5(unix-timestamp-string)
//	hash = md5(salt + uid + password)
//
// Windguru ignores fields it doesn't know about. We strip water_temperature
// upstream (the station packages decide which fields apply) before getting
// here, so this package just signs and sends whatever it's given.
package windguru

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"met-to-wg/internal/httpx"
)

const defaultBaseURL = "https://www.windguru.cz/upload/api.php"

// Client uploads observations to Windguru.
type Client struct {
	BaseURL string
	Doer    httpx.Doer
	Now     func() time.Time // injectable for deterministic tests
}

// New builds a Client with sensible defaults; pass "" for baseURL to use the
// upstream production endpoint.
func New(baseURL string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL: baseURL,
		Doer:    &http.Client{Timeout: timeout},
		Now:     time.Now,
	}
}

// Upload signs the request with the station's credentials and POSTs it to
// Windguru. (The upstream API is technically GET; we keep it GET because
// Windguru actually parses the query string.)
func (c *Client) Upload(ctx context.Context, uid, password string, fields map[string]string) error {
	salt := md5hex(strconv.FormatInt(c.now().Unix(), 10))
	hash := md5hex(salt + uid + password)

	params := url.Values{}
	params.Set("uid", uid)
	params.Set("salt", salt)
	params.Set("hash", hash)
	// Sort field keys so the request URL is deterministic (helpful for tests
	// and for log analysis).
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		params.Set(k, fields[k])
	}

	endpoint := c.BaseURL + "?" + params.Encode()
	slog.Info("windguru request", "uid", uid, "salt", salt, "hash", hash, "url", endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("windguru: build request: %w", err)
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return fmt.Errorf("windguru: upload: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("windguru: status %d: %s", resp.StatusCode, string(body))
	}
	// Windguru returns 200 OK with a plain-text body — "OK" on success,
	// "ERROR: <reason>" (e.g. bad hash, unknown station) on failure. The HTTP
	// status alone isn't enough to tell them apart.
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(strings.ToUpper(trimmed), "ERROR") {
		return fmt.Errorf("windguru: %s", trimmed)
	}
	return nil
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
