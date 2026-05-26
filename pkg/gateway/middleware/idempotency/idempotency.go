// SPDX-License-Identifier: MIT

// Package idempotency wraps any http.Handler with §11.5 idempotency
// enforcement. The middleware reads the Idempotency-Key header on
// every POST, hashes the request body, and consults the configured
// Store. On a match-with-same-body it replays the cached Response.
// On a match-with-different-body it returns the §11.5
// 422 IDEMPOTENCY_KEY_REUSED envelope.
//
// The Store interface is small enough that callers can plug in either
// the in-memory implementation in this package or a Postgres-backed
// implementation in a later phase. Both implementations satisfy the
// same wire-level contract; the contract tests in
// tests/tier3_contract/rest_idempotency/ exercise the middleware
// itself, not the store.
package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	"github.com/lennylabs/lenny/pkg/idempotency"
)

// Store is the persistence contract the middleware drives. Production
// uses a Postgres-backed implementation; the in-memory store below
// satisfies the same shape for tests and the minimal gateway.
type Store interface {
	// Get returns the stored Record for (tenantID, key) or the zero
	// value when no record exists.
	Get(ctx context.Context, tenantID, key string) (idempotency.Record, bool, error)

	// Put inserts or replaces a record. Implementations MUST honour
	// the §11.5 24-hour TTL via Record.StoredAt.
	Put(ctx context.Context, record idempotency.Record) error
}

// MemoryStore is an in-memory Store backing tests and the minimal
// gateway. The map is keyed by (tenantID, key) and protected by an
// RWMutex.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[storeKey]idempotency.Record
	now     func() time.Time
}

type storeKey struct {
	tenantID string
	key      string
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records: make(map[storeKey]idempotency.Record),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (m *MemoryStore) Get(_ context.Context, tenantID, key string) (idempotency.Record, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.records[storeKey{tenantID, key}]
	if !ok {
		return idempotency.Record{}, false, nil
	}
	// Treat expired records as absent so the middleware writes a fresh entry.
	if rec.IsExpired(m.now()) {
		return idempotency.Record{}, false, nil
	}
	return rec, true, nil
}

func (m *MemoryStore) Put(_ context.Context, rec idempotency.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec.StoredAt.IsZero() {
		rec.StoredAt = m.now()
	}
	m.records[storeKey{rec.Key.TenantID, rec.Key.Value}] = rec
	return nil
}

// HeaderName is the §11.5 HTTP header that carries the idempotency
// key on REST requests.
const HeaderName = "Idempotency-Key"

// Metrics is the subset of gatewaymetrics.Metrics the §11.5 middleware
// emits. A nil Metrics passes every call through to a no-op so the
// middleware remains usable in tests and minimal gateways that do not
// register a registry. spec: §11.5 line 277.
type Metrics interface {
	// IncIdempotencyCacheWriteFailure increments
	// lenny_idempotency_cache_write_failures_total when the durable
	// store rejects a Put after the inner handler already executed —
	// the silent-failure case F-11.5.4 calls out.
	IncIdempotencyCacheWriteFailure(tenantID string)

	// IncIdempotencyCacheSkipped increments
	// lenny_idempotency_cache_skipped_total when the middleware
	// declines to persist the response by policy (5xx status from the
	// inner handler) — the 24-hour-error-replay case F-11.5.3.
	// reason is one of: "server_error", "no_status".
	IncIdempotencyCacheSkipped(tenantID, reason string)
}

// Options configures a Middleware.
type Options struct {
	// TenantFromRequest extracts the tenant id from the inbound
	// request. The minimal gateway uses the dev X-Lenny-Tenant-ID
	// header; production wires in pkg/auth.
	TenantFromRequest func(*http.Request) string

	// MaxBodyBytes caps the body the middleware buffers in memory
	// before hashing. Required because the middleware must read the
	// entire body, hash it, then either replay (no inner handler call)
	// or pass to the inner handler with a fresh reader.
	// Zero means 1 MiB.
	MaxBodyBytes int64

	// AllowedMethods restricts the methods the middleware will admit
	// when an Idempotency-Key header is present. Requests on any other
	// method pass through to the inner handler untouched (header is
	// ignored, no cache lookup, no cache write). Nil defaults to
	// {http.MethodPost} — the §11.5 critical operations are all POSTs.
	// spec: §11.5 line 268; F-11.5.7.
	AllowedMethods []string

	// AllowedPaths restricts which paths the middleware admits. Each
	// entry is a literal prefix match against r.URL.Path (so
	// "/v1/sessions" matches "/v1/sessions" and
	// "/v1/sessions/{id}/start"). Nil leaves every path admissible,
	// preserving the legacy behaviour for tests that wire the
	// middleware directly on a single handler; production callers
	// SHOULD pass the §11.5 enumerated set so a misguided client cannot
	// drag a non-critical endpoint into the cache. spec: §11.5 line
	// 268; F-11.5.7.
	AllowedPaths []string

	// Metrics receives §11.5 cache-write failure and skip counters.
	// Nil leaves observability disabled — the middleware still enforces
	// and still skips identically. spec: §11.5 line 277; F-11.5.4.
	Metrics Metrics

	// Logger receives a structured WARN line whenever the durable
	// store rejects a Put or the middleware declines to cache an inner
	// 5xx response. Nil uses log.Default(). spec: F-11.5.3, F-11.5.4.
	Logger *log.Logger
}

// Wrap returns an http.Handler that applies §11.5 idempotency before
// invoking inner. Requests without an Idempotency-Key header pass
// through untouched.
func Wrap(inner http.Handler, store Store, opts Options) http.Handler {
	if opts.TenantFromRequest == nil {
		opts.TenantFromRequest = defaultTenantFromRequest
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 1 << 20
	}
	if opts.AllowedMethods == nil {
		opts.AllowedMethods = []string{http.MethodPost}
	}
	return &middleware{inner: inner, store: store, opts: opts}
}

type middleware struct {
	inner http.Handler
	store Store
	opts  Options
}

// methodAllowed reports whether method is in opts.AllowedMethods. The
// default set is {POST}; an empty slice (zero-value after Wrap's
// fixup) admits nothing — the caller is expected to leave AllowedMethods
// nil if they want the default.
func (m *middleware) methodAllowed(method string) bool {
	for _, ok := range m.opts.AllowedMethods {
		if strings.EqualFold(ok, method) {
			return true
		}
	}
	return false
}

// pathAllowed reports whether path is admissible under opts.AllowedPaths.
// A nil/empty slice admits every path so single-handler test wiring
// continues to work without an explicit allow-list. Each entry uses
// the same `{var}` placeholder syntax as the Go 1.22 mux pattern (e.g.
// `/v1/sessions/{id}/finalize` matches `/v1/sessions/abc/finalize`); a
// trailing `/...` admits the segment subtree (e.g. `/v1/sessions/{id}/tool-use/...`
// matches the whole approve/deny suffix tree). spec: §11.5; F-11.5.7.
func (m *middleware) pathAllowed(path string) bool {
	if len(m.opts.AllowedPaths) == 0 {
		return true
	}
	for _, pattern := range m.opts.AllowedPaths {
		if pattern == "" {
			continue
		}
		if matchPathPattern(pattern, path) {
			return true
		}
	}
	return false
}

// matchPathPattern reports whether the request path satisfies the
// pattern. Segments matching `{name}` consume one segment of any
// (non-empty) value; a literal `...` tail (preceded by /) matches the
// rest of the path. spec: §11.5; F-11.5.7.
func matchPathPattern(pattern, path string) bool {
	patSegs := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathSegs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, ps := range patSegs {
		if ps == "..." {
			return true
		}
		if i >= len(pathSegs) {
			return false
		}
		if strings.HasPrefix(ps, "{") && strings.HasSuffix(ps, "}") {
			if pathSegs[i] == "" {
				return false
			}
			continue
		}
		if ps != pathSegs[i] {
			return false
		}
	}
	return len(patSegs) == len(pathSegs)
}

func (m *middleware) logger() *log.Logger {
	if m.opts.Logger != nil {
		return m.opts.Logger
	}
	return log.Default()
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(HeaderName)
	if key == "" {
		m.inner.ServeHTTP(w, r)
		return
	}

	// spec: §11.5 line 268 — the six "critical operations" are all
	// mutating POSTs. An Idempotency-Key header on any other method
	// is a caller mistake; passing the request through (instead of
	// caching it) keeps the §11.5 contract honest and prevents a
	// stale GET response from masking subsequent state changes for
	// 24 hours. Closes F-11.5.7.
	if !m.methodAllowed(r.Method) || !m.pathAllowed(r.URL.Path) {
		m.inner.ServeHTTP(w, r)
		return
	}

	tenantID := m.opts.TenantFromRequest(r)
	if tenantID == "" {
		// spec: §11.5 line 277 — "Idempotency keys are scoped per
		// tenant — the same key string used by different tenants is
		// treated independently." If the request reaches this
		// middleware without a tenant, the chain is misordered (the
		// idempotency middleware ran before auth or auth was bypassed
		// for this path); failing closed surfaces the wiring bug
		// instead of collapsing keys from different tenants under a
		// shared scope.
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"idempotency middleware: tenant could not be resolved from request — auth chain must precede idempotency",
			map[string]any{"reason": "tenant_required"})
		return
	}
	idemKey := idempotency.Key{TenantID: tenantID, Value: key}
	if err := idemKey.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", err.Error(), nil)
		return
	}

	body, err := readBodyLimited(r, m.opts.MaxBodyBytes)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", err.Error(), nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	hash := idempotency.HashBody(body)

	stored, found, err := m.store.Get(r.Context(), tenantID, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	var record idempotency.Record
	if found {
		record = stored
	}
	action, derr := idempotency.DetectReuse(record, hash, time.Now().UTC())
	if derr != nil {
		var reuseErr *idempotency.ReuseError
		if asReuseError(derr, &reuseErr) {
			writeError(w, reuseErr.Code(), reuseErr.ErrorCode(), reuseErr.Error(), map[string]any{
				"storedHash":  reuseErr.StoredHash,
				"inboundHash": reuseErr.InboundHash,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", derr.Error(), nil)
		return
	}

	switch action {
	case idempotency.ActionReplay:
		replayResponse(w, record.Response)
		return
	case idempotency.ActionStoreNew:
		captured := &captureWriter{header: http.Header{}}
		m.inner.ServeHTTP(captured, r)
		captured.flush(w)
		m.maybePersist(r.Context(), idemKey, hash, captured)
	}
}

// maybePersist decides whether to write the captured response to the
// durable cache, applying the §11.5 line 277 success-only-replay policy
// and surfacing Put errors via the configured logger + metrics.
// spec: §11.5 line 277; F-11.5.3, F-11.5.4, F-11.5.12.
func (m *middleware) maybePersist(ctx context.Context, idemKey idempotency.Key, hash string, captured *captureWriter) {
	status := captured.status
	if status == 0 {
		// spec: §11.5 line 277 — captured.status==0 means the inner
		// handler returned without calling WriteHeader; the flush path
		// emits 200 OK to the client, so we persist 200 too. Without
		// this normalisation the cache row stores a 0 that gets
		// rewritten to 200 on replay only by accident of replayResponse's
		// fallback. Closes F-11.5.12.
		status = http.StatusOK
	}
	if status >= 500 {
		// spec: §11.5 line 277 — a transient 5xx must not be replayed
		// for the entire 24-hour TTL; that would block a legitimate
		// retry against a now-healthy backend. The middleware does not
		// cache it, so the next request with the same key re-executes
		// the operation (and, if it succeeds, the cache row is written
		// then). Closes F-11.5.3.
		if m.opts.Metrics != nil {
			m.opts.Metrics.IncIdempotencyCacheSkipped(idemKey.TenantID, "server_error")
		}
		m.logger().Printf("idempotency: skipping cache write for 5xx response tenant=%q key=%q status=%d", idemKey.TenantID, idemKey.Value, status)
		return
	}
	fresh := idempotency.Record{
		Key:      idemKey,
		BodyHash: hash,
		Response: idempotency.Response{
			StatusCode: status,
			Headers:    cloneHeader(captured.header),
			Body:       captured.body.Bytes(),
		},
		StoredAt: time.Now().UTC(),
	}
	if err := m.store.Put(ctx, fresh); err != nil {
		// spec: §11.5 line 277 — the inner handler already executed and
		// the client already got the response, so we cannot fail the
		// HTTP exchange retroactively. A retry with the same key would
		// re-execute the operation (creating a duplicate session /
		// child), so surface the silent-failure case via metric + log
		// for the operator. Closes F-11.5.4.
		if m.opts.Metrics != nil {
			m.opts.Metrics.IncIdempotencyCacheWriteFailure(idemKey.TenantID)
		}
		m.logger().Printf("idempotency: cache write failed (operation already executed; retry with same key WILL re-execute) tenant=%q key=%q err=%q", idemKey.TenantID, idemKey.Value, err.Error())
	}
}

// asReuseError narrows err to *idempotency.ReuseError without
// importing errors at the package-level.
func asReuseError(err error, out **idempotency.ReuseError) bool {
	if e, ok := err.(*idempotency.ReuseError); ok {
		*out = e
		return true
	}
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return asReuseError(u.Unwrap(), out)
	}
	return false
}

// captureWriter is an http.ResponseWriter that buffers the response
// so the middleware can both forward to the client and stash a copy
// in the idempotency store.
type captureWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
	wrote  bool
}

func (c *captureWriter) Header() http.Header { return c.header }

func (c *captureWriter) WriteHeader(status int) {
	if c.wrote {
		return
	}
	c.status = status
	c.wrote = true
}

func (c *captureWriter) Write(p []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.body.Write(p)
}

func (c *captureWriter) flush(w http.ResponseWriter) {
	for k, vs := range c.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(c.body.Bytes())
}

// replayResponse mirrors the captured response onto the live writer.
// Multi-valued headers (Set-Cookie, Vary, WWW-Authenticate, …) are
// emitted once per value so the replayed response is byte-for-byte
// equivalent to the original wire response — anything less would
// quietly drop cookies on the second-of-two retries, which §11.5
// explicitly designates as the "same HTTP status and body" surface.
// spec: §11.5 line 277; F-11.5.9.
func replayResponse(w http.ResponseWriter, resp idempotency.Response) {
	for k, vs := range resp.Headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if resp.StatusCode == 0 {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(resp.StatusCode)
	}
	_, _ = w.Write(resp.Body)
}

func readBodyLimited(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	// Content-Length pre-check.
	if r.ContentLength > limit {
		return nil, &bodyTooLargeError{size: r.ContentLength, limit: limit}
	}
	buf := bytes.Buffer{}
	if _, err := io.Copy(&buf, io.LimitReader(r.Body, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, &bodyTooLargeError{size: int64(buf.Len()), limit: limit}
	}
	return buf.Bytes(), nil
}

type bodyTooLargeError struct {
	size  int64
	limit int64
}

func (e *bodyTooLargeError) Error() string {
	return "idempotency middleware: body size " + strconv.FormatInt(e.size, 10) + " exceeds limit " + strconv.FormatInt(e.limit, 10)
}

// cloneHeader copies http.Header into the typed map[string][]string the
// idempotency Record persists. Every value of a multi-valued header is
// preserved so the cached row replays the original wire response. The
// returned value-slice is a fresh slice — mutating the captured writer's
// header after the call cannot retroactively change the stored record.
// spec: §11.5 line 277; F-11.5.9.
func cloneHeader(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if len(vs) == 0 {
			continue
		}
		dup := make([]string, len(vs))
		copy(dup, vs)
		out[k] = dup
	}
	return out
}

// writeError emits the §15.1 canonical error envelope. Category and
// retryable are populated through the shared §15.2.1 classifier so
// the middleware's REST surface reports the same values for the same
// code as the rest of the gateway (sessionserver, MCP, etc.).
// spec: §15.1 lines 958-972 (error response envelope).
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	cat, retryable := errorclassify.Classify(code)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{
		"code":      code,
		"category":  string(cat),
		"message":   message,
		"retryable": retryable,
	}
	if details != nil {
		body["details"] = details
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

// defaultTenantFromRequest reads the tenant from the auth-set
// X-Lenny-Tenant-ID header (set by pkg/gateway/middleware/auth on
// every authenticated request). Returning "" causes the middleware to
// fail closed with 500 INTERNAL_ERROR — the §11.5 per-tenant scope
// must not silently collapse to a shared bucket when the request
// arrives without a tenant.
// spec: §11.5 line 277 — "scoped per tenant".
func defaultTenantFromRequest(r *http.Request) string {
	return r.Header.Get("X-Lenny-Tenant-ID")
}
