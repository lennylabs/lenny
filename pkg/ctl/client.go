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
}

// New returns a configured Client.
func New(opts Options) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:   strings.TrimRight(opts.BaseURL, "/"),
		http:      &http.Client{Timeout: timeout},
		bearer:    opts.Bearer,
		devTenant: opts.DevTenant,
		devRoles:  opts.DevRoles,
	}
}

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
