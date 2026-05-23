// SPDX-License-Identifier: MIT

// Package gateway is the §25.4 lenny-ops → gateway admin-API client.
// It owns two seams documented in §25.4:
//
//   - Client (this file) — the HTTPS client that calls the gateway's
//     admin API. Shared-state endpoints (pool config, connectors,
//     scaling, drift) route through the front-door ClusterIP Service;
//     per-replica endpoints (events buffer, health, recommendations)
//     fan out across every pod returned by ReplicaDiscovery.
//
//   - ReplicaDiscovery (discovery.go) — resolves the
//     §17.8 lenny-gateway-pods headless Service so per-replica calls
//     reach every pod.
//
// §25.4 keeps the client and the discovery interface in the same
// package so the fan-out methods compose without exposing a
// cross-package callback.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// TokenSource yields the §25.4 service-account OIDC token the client
// presents on every admin-API call. The production implementation
// refreshes via the projected ServiceAccount token volume; tests
// supply a static string.
type TokenSource interface {
	// Token returns the current bearer token. An error short-circuits
	// the request so a stale-or-failed refresh never leaks an empty
	// Authorization header.
	Token(ctx context.Context) (string, error)
}

// StaticToken is a TokenSource that returns a fixed bearer string.
// Useful in tests and the v1 single-process degraded mode before the
// OIDC refresh path is wired.
type StaticToken string

// Token returns the wrapped string.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// Config configures a Client.
type Config struct {
	// BaseURL is the gateway admin API base, normally the ClusterIP
	// Service (https://lenny-gateway:8443 by default). Required.
	BaseURL string
	// Token is the OIDC service-account token source. A nil source
	// means the client sends no Authorization header — only valid when
	// the gateway is configured with AllowDevHeaders.
	Token TokenSource
	// HTTPClient overrides the underlying transport. A nil value uses
	// an http.Client with the supplied PerRequestTimeout.
	HTTPClient *http.Client
	// PerRequestTimeout bounds each individual request. Required when
	// HTTPClient is nil.
	PerRequestTimeout time.Duration
	// Discovery resolves per-replica endpoints for the fan-out
	// methods. A nil discovery makes the fan-out methods return
	// ErrFanOutUnavailable.
	Discovery ReplicaDiscovery
	// FanOutTimeout bounds each per-replica request inside a fan-out
	// query. §25.4 ops.gateway.fanOutTimeoutSeconds default is 2s.
	FanOutTimeout time.Duration
}

// Client is the §25.4 gateway-admin client.
type Client struct {
	baseURL       string
	token         TokenSource
	http          *http.Client
	discovery     ReplicaDiscovery
	fanOutTimeout time.Duration
}

// NewClient validates cfg and returns a Client. An empty BaseURL is
// rejected; the spec keeps the base URL an explicit configuration
// value so a misconfigured chart does not silently hit a default
// endpoint.
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("gateway client: BaseURL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("gateway client: invalid BaseURL: %w", err)
	}
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.PerRequestTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		hc = &http.Client{Timeout: timeout}
	}
	fanOut := cfg.FanOutTimeout
	if fanOut <= 0 {
		fanOut = 2 * time.Second
	}
	return &Client{
		baseURL:       cfg.BaseURL,
		token:         cfg.Token,
		http:          hc,
		discovery:     cfg.Discovery,
		fanOutTimeout: fanOut,
	}, nil
}

// BaseURL returns the configured ClusterIP base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Get issues an authenticated GET against the gateway admin API and
// decodes the JSON response into out. A non-2xx response returns
// HTTPError so callers can branch on status.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, c.baseURL, path, nil, out)
}

// PostJSON issues an authenticated POST with a JSON body and decodes
// the response into out (which may be nil).
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, c.baseURL, path, body, out)
}

// do is the shared request body for both single-replica calls (against
// the ClusterIP) and per-replica fan-out calls (against a pod-IP base).
func (c *Client) do(ctx context.Context, method, base, path string, body, out any) error {
	var bodyReader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("gateway client: marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, base+path, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, base+path, nil)
	}
	if err != nil {
		return fmt.Errorf("gateway client: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != nil {
		tok, terr := c.token.Token(ctx)
		if terr != nil {
			return fmt.Errorf("gateway client: token: %w", terr)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return &HTTPError{Status: resp.StatusCode, Body: raw, URL: req.URL.String()}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// HTTPError reports a non-2xx response from the gateway admin API.
type HTTPError struct {
	Status int
	URL    string
	Body   []byte
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("gateway client: %s returned %d: %s", e.URL, e.Status, truncate(string(e.Body), 200))
}

// truncate returns s clamped to n runes plus an ellipsis when clamped.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ErrFanOutUnavailable is returned by the per-replica fan-out methods
// when no ReplicaDiscovery is configured. §25.4 names headless-Service
// discovery a hard dependency for fan-out; without it the client
// cannot reach individual pods.
var ErrFanOutUnavailable = errors.New("gateway client: fan-out requires ReplicaDiscovery")

// ReplicaResult is one per-replica outcome returned by the fan-out
// methods. Endpoint carries the pod-IP URL the query reached;
// Err is non-nil when that replica's call failed (timeout, 5xx).
type ReplicaResult struct {
	Endpoint string
	Body     json.RawMessage
	Err      error
}

// FanOutGet issues GET path against every endpoint ReplicaDiscovery
// returns and gathers the raw JSON bodies. Each per-replica call uses
// the configured FanOutTimeout. A discovery failure short-circuits
// with the discovery error; a per-replica failure lands in
// ReplicaResult.Err so the caller can render the §25.2 degradation
// envelope ("Aggregation based on N of M replicas").
//
// Calls run concurrently because the fan-out cost is the bottleneck
// during a Prometheus outage (§25.4 Fallback Caching).
func (c *Client) FanOutGet(ctx context.Context, path string) ([]ReplicaResult, error) {
	return c.fanOut(ctx, path, true)
}

// FanOutGetRaw is the §25.4 fan-out variant that returns each replica's
// response body without JSON decoding. The Prometheus text-format
// scrape served by `/metrics` is not JSON, so the Prometheus-fallback
// path uses this entry point to get the raw text it parses line by
// line.
func (c *Client) FanOutGetRaw(ctx context.Context, path string) ([]ReplicaResult, error) {
	return c.fanOut(ctx, path, false)
}

// fanOut is the shared body of FanOutGet and FanOutGetRaw. When
// decodeJSON is true the per-replica body is stored as a
// json.RawMessage; when false it is the response bytes verbatim.
func (c *Client) fanOut(ctx context.Context, path string, decodeJSON bool) ([]ReplicaResult, error) {
	if c.discovery == nil {
		return nil, ErrFanOutUnavailable
	}
	endpoints, err := c.discovery.Endpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("gateway client: discovery: %w", err)
	}
	if len(endpoints) == 0 {
		return nil, nil
	}
	results := make([]ReplicaResult, len(endpoints))
	var wg sync.WaitGroup
	for i, ep := range endpoints {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, c.fanOutTimeout)
			defer cancel()
			if decodeJSON {
				var raw json.RawMessage
				err := c.do(cctx, http.MethodGet, base, path, nil, &raw)
				results[i] = ReplicaResult{Endpoint: base, Body: raw, Err: err}
				return
			}
			body, derr := c.doRaw(cctx, http.MethodGet, base, path)
			results[i] = ReplicaResult{Endpoint: base, Body: body, Err: derr}
		}(i, ep)
	}
	wg.Wait()
	return results, nil
}

// doRaw is the JSON-free sibling of `do`. It returns the response body
// verbatim so callers parsing non-JSON formats (Prometheus text, raw
// SSE frames) can do so without an extra unmarshal round.
func (c *Client) doRaw(ctx context.Context, method, base, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("gateway client: build request: %w", err)
	}
	if c.token != nil {
		tok, terr := c.token.Token(ctx)
		if terr != nil {
			return nil, fmt.Errorf("gateway client: token: %w", terr)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPError{Status: resp.StatusCode, Body: body, URL: req.URL.String()}
	}
	return body, nil
}
