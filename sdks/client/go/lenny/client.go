// SPDX-License-Identifier: MIT

package lenny

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// userAgent identifies the SDK in the User-Agent request header.
const userAgent = "lenny-client-sdk-go"

// Client is the §15.1 REST gateway client. Construct it with New.
// A Client is safe for concurrent use by multiple goroutines.
type Client struct {
	baseURL    string
	httpClient *http.Client
	auth       Authenticator
	tenantID   string
	retry      RetryPolicy
	clock      func() time.Time
	rng        *rand.Rand
}

// Option configures a Client at construction.
type Option func(*Client)

// WithHTTPClient sets the underlying *http.Client. The default is a
// client with a 30-second timeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithAuth sets the authentication credential the Client attaches to
// every request. See BearerToken and APIKey.
func WithAuth(a Authenticator) Option {
	return func(c *Client) { c.auth = a }
}

// WithTenant sets the development X-Lenny-Tenant-ID header. The
// minimal gateway honors this header to scope requests to a tenant
// when no OIDC principal is present. Production deployments derive
// the tenant from the authenticated principal and ignore the header.
func WithTenant(tenantID string) Option {
	return func(c *Client) { c.tenantID = tenantID }
}

// WithRetryPolicy overrides DefaultRetryPolicy. Unset fields of the
// supplied policy are filled from DefaultRetryPolicy.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *Client) { c.retry = p.normalized() }
}

// WithClock overrides the time source. Tests inject a deterministic
// clock; production leaves this unset.
func WithClock(clock func() time.Time) Option {
	return func(c *Client) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// New returns a Client bound to baseURL. baseURL is the gateway
// origin (for example https://gateway.acme.com); the /v1 path prefix
// is appended by the SDK. New returns an error when baseURL is empty
// or does not parse as an absolute URL.
func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("lenny: base URL is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("lenny: base URL is invalid: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("lenny: base URL %q must be absolute", baseURL)
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		retry:      DefaultRetryPolicy,
		clock:      func() time.Time { return time.Now() },
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// requestOptions carries per-call settings threaded through do.
type requestOptions struct {
	idempotencyKey string
}

// RequestOption customizes a single request.
type RequestOption func(*requestOptions)

// WithIdempotencyKey attaches an Idempotency-Key header to the
// request. The §11.5 gateway middleware replays the cached response
// when the same key is presented again with the same body within the
// key's TTL. Use it on POST /v1/sessions so a retried create does
// not produce a second session.
func WithIdempotencyKey(key string) RequestOption {
	return func(o *requestOptions) { o.idempotencyKey = key }
}

// CreateSession calls POST /v1/sessions. The request is retried on a
// retryable error per the Client's retry policy. Pass
// WithIdempotencyKey so a retry is collapsed by the gateway's
// idempotency middleware rather than producing a duplicate session.
func (c *Client) CreateSession(ctx context.Context, req CreateSessionRequest, opts ...RequestOption) (*CreateSessionResult, error) {
	var out CreateSessionResult
	if err := c.do(ctx, http.MethodPost, "/v1/sessions", req, &out, opts...); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSession calls GET /v1/sessions/{id}.
func (c *Client) GetSession(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	var out Session
	if err := c.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id), nil, &out, opts...); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSessions calls GET /v1/sessions, applying the filters in opt.
// It returns one page; iterate with the returned NextCursor or use
// IterateSessions for an all-pages helper.
func (c *Client) ListSessions(ctx context.Context, opt ListOptions, opts ...RequestOption) (*SessionPage, error) {
	q := url.Values{}
	if opt.State != "" {
		q.Set("state", string(opt.State))
	}
	if opt.Runtime != "" {
		q.Set("runtime", opt.Runtime)
	}
	if opt.Cursor != "" {
		q.Set("cursor", opt.Cursor)
	}
	if opt.Limit > 0 {
		q.Set("limit", strconv.Itoa(opt.Limit))
	}
	path := "/v1/sessions"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var raw struct {
		Sessions   []Session `json:"sessions"`
		NextCursor string    `json:"nextCursor"`
		HasMore    bool      `json:"hasMore"`
		Pagination *struct {
			Cursor  string `json:"cursor"`
			HasMore bool   `json:"hasMore"`
		} `json:"pagination"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &raw, opts...); err != nil {
		return nil, err
	}
	page := &SessionPage{
		Sessions:   raw.Sessions,
		NextCursor: raw.NextCursor,
		HasMore:    raw.HasMore,
	}
	// The §25.2 pagination envelope is the canonical source when the
	// gateway supplies it; the top-level fields are the fallback.
	if raw.Pagination != nil {
		page.NextCursor = raw.Pagination.Cursor
		page.HasMore = raw.Pagination.HasMore
	}
	return page, nil
}

// IterateSessions walks every page of a GET /v1/sessions listing,
// invoking yield once per session. Iteration stops when yield
// returns false, when a page returns an error, or when the listing
// is exhausted. It is the cursor-iteration helper for the §15.6
// pagination-cursors contract.
func (c *Client) IterateSessions(ctx context.Context, opt ListOptions, yield func(Session) bool) error {
	cursor := opt.Cursor
	for {
		pageOpt := opt
		pageOpt.Cursor = cursor
		page, err := c.ListSessions(ctx, pageOpt)
		if err != nil {
			return err
		}
		for _, s := range page.Sessions {
			if !yield(s) {
				return nil
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

// DeleteSession calls DELETE /v1/sessions/{id}. The §15.1 contract
// moves a non-terminal session to the cancelled state and returns
// the updated envelope.
func (c *Client) DeleteSession(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodDelete, id, "", opts...)
}

// Finalize calls POST /v1/sessions/{id}/finalize, transitioning a
// created session to ready.
func (c *Client) Finalize(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodPost, id, "finalize", opts...)
}

// Start calls POST /v1/sessions/{id}/start, transitioning a ready
// session to running.
func (c *Client) Start(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodPost, id, "start", opts...)
}

// Interrupt calls POST /v1/sessions/{id}/interrupt, transitioning a
// running session to suspended.
func (c *Client) Interrupt(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodPost, id, "interrupt", opts...)
}

// Terminate calls POST /v1/sessions/{id}/terminate, transitioning a
// non-terminal session to completed.
func (c *Client) Terminate(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodPost, id, "terminate", opts...)
}

// Resume calls POST /v1/sessions/{id}/resume, transitioning a
// session awaiting client action back to running.
func (c *Client) Resume(ctx context.Context, id string, opts ...RequestOption) (*Session, error) {
	return c.transition(ctx, http.MethodPost, id, "resume", opts...)
}

// transition issues a no-body state-mutating call. action is the
// trailing path segment (finalize, start, ...); an empty action
// targets the bare /v1/sessions/{id} path for DELETE.
func (c *Client) transition(ctx context.Context, method, id, action string, opts ...RequestOption) (*Session, error) {
	path := "/v1/sessions/" + url.PathEscape(id)
	if action != "" {
		path += "/" + action
	}
	var out Session
	if err := c.do(ctx, method, path, nil, &out, opts...); err != nil {
		return nil, err
	}
	return &out, nil
}

// do executes one REST call with retry. It marshals body to JSON
// (when non-nil), sends the request, retries retryable failures per
// the Client's retry policy, and decodes a 2xx response into out.
// A non-2xx response is decoded into an *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any, opts ...RequestOption) error {
	var ro requestOptions
	for _, opt := range opts {
		opt(&ro)
	}

	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("lenny: marshal request body: %w", err)
		}
		bodyBytes = b
	}

	policy := c.retry
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if attempt > 1 {
			delay := policy.delayForAttempt(attempt-1, c.rng)
			if err := sleepCtx(ctx, delay); err != nil {
				return err
			}
		}

		status, respBody, err := c.roundTrip(ctx, method, path, bodyBytes, ro)
		if err != nil {
			// A transport-level failure (connection refused, DNS) is
			// retried like a TRANSIENT error.
			lastErr = err
			continue
		}

		if status >= 200 && status < 300 {
			if out == nil || len(respBody) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("lenny: decode %s %s response: %w", method, path, err)
			}
			return nil
		}

		apiErr := decodeAPIError(status, respBody)
		lastErr = apiErr
		if apiErr.Retryable && attempt < policy.MaxAttempts {
			continue
		}
		return apiErr
	}
	return lastErr
}

// roundTrip performs a single HTTP request and returns the status
// code and response body. It does not interpret the body.
func (c *Client) roundTrip(ctx context.Context, method, path string, bodyBytes []byte, ro requestOptions) (int, []byte, error) {
	var reqBody io.Reader
	if bodyBytes != nil {
		reqBody = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("lenny: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ro.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", ro.idempotencyKey)
	}
	if c.tenantID != "" {
		req.Header.Set("X-Lenny-Tenant-ID", c.tenantID)
	}
	if c.auth != nil {
		if err := c.auth.Apply(req); err != nil {
			return 0, nil, fmt.Errorf("lenny: apply auth: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("lenny: read response body: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// decodeAPIError parses a non-2xx response body into an *APIError.
// When the body does not carry a §15.1 envelope, the error code is
// left empty and Retryable is inferred from the HTTP status.
func decodeAPIError(status int, body []byte) *APIError {
	var envelope struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Code != "" {
		apiErr := envelope.Error
		apiErr.HTTPStatus = status
		return &apiErr
	}
	return &APIError{
		Message:    strings.TrimSpace(string(body)),
		Retryable:  retryableStatus(status),
		HTTPStatus: status,
	}
}

// sleepCtx waits for d or until ctx is done, whichever is first. It
// returns ctx.Err() when the context is canceled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
