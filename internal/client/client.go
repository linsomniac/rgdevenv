package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIError is a non-2xx response decoded from the API's {error,code} body (§12).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (%s, http %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("%s (http %d)", e.Message, e.Status)
}

// Client calls the rgdevenv management REST API with a bearer token.
type Client struct {
	base  string // base URL without trailing slash
	token string
	hc    *http.Client
}

// New builds a Client. The base URL must be absolute (http/https). Insecure skips
// TLS verification (dev only).
func New(cfg Config) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.API))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("client: invalid API base URL %q", cfg.API)
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if cfg.Insecure {
		// AIDEV-NOTE: InsecureSkipVerify is intentionally allowed here for dev environments
		// with self-signed certs or private CAs not installed; never use in production.
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // dev only
	}
	return &Client{base: strings.TrimRight(cfg.API, "/"), token: cfg.Token, hc: hc}, nil
}

// do performs an authenticated request. body (if non-nil) is JSON-encoded; out
// (if non-nil) is JSON-decoded from a 2xx response. A non-2xx response is mapped
// to *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("client: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	// AIDEV-NOTE: Decode returns io.EOF only for a zero-byte body (a 2xx with no
	// payload); a truncated body returns io.ErrUnexpectedEOF, which we DO surface.
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
		return fmt.Errorf("client: decode response: %w", err)
	}
	return nil
}

func decodeAPIError(resp *http.Response) error {
	ae := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(b, &body) == nil && body.Error != "" {
		ae.Message = body.Error
		ae.Code = body.Code
	}
	return ae
}
