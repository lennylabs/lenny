// SPDX-License-Identifier: MIT

// Package ctl is the HTTP client library behind the `lenny-ctl`
// operator CLI. It wraps the §15.1 admin API surface so the CLI
// command handlers stay thin and the request/response plumbing is
// unit-testable without spawning a process.
//
// The client speaks the gateway's §15.1 JSON surface and carries
// the operator's bearer token (or, in dev mode, the X-Lenny-Tenant
// and X-Lenny-Roles dev headers) on every request.
package ctl

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the §15.1 admin API HTTP client.
type Client struct {
	baseURL string
	http    *http.Client

	// bearer, when set, is sent as `Authorization: Bearer <token>`.
	bearer string

	// devTenant + devRoles, when set, send the X-Lenny-Tenant-ID /
	// X-Lenny-Roles dev headers — the local Embedded Mode auth path.
	devTenant string
	devRoles  string
}

// Options configures a Client.
type Options struct {
	// BaseURL is the gateway root (e.g., http://localhost:8080).
	BaseURL string

	// Bearer is the operator's access token. Optional.
	Bearer string

	// DevTenant + DevRoles drive the dev-header auth path used by
	// Embedded Mode. Ignored when Bearer is set.
	DevTenant string
	DevRoles  string

	// Timeout bounds each request. Zero defaults to 30 s.
	Timeout time.Duration

	// InsecureSkipVerify disables TLS certificate verification. It
	// backs the §24.16 `--insecure-skip-verify` dev-only global flag
	// and must never be set in a production deployment.
	InsecureSkipVerify bool
}

// New returns a configured Client.
func New(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	httpClient := &http.Client{Timeout: timeout}
	if opts.InsecureSkipVerify {
		// spec: §24.16 line 205 — `--insecure-skip-verify` (dev only).
		// Clone the default transport so connection-pool defaults are
		// preserved and only TLS verification is relaxed.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		httpClient.Transport = tr
	}
	return &Client{
		baseURL:   strings.TrimRight(opts.BaseURL, "/"),
		http:      httpClient,
		bearer:    opts.Bearer,
		devTenant: opts.DevTenant,
		devRoles:  opts.DevRoles,
	}
}

// BaseURL returns the gateway root the client targets, with any
// trailing slash trimmed. Command handlers use it to name the target in
// up-front diagnostics before issuing a request.
func (c *Client) BaseURL() string { return c.baseURL }

// HasAuth reports whether the client carries a credential it can present
// to the gateway: either a bearer token or the Embedded Mode dev-header
// auth pair. A handler that requires an authenticated call (for example
// `lenny runtime publish`, whose register step is platform-admin per
// §24.18) checks this before reaching the gateway so a missing token
// surfaces as a clear CLI diagnostic rather than a server-side 401.
func (c *Client) HasAuth() bool { return c.bearer != "" || c.devTenant != "" }

// Bearer returns the operator bearer token the client carries, or the
// empty string when none is set. The token-exchange rotation
// (`lenny-ctl admin users rotate-token`, §24.9) reads it to populate the
// RFC 8693 `subject_token`.
func (c *Client) Bearer() string { return c.bearer }

// APIError is a non-2xx response surfaced as a Go error. It carries
// the §15.1 error envelope fields.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("lenny-ctl: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("lenny-ctl: HTTP %d", e.Status)
}

// Do issues a request to the gateway. method is the HTTP verb, path
// is the path beginning with `/`, body (when non-nil) is JSON-encoded
// as the request body, and out (when non-nil) receives the decoded
// JSON response.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("lenny-ctl: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("lenny-ctl: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lenny-ctl: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("lenny-ctl: decode response: %w", err)
		}
	}
	return nil
}

// Stream opens a long-lived GET against path and copies the response
// body to w until the server closes the stream or ctx is cancelled. It
// backs the §25.14 SSE tail commands (`lenny-ctl events tail`,
// `audit ... --follow`) where the response is a `text/event-stream`
// rather than a single JSON document. Unlike Do, Stream does not apply
// the per-request Timeout: an SSE stream is bounded only by ctx, so a
// 30 s client timeout would otherwise sever a healthy tail.
// spec: §25.14 line 4920 — `lenny-ctl events tail` maps to
// GET /v1/admin/events/stream (SSE).
func (c *Client) Stream(ctx context.Context, path string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("lenny-ctl: build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	c.applyAuth(req)

	// A timeout-free client reusing the configured transport (which
	// carries any InsecureSkipVerify relaxation). The nil-transport
	// case falls through to http.DefaultTransport.
	hc := &http.Client{Transport: c.http.Transport}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("lenny-ctl: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return parseAPIError(resp.StatusCode, raw)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		// A cancelled context is the normal exit path for an operator
		// who interrupted the tail; surface it as success.
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("lenny-ctl: stream %s: %w", path, err)
	}
	return nil
}

// Get issues a bounded GET against path and copies the response body to
// w. Unlike Stream it keeps the per-request Timeout, because the response
// is a finite document rather than a long-lived stream, and it sets no
// streaming Accept header. It backs the §24.15 `lenny-ctl logs pods`
// pod-log proxy, whose upstream (GET /v1/admin/logs/pods/{ns}/{name} on
// lenny-ops) returns the raw container log as text/plain.
// spec: §24.15 line 192; §25.4 lines 2528-2534.
func (c *Client) Get(ctx context.Context, path string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("lenny-ctl: build request: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lenny-ctl: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return parseAPIError(resp.StatusCode, raw)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("lenny-ctl: GET %s: %w", path, err)
	}
	return nil
}

// applyAuth adds the §10.2 auth headers to a request.
func (c *Client) applyAuth(req *http.Request) {
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
		return
	}
	if c.devTenant != "" {
		req.Header.Set("X-Lenny-Tenant-ID", c.devTenant)
	}
	if c.devRoles != "" {
		req.Header.Set("X-Lenny-Roles", c.devRoles)
		// A dev-roles caller is a human operator; stamp a subject so
		// the §11.7 audit trail records a non-empty actor.
		req.Header.Set("X-Lenny-User-ID", "lenny-ctl")
	}
}

// parseAPIError decodes the §15.1 error envelope from a non-2xx
// response body.
func parseAPIError(status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return &APIError{
		Status:  status,
		Code:    env.Error.Code,
		Message: env.Error.Message,
	}
}
