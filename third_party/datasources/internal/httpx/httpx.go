// Package httpx is the single shared HTTP utility for all data-source packages:
// a pooled client with retry/backoff and JSON helpers. Source packages must not
// roll their own client — they call httpx so timeout/retry/UA behaviour is uniform.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// secretQueryParams are query keys whose values are credentials; they are
// redacted from transport-error strings so a DNS/TLS/timeout failure (whose
// *url.Error embeds the full request URL) can't leak keys into logs/responses.
var secretQueryParams = []string{"token", "api_key", "apikey", "key"}

// redactURLError strips secret query params from a *url.Error's URL. Non-URL
// errors pass through unchanged.
func redactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	if u, perr := url.Parse(ue.URL); perr == nil {
		q := u.Query()
		redacted := false
		for _, k := range secretQueryParams {
			if q.Has(k) {
				q.Set(k, "REDACTED")
				redacted = true
			}
		}
		if redacted {
			u.RawQuery = q.Encode()
			ue.URL = u.String()
		}
	}
	return ue
}

// DefaultUserAgent is sent on every request; some upstreams (Yahoo, Binance Alpha)
// reject the default Go UA.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Client is a thin retrying wrapper over net/http.
type Client struct {
	hc      *http.Client
	ua      string
	retries int
}

// New returns a Client with a pooled transport and the given per-request timeout.
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	return &Client{
		hc: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		ua:      DefaultUserAgent,
		retries: 3,
	}
}

// Default is a shared client for simple callers.
var Default = New(12 * time.Second)

// GetBytes performs a GET with retry/backoff and returns the response body.
// headers may be nil. Non-2xx is an error (body included, truncated).
func (c *Client) GetBytes(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	return c.doBytes(ctx, http.MethodGet, url, headers, nil)
}

// doBytes performs method on url with retry/backoff and returns the response
// body. reqBody may be nil (GET); for a body, it is re-sent on each retry.
func (c *Client) doBytes(ctx context.Context, method, url string, headers map[string]string, reqBody []byte) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 400 * time.Millisecond):
			}
		}
		var bodyReader io.Reader
		if reqBody != nil {
			bodyReader = bytes.NewReader(reqBody)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = redactURLError(err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, truncate(body, 200))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(body, 200))
		}
		return body, nil
	}
	return nil, fmt.Errorf("after %d attempts: %w", c.retries, lastErr)
}

// GetJSON performs GetBytes and unmarshals into out.
func (c *Client) GetJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	body, err := c.GetBytes(ctx, url, headers)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// PostJSON performs a POST with the given JSON body (Content-Type set to
// application/json) and unmarshals the response into out. headers may be nil.
//
// Like GetBytes, this retries on 429/5xx and re-sends the full body each
// attempt — only use it for idempotent/read-only POST query endpoints, not for
// non-idempotent writes where a retry could double-submit.
func (c *Client) PostJSON(ctx context.Context, url string, headers map[string]string, reqBody []byte, out any) error {
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	body, err := c.doBytes(ctx, http.MethodPost, url, h, reqBody)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
