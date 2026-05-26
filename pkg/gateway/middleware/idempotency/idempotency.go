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
	"net/http"
	"strconv"
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
	return &middleware{inner: inner, store: store, opts: opts}
}

type middleware struct {
	inner http.Handler
	store Store
	opts  Options
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get(HeaderName)
	if key == "" {
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
		fresh := idempotency.Record{
			Key:      idemKey,
			BodyHash: hash,
			Response: idempotency.Response{
				StatusCode: captured.status,
				Headers:    flattenHeader(captured.header),
				Body:       captured.body.Bytes(),
			},
			StoredAt: time.Now().UTC(),
		}
		_ = m.store.Put(r.Context(), fresh)
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

func replayResponse(w http.ResponseWriter, resp idempotency.Response) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
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

func flattenHeader(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
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
