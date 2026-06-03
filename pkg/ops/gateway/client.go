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

// TLSMetrics records the §25.4 NET-070 admin-API transport posture on
// every GatewayClient request attempt. The production adapter
// increments lenny_ops_admin_api_tls_handshake_total{result}; the
// OpsAdminAPIPlaintextDetected alert (§16.5) fires on a "plaintext"
// result. Tests pass a recording stub or the Noop.
//
// spec: §25.4 lines 2538-2546.
type TLSMetrics interface {
	// Handshake records one connection attempt. result is "plaintext"
	// when the admin-API URL is http://, "tls" on a completed HTTPS
	// request, and "tls_error" when an HTTPS request fails at the
	// transport layer.
	Handshake(result string)
}

// NoopTLSMetrics discards handshake results.
type NoopTLSMetrics struct{}

// Handshake implements TLSMetrics.
func (NoopTLSMetrics) Handshake(string) {}

// Revoker is the optional capability a TokenSource implements when it
// can drop a cached token on a 401, the §25.4 revocation-detection
// path. The Client calls MarkRevoked so the next request reloads the
// service-account token.
type Revoker interface {
	MarkRevoked()
}

// Handshake result labels.
const (
	handshakePlaintext = "plaintext"
	handshakeTLS       = "tls"
	handshakeTLSError  = "tls_error"
)

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
	// Breaker is the §25.4 per-replica circuit breaker for the fan-out
	// path. A nil breaker disables circuit breaking (every replica is
	// always attempted).
	Breaker *CircuitBreaker
	// TLSMetrics records the §25.4 NET-070 admin-API transport posture
	// on each request. A nil value uses NoopTLSMetrics.
	TLSMetrics TLSMetrics
}

// Client is the §25.4 gateway-admin client.
type Client struct {
	baseURL       string
	token         TokenSource
	http          *http.Client
	discovery     ReplicaDiscovery
	fanOutTimeout time.Duration
	breaker       *CircuitBreaker
	tlsMetrics    TLSMetrics
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
	tlsMetrics := cfg.TLSMetrics
	if tlsMetrics == nil {
		tlsMetrics = NoopTLSMetrics{}
	}
	return &Client{
		baseURL:       cfg.BaseURL,
		token:         cfg.Token,
		http:          hc,
		discovery:     cfg.Discovery,
		fanOutTimeout: fanOut,
		breaker:       cfg.Breaker,
		tlsMetrics:    tlsMetrics,
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
	c.recordHandshake(req.URL.Scheme, err)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			c.markRevoked()
		}
		return &HTTPError{Status: resp.StatusCode, Body: raw, URL: req.URL.String()}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// recordHandshake reports the §25.4 NET-070 transport posture for one
// request attempt. An http:// scheme is always "plaintext"; an https://
// request is "tls" when it completed at the transport layer and
// "tls_error" when it failed before a response.
func (c *Client) recordHandshake(scheme string, transportErr error) {
	switch {
	case scheme == "http":
		c.tlsMetrics.Handshake(handshakePlaintext)
	case transportErr != nil:
		c.tlsMetrics.Handshake(handshakeTLSError)
	default:
		c.tlsMetrics.Handshake(handshakeTLS)
	}
}

// markRevoked flags the token source for reload on the next request
// when it supports revocation. The §25.4 401-triggered revocation path.
func (c *Client) markRevoked() {
	if r, ok := c.token.(Revoker); ok {
		r.MarkRevoked()
	}
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
		// §25.4 per-replica circuit breaker: skip a replica whose
		// breaker is open so one struggling replica does not slow the
		// aggregation. The skip lands as a ReplicaResult with
		// ErrCircuitOpen so the caller counts it toward the §25.2
		// "based on N of M replicas" degradation envelope.
		if !c.breaker.Allow(ep) {
			results[i] = ReplicaResult{Endpoint: ep, Err: ErrCircuitOpen}
			continue
		}
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, c.fanOutTimeout)
			defer cancel()
			var rerr error
			if decodeJSON {
				var raw json.RawMessage
				rerr = c.do(cctx, http.MethodGet, base, path, nil, &raw)
				results[i] = ReplicaResult{Endpoint: base, Body: raw, Err: rerr}
			} else {
				body, derr := c.doRaw(cctx, http.MethodGet, base, path)
				rerr = derr
				results[i] = ReplicaResult{Endpoint: base, Body: body, Err: derr}
			}
			c.recordReplicaOutcome(base, rerr)
		}(i, ep)
	}
	wg.Wait()
	return results, nil
}

// recordReplicaOutcome feeds the per-replica circuit breaker. A nil
// error or a 4xx (the replica responded; the request was malformed or
// unauthorized, not a replica-health problem) counts as a success; a
// transport error or a 5xx counts as a consecutive failure that can
// trip the breaker.
func (c *Client) recordReplicaOutcome(endpoint string, err error) {
	if c.breaker == nil {
		return
	}
	if err == nil {
		c.breaker.RecordSuccess(endpoint)
		return
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status < 500 {
		c.breaker.RecordSuccess(endpoint)
		return
	}
	c.breaker.RecordFailure(endpoint)
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
	c.recordHandshake(req.URL.Scheme, err)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusUnauthorized {
			c.markRevoked()
		}
		return nil, &HTTPError{Status: resp.StatusCode, Body: body, URL: req.URL.String()}
	}
	return body, nil
}
