// SPDX-License-Identifier: MIT

package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// stubStore lets a test inject Get/Put/Claim errors.
type stubStore struct {
	getErr   error
	putErr   error
	claimErr error
	// Inject behaviour for Claim: when injectClaimed is set, Claim returns
	// (zero, true, nil). Otherwise Claim falls through to a Get-shaped
	// lookup using rec/found, so existing tests that only seed (rec, found)
	// continue to behave as if no prior row existed (claim succeeds) or
	// the prior row replays (claim returns it). Two-arg sentinel keeps
	// existing tests source-compatible.
	rec      idempotency.Record
	found    bool
	puts     int
	releases int
}

func (s *stubStore) Get(_ context.Context, _, _ string) (idempotency.Record, bool, error) {
	if s.getErr != nil {
		return idempotency.Record{}, false, s.getErr
	}
	return s.rec, s.found, nil
}

func (s *stubStore) Put(_ context.Context, rec idempotency.Record) error {
	s.puts++
	if s.putErr != nil {
		return s.putErr
	}
	s.rec = rec
	s.found = true
	return nil
}

// Claim mimics the atomic claim: when no prior record exists, the
// middleware "wins the slot" (claimed=true, no existing row). When a
// prior record exists, it's returned with claimed=false so the
// middleware's existing-row branch runs. Errors from getErr are
// surfaced through claimErr too so the §15.1 envelope test keeps
// covering the store-failure path. spec: §11.5 line 277; F-11.5.2.
func (s *stubStore) Claim(_ context.Context, tenantID, key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	if s.claimErr != nil {
		return idempotency.Record{}, false, s.claimErr
	}
	if s.getErr != nil {
		return idempotency.Record{}, false, s.getErr
	}
	if s.found {
		return s.rec, false, nil
	}
	// Simulate inserting a pending row.
	s.rec = idempotency.Record{
		Key:      idempotency.Key{TenantID: tenantID, Value: key},
		BodyHash: bodyHash,
		Response: idempotency.Response{StatusCode: 0},
		StoredAt: now,
	}
	s.found = true
	return idempotency.Record{}, true, nil
}

func (s *stubStore) Release(_ context.Context, _, _ string) error {
	s.releases++
	s.found = false
	s.rec = idempotency.Record{}
	return nil
}

// spec: §11.5 line 277 "scoped per tenant". The middleware must fail
// closed when no tenant header is present so requests are never
// collapsed under a shared "default" bucket. spec: F-11.5.13.
func TestWrap_FailsClosedWhenTenantMissing_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatalf("inner handler must not be invoked when tenant is missing")
		w.WriteHeader(http.StatusOK)
	})
	wrapped := Wrap(inner, NewMemoryStore(), Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "abc-123")
	// No X-Lenny-Tenant-ID header.

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil {
		t.Fatalf("error body missing: %v", env)
	}
	if body["code"] != "INTERNAL_ERROR" {
		t.Errorf("code = %v, want INTERNAL_ERROR", body["code"])
	}
	details, _ := body["details"].(map[string]any)
	if details == nil || details["reason"] != "tenant_required" {
		t.Errorf("details.reason = %v, want tenant_required", details)
	}
}

// spec: §11.5 line 277. When the tenant header is present, the
// middleware proceeds normally and reaches the inner handler.
func TestWrap_AdmitsWhenTenantPresent_spec_11_5(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})
	wrapped := Wrap(inner, NewMemoryStore(), Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "abc-123")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("inner handler must be invoked")
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
}

// spec: §15.1 lines 958-972 — the canonical error envelope carries
// code, category, message, retryable, and (optionally) details. The
// middleware must emit the same shape as sessionserver. spec: F-11.5.14.
func TestWrap_ErrorEnvelopeIncludesCategoryAndRetryable_spec_15_1(t *testing.T) {
	cases := []struct {
		name      string
		key       string // oversized → INVALID_IDEMPOTENCY_KEY
		tenant    string
		body      io.Reader
		wantCode  string
		wantCat   string
		wantRetry bool
	}{
		{
			// spec: §11.5 line 277 — `INVALID_IDEMPOTENCY_KEY` is the
			// dedicated PERMANENT envelope for malformed keys (the same
			// key fails identically until the client picks a different
			// one). F-15.1.30 catalogued it on the classifier.
			name:      "INVALID_IDEMPOTENCY_KEY",
			key:       strings.Repeat("x", idempotency.MaxKeyLength+1),
			tenant:    "acme",
			body:      strings.NewReader(`{}`),
			wantCode:  "INVALID_IDEMPOTENCY_KEY",
			wantCat:   "PERMANENT",
			wantRetry: false,
		},
		{
			name:      "INTERNAL_ERROR_when_tenant_missing",
			key:       "abc",
			tenant:    "",
			body:      strings.NewReader(`{}`),
			wantCode:  "INTERNAL_ERROR",
			wantCat:   "TRANSIENT",
			wantRetry: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
			wrapped := Wrap(inner, NewMemoryStore(), Options{})
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions", tc.body)
			req.Header.Set(HeaderName, tc.key)
			if tc.tenant != "" {
				req.Header.Set("X-Lenny-Tenant-ID", tc.tenant)
			}
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			var env map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			body, _ := env["error"].(map[string]any)
			if body == nil {
				t.Fatalf("error body missing: %v", env)
			}
			if body["code"] != tc.wantCode {
				t.Errorf("code = %v, want %v", body["code"], tc.wantCode)
			}
			if body["category"] != tc.wantCat {
				t.Errorf("category = %v, want %v", body["category"], tc.wantCat)
			}
			if body["retryable"] != tc.wantRetry {
				t.Errorf("retryable = %v, want %v", body["retryable"], tc.wantRetry)
			}
			if _, ok := body["message"]; !ok {
				t.Errorf("message field missing: %v", body)
			}
		})
	}
}

// spec: §15.1 + §11.5 — the 422 IDEMPOTENCY_KEY_REUSED envelope must
// carry the spec-mandated code/category/retryable triple.
// spec: F-11.5.14.
func TestWrap_ReuseEnvelopeShape_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sessionId":"sess-1"}`))
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(payload))
		req.Header.Set(HeaderName, "k1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	// First call stores.
	if r := send(`{"a":1}`); r.Code != http.StatusCreated {
		t.Fatalf("first call status = %d, want 201", r.Code)
	}
	// Second call with a different body → 422 IDEMPOTENCY_KEY_REUSED.
	r := send(`{"a":2}`)
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reuse status = %d, want 422", r.Code)
	}
	var env map[string]any
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil {
		t.Fatalf("error body missing: %v", env)
	}
	if body["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("code = %v, want IDEMPOTENCY_KEY_REUSED", body["code"])
	}
	if body["category"] != "PERMANENT" {
		t.Errorf("category = %v, want PERMANENT", body["category"])
	}
	if body["retryable"] != false {
		t.Errorf("retryable = %v, want false", body["retryable"])
	}
}

// spec: F-11.5.13 — defaultTenantFromRequest must never return
// "default" as a synthetic fallback.
func TestDefaultTenantFromRequest_ReturnsEmptyWhenHeaderMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if got := defaultTenantFromRequest(req); got != "" {
		t.Errorf("default tenant = %q, want empty string", got)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if got := defaultTenantFromRequest(req); got != "acme" {
		t.Errorf("with header = %q, want acme", got)
	}
}

// spec: F-11.5.14 — when Put fails the middleware returns an envelope
// in the spec shape (still no panic, still carries category/retryable).
func TestWrap_StoreGetError_EnvelopeShape_spec_15_1(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	store := &stubStore{getErr: errors.New("boom")}
	wrapped := Wrap(inner, store, Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil || body["code"] != "INTERNAL_ERROR" || body["category"] != "TRANSIENT" || body["retryable"] != true {
		t.Errorf("envelope missing canonical fields: %v", env)
	}
}

// recordingMetrics captures the §11.5 cache-write-failure / cache-skipped
// counter labels so the test can assert the middleware emitted the
// right rows. spec: F-11.5.3, F-11.5.4.
type recordingMetrics struct {
	mu       sync.Mutex
	failures []string // tenant_id
	skipped  []struct{ tenant, reason string }
}

func (r *recordingMetrics) IncIdempotencyCacheWriteFailure(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, tenantID)
}

func (r *recordingMetrics) IncIdempotencyCacheSkipped(tenantID, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipped = append(r.skipped, struct{ tenant, reason string }{tenantID, reason})
}

// spec: §11.5 line 277 — a transient 5xx must not be replayed for the
// 24-hour TTL; the middleware skips the cache write so the next retry
// re-executes against a (hopefully) healthy backend. Closes F-11.5.3.
func TestWrap_DoesNotCache5xxResponses_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR"}}`))
	})
	store := NewMemoryStore()
	rm := &recordingMetrics{}
	var buf bytes.Buffer
	wrapped := Wrap(inner, store, Options{Metrics: rm, Logger: log.New(&buf, "", 0)})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("client status = %d, want 500", rec.Code)
	}
	// The cache must be empty: the next retry should re-execute, not replay.
	if _, found, err := store.Get(context.Background(), "acme", "k1"); err != nil || found {
		t.Errorf("5xx must not be persisted: found=%v err=%v", found, err)
	}
	if len(rm.skipped) != 1 || rm.skipped[0].tenant != "acme" || rm.skipped[0].reason != "server_error" {
		t.Errorf("skip metric: got %+v, want one server_error row", rm.skipped)
	}
	if !strings.Contains(buf.String(), "skipping cache write for 5xx") {
		t.Errorf("expected WARN log on 5xx skip, got %q", buf.String())
	}
}

// spec: §11.5 line 277 — a 4xx is a deterministic per-request outcome
// (e.g. VALIDATION_ERROR), so it IS cached: replay is correct and
// avoids re-executing the rejected operation. spec: F-11.5.3.
func TestWrap_Caches4xxResponses_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR"}}`))
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
		req.Header.Set(HeaderName, "k1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	if r := send(); r.Code != http.StatusBadRequest {
		t.Fatalf("first status = %d, want 400", r.Code)
	}
	if r := send(); r.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400", r.Code)
	}
	if calls != 1 {
		t.Errorf("inner handler called %d times, want 1 (4xx must replay)", calls)
	}
}

// spec: §11.5 line 277 — when the durable Put rejects after the inner
// handler executed, the failure is recorded so the operator knows a
// retry with the same key WILL re-execute. Closes F-11.5.4.
func TestWrap_StorePutError_LogsAndIncrementsMetric_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sessionId":"sess-1"}`))
	})
	store := &stubStore{putErr: errors.New("rls denial")}
	rm := &recordingMetrics{}
	var buf bytes.Buffer
	wrapped := Wrap(inner, store, Options{Metrics: rm, Logger: log.New(&buf, "", 0)})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// The client still sees the 201 from the inner handler — we cannot
	// rewrite the wire response after the fact.
	if rec.Code != http.StatusCreated {
		t.Fatalf("client status = %d, want 201", rec.Code)
	}
	if len(rm.failures) != 1 || rm.failures[0] != "acme" {
		t.Errorf("write-failure metric: got %+v, want one acme row", rm.failures)
	}
	if !strings.Contains(buf.String(), "cache write failed") || !strings.Contains(buf.String(), "rls denial") {
		t.Errorf("expected WARN log on Put failure, got %q", buf.String())
	}
}

// spec: §11.5 line 268 — only the six "critical operations" support
// idempotency. A GET with an Idempotency-Key passes through without
// being trapped in the cache. Closes F-11.5.7.
func TestWrap_PassesThroughNonAllowedMethod_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sessions":[]}`))
	})
	store := &stubStore{}
	wrapped := Wrap(inner, store, Options{})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
		req.Header.Set(HeaderName, "k1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d status = %d, want 200", i, rec.Code)
		}
	}
	if calls != 2 {
		t.Errorf("inner called %d times, want 2 (GET must not be cached)", calls)
	}
	if store.puts != 0 {
		t.Errorf("store.Put called %d times, want 0 for GET passthrough", store.puts)
	}
}

// spec: §11.5 line 268 — POST is the default admitted method.
func TestWrap_AdmitsPOSTWithDefaultAllowedMethods_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	store := &stubStore{}
	wrapped := Wrap(inner, store, Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if store.puts != 1 {
		t.Errorf("Put count = %d, want 1 (POST must be cached)", store.puts)
	}
}

// countingStore wraps NewMemoryStore so we can assert exactly how
// many Put calls each request triggered without losing the proper
// (tenant,key) → record map.
type countingStore struct {
	inner *MemoryStore
	puts  int
}

func (c *countingStore) Get(ctx context.Context, tenantID, key string) (idempotency.Record, bool, error) {
	return c.inner.Get(ctx, tenantID, key)
}

func (c *countingStore) Put(ctx context.Context, rec idempotency.Record) error {
	c.puts++
	return c.inner.Put(ctx, rec)
}

func (c *countingStore) Claim(ctx context.Context, tenantID, key, bodyHash string, now time.Time) (idempotency.Record, bool, error) {
	return c.inner.Claim(ctx, tenantID, key, bodyHash, now)
}

func (c *countingStore) Release(ctx context.Context, tenantID, key string) error {
	return c.inner.Release(ctx, tenantID, key)
}

// spec: §11.5 — only paths matching an AllowedPaths pattern are
// admissible; other POSTs pass through without caching. Closes
// F-11.5.7.
func TestWrap_PathAllowList_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})
	store := &countingStore{inner: NewMemoryStore()}
	opts := Options{
		AllowedPaths: []string{
			"/v1/sessions",
			"/v1/sessions/{id}/finalize",
			"/v1/sessions/{id}/tool-use/...",
		},
	}
	wrapped := Wrap(inner, store, opts)

	cases := []struct {
		path    string
		wantPut bool
	}{
		{"/v1/sessions", true},
		{"/v1/sessions/abc/finalize", true},
		{"/v1/sessions/abc/tool-use/tc-1/approve", true},
		{"/v1/sessions/abc/start", false},     // not on list
		{"/v1/sessions/abc/terminate", false}, // not on list
		{"/v1/users/me", false},               // unrelated
	}
	for _, c := range cases {
		before := store.puts
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(`{}`))
		// Distinct keys keep one Get from short-circuiting the next as
		// a replay against the prior path.
		req.Header.Set(HeaderName, "k-"+c.path)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("path=%q status=%d, want 200", c.path, rec.Code)
		}
		got := store.puts > before
		if got != c.wantPut {
			t.Errorf("path=%q cached=%v, want %v", c.path, got, c.wantPut)
		}
	}
	if calls != len(cases) {
		t.Errorf("inner called %d, want %d (every request must reach the inner handler exactly once)", calls, len(cases))
	}
}

// spec: §11.5 line 277 — replay reproduces "the cached response (same
// HTTP status and body)"; preserving every value of a multi-valued
// header keeps Set-Cookie / Vary / WWW-Authenticate round-trips
// faithful. Closes F-11.5.9.
func TestWrap_MultiValueHeadersPreservedOnReplay_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Add("Set-Cookie", "sid=abc; Path=/")
		w.Header().Add("Set-Cookie", "trace=x; Path=/")
		w.Header().Add("Vary", "Accept")
		w.Header().Add("Vary", "Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
		req.Header.Set(HeaderName, "k1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	// Prime the cache.
	if r := send(); r.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", r.Code)
	}
	// Replay; inner must NOT be called again.
	r := send()
	if r.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", r.Code)
	}
	if calls != 1 {
		t.Errorf("inner called %d times, want 1 (second call must replay from cache)", calls)
	}
	cookies := r.Result().Header.Values("Set-Cookie")
	if len(cookies) != 2 || cookies[0] != "sid=abc; Path=/" || cookies[1] != "trace=x; Path=/" {
		t.Errorf("Set-Cookie values: got %v, want both values preserved", cookies)
	}
	varies := r.Result().Header.Values("Vary")
	if len(varies) != 2 || varies[0] != "Accept" || varies[1] != "Authorization" {
		t.Errorf("Vary values: got %v, want both values preserved", varies)
	}
}

// spec: §11.5 line 277 — when the inner handler returns without
// calling WriteHeader, captured.status is 0; the cache row stores 200
// so a future replay never gives back a degenerate 0 status. Closes
// F-11.5.12.
func TestWrap_ZeroStatusCaptureStoredAs200_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No WriteHeader; no Write either.
	})
	store := &stubStore{}
	wrapped := Wrap(inner, store, Options{})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Header.Set(HeaderName, "k1")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("client status = %d, want 200 (flush normalises 0 → 200)", rec.Code)
	}
	if store.puts != 1 {
		t.Fatalf("Put count = %d, want 1", store.puts)
	}
	if store.rec.Response.StatusCode != http.StatusOK {
		t.Errorf("persisted status = %d, want 200 (must not store 0)", store.rec.Response.StatusCode)
	}
}

// matchPathPattern is the path matcher behind Options.AllowedPaths.
// spec: §11.5; F-11.5.7.
func TestMatchPathPattern(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/v1/sessions", "/v1/sessions", true},
		{"/v1/sessions", "/v1/sessions/abc", false},
		{"/v1/sessions/{id}/start", "/v1/sessions/abc/start", true},
		{"/v1/sessions/{id}/start", "/v1/sessions/abc/start/extra", false},
		{"/v1/sessions/{id}/start", "/v1/sessions//start", false}, // empty segment rejected
		{"/v1/sessions/{id}/tool-use/...", "/v1/sessions/abc/tool-use/tc1/approve", true},
		{"/v1/sessions/{id}/tool-use/...", "/v1/sessions/abc/start", false},
		{"/mcp", "/mcp", true},
		{"/mcp", "/mcpx", false},
		{"/", "/", true},
	}
	for _, c := range cases {
		got := matchPathPattern(c.pattern, c.path)
		if got != c.want {
			t.Errorf("matchPathPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// io.Reader for tests that need a body but want to avoid unused-import
// lint when the test file doesn't use io anywhere else.
var _ io.Reader = strings.NewReader("")

// spec: §11.5 line 277 ("without re-executing the operation"). The
// atomic Claim inserts a pending row before the inner handler runs; a
// concurrent retry that arrives mid-execution observes the pending row
// and is rejected with 409 IDEMPOTENCY_KEY_IN_FLIGHT. Closes F-11.5.2.
func TestWrap_AtomicClaim_ConcurrentRetryGetsInFlight_spec_11_5(t *testing.T) {
	gate := make(chan struct{})
	releaseInner := make(chan struct{})
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		gate <- struct{}{}
		<-releaseInner
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sessionId":"sess-1"}`))
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
		req.Header.Set(HeaderName, "race-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- send() }()
	<-gate

	// Second concurrent retry — original is still in flight.
	r2 := send()
	if r2.Code != http.StatusConflict {
		t.Errorf("concurrent retry status = %d, want 409 IDEMPOTENCY_KEY_IN_FLIGHT", r2.Code)
	}
	if r2.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q, want 1", r2.Header().Get("Retry-After"))
	}
	var env map[string]any
	if err := json.NewDecoder(r2.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil || body["code"] != "IDEMPOTENCY_KEY_IN_FLIGHT" || body["category"] != "POLICY" || body["retryable"] != true {
		t.Errorf("envelope: got %v, want IN_FLIGHT/POLICY/retryable", env)
	}

	// Let the first request complete.
	close(releaseInner)
	r1 := <-done
	if r1.Code != http.StatusCreated {
		t.Fatalf("original status = %d, want 201", r1.Code)
	}
	if calls != 1 {
		t.Errorf("inner called %d times, want 1 (concurrent retry must not double-execute)", calls)
	}

	// After completion, a third retry replays the cached response.
	r3 := send()
	if r3.Code != http.StatusCreated {
		t.Errorf("post-completion retry status = %d, want 201 (replay)", r3.Code)
	}
	if calls != 1 {
		t.Errorf("inner called %d times after replay, want 1", calls)
	}
}

// spec: §11.5 line 277 — when the inner handler returns a 5xx, the
// pending row is released so the retry can re-execute against a healthy
// backend. Closes F-11.5.2 (release on 5xx skip path) + F-11.5.3.
func TestWrap_AtomicClaim_5xxReleasesPendingRow_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
		req.Header.Set(HeaderName, "5xx-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	if r := send(); r.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", r.Code)
	}
	// A second retry must re-execute, not see IDEMPOTENCY_KEY_IN_FLIGHT.
	if r := send(); r.Code != http.StatusCreated {
		t.Fatalf("retry status = %d, want 201 (5xx released pending row)", r.Code)
	}
	if calls != 2 {
		t.Errorf("inner called %d times, want 2 (first 5xx, second succeeds)", calls)
	}
}

// spec: §11.5 line 277 — a streamed response (handler calls Flush) is
// not cached; the pending row is released so a retry re-executes.
// Closes F-11.5.10.
func TestWrap_StreamingResponseSkipsCache_spec_11_5(t *testing.T) {
	calls := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: tick-1\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("data: tick-2\n\n"))
	})
	store := NewMemoryStore()
	rm := &recordingMetrics{}
	var buf bytes.Buffer
	wrapped := Wrap(inner, store, Options{Metrics: rm, Logger: log.New(&buf, "", 0)})

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
		req.Header.Set(HeaderName, "stream-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	r := send()
	if r.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", r.Code)
	}
	if !strings.Contains(r.Body.String(), "tick-1") || !strings.Contains(r.Body.String(), "tick-2") {
		t.Errorf("body missing stream output: %q", r.Body.String())
	}
	// The cache must be empty: streamed responses are not safely
	// replayable.
	if _, found, err := store.Get(context.Background(), "acme", "stream-key"); err != nil || found {
		t.Errorf("streamed response must not be cached: found=%v err=%v", found, err)
	}
	if len(rm.skipped) != 1 || rm.skipped[0].reason != "streamed" {
		t.Errorf("skip metric: got %+v, want one streamed row", rm.skipped)
	}
	if !strings.Contains(buf.String(), "skipping cache write for streamed response") {
		t.Errorf("expected WARN log on streamed skip, got %q", buf.String())
	}
	// A retry must re-execute.
	r2 := send()
	if r2.Code != http.StatusOK {
		t.Errorf("retry status = %d, want 200", r2.Code)
	}
	if calls != 2 {
		t.Errorf("inner called %d times, want 2 (streamed responses must not replay)", calls)
	}
}

// spec: §11.5 line 277 — the atomic Claim path remains compatible with
// the existing same-key-different-body 422 contract: after the original
// completes, a same-key retry with a mismatched body sees IDEMPOTENCY_KEY_REUSED.
// Regression guard against the Claim/DetectReuse rewiring. spec: F-11.5.2.
func TestWrap_AtomicClaim_PreservesReuseRejection_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	store := NewMemoryStore()
	wrapped := Wrap(inner, store, Options{})

	send := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(payload))
		req.Header.Set(HeaderName, "reuse-key")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}
	if r := send(`{"a":1}`); r.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", r.Code)
	}
	r := send(`{"a":2}`)
	if r.Code != http.StatusUnprocessableEntity {
		t.Errorf("mismatched body status = %d, want 422", r.Code)
	}
	var env map[string]any
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil || body["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Errorf("envelope: got %v, want IDEMPOTENCY_KEY_REUSED", env)
	}
}

// spec: §11.5 line 277 — MemoryStore.Claim returns claimed=true the
// first time and an existing record (pending or final) on conflict.
// spec: F-11.5.2.
func TestMemoryStore_Claim_spec_11_5(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()

	// First claim wins.
	existing, claimed, err := store.Claim(ctx, "acme", "k1", "hash-1", now)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !claimed {
		t.Errorf("first claim should win, got claimed=%v", claimed)
	}
	if existing.Response.StatusCode != 0 {
		t.Errorf("first claim should not return existing row, got %+v", existing)
	}

	// Concurrent retry observes the pending row.
	existing, claimed, err = store.Claim(ctx, "acme", "k1", "hash-1", now)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if claimed {
		t.Errorf("second claim should not win, got claimed=%v", claimed)
	}
	if existing.Response.StatusCode != 0 {
		t.Errorf("second claim should return pending row (status==0), got %+v", existing)
	}
	if existing.BodyHash != "hash-1" {
		t.Errorf("BodyHash = %q, want hash-1", existing.BodyHash)
	}

	// Release frees the slot.
	if err := store.Release(ctx, "acme", "k1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, claimed, err = store.Claim(ctx, "acme", "k1", "hash-2", now)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if !claimed {
		t.Errorf("claim after release should win, got claimed=%v", claimed)
	}
}

// spec: §11.5 line 277 — an expired pending row is treated as absent so
// a fresh claim wins the slot (stuck claims are reclaimable by TTL).
// spec: F-11.5.2.
func TestMemoryStore_Claim_ExpiredRecordReclaimed_spec_11_5(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	old := time.Now().UTC().Add(-25 * time.Hour) // past the §11.5 24-hour TTL

	// Seed a stale claim directly.
	store.now = func() time.Time { return old }
	_, claimed, err := store.Claim(ctx, "acme", "k1", "hash-old", old)
	if err != nil || !claimed {
		t.Fatalf("seed: claimed=%v err=%v", claimed, err)
	}
	// Now claim with a fresh `now`; expired pending row should yield.
	store.now = func() time.Time { return time.Now().UTC() }
	_, claimed, err = store.Claim(ctx, "acme", "k1", "hash-new", time.Now().UTC())
	if err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	if !claimed {
		t.Errorf("fresh claim should win over expired row, got claimed=%v", claimed)
	}
}

// spec: §11.5 line 277 — Options.MaxBodyBytes governs the cap on the
// request body the middleware buffers; a request larger than the cap
// is rejected with 413 BODY_TOO_LARGE, smaller bodies pass through.
// The flag --idempotency-max-body-bytes raises the operator-tunable
// default. Closes F-11.5.8.
func TestWrap_MaxBodyBytesIsOperatorTunable_spec_11_5(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	store := NewMemoryStore()
	// Operator raises the cap to 16 MiB.
	wrapped := Wrap(inner, store, Options{MaxBodyBytes: 16 << 20})

	// A 2 MiB body (would be rejected by the legacy 1 MiB default)
	// passes through under the raised cap.
	big := strings.Repeat("x", 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(big))
	req.Header.Set(HeaderName, "big-key")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Errorf("raised cap should admit 2 MiB body, got %d", rec.Code)
	}

	// A body just over the configured cap is rejected with 413.
	wrappedTight := Wrap(inner, NewMemoryStore(), Options{MaxBodyBytes: 1024})
	tooBig := strings.Repeat("y", 2048)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(tooBig))
	req2.Header.Set(HeaderName, "k2")
	req2.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec2 := httptest.NewRecorder()
	wrappedTight.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap body status = %d, want 413", rec2.Code)
	}
	var env map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body, _ := env["error"].(map[string]any)
	if body == nil || body["code"] != "BODY_TOO_LARGE" {
		t.Errorf("envelope = %v, want BODY_TOO_LARGE", env)
	}
}

// spec: §11.5 line 277 — the new IDEMPOTENCY_KEY_IN_FLIGHT code is in
// the §15.2.1 classifier with (POLICY, retryable=true). Regression
// guard for the envelope shape on the in-flight gate. spec: F-11.5.2.
func TestErrorClassify_IdempotencyKeyInFlight_spec_15_1(t *testing.T) {
	cat, retryable := classifyForTest("IDEMPOTENCY_KEY_IN_FLIGHT")
	if cat != "POLICY" || !retryable {
		t.Errorf("IDEMPOTENCY_KEY_IN_FLIGHT = (%v, %v), want (POLICY, true)", cat, retryable)
	}
}

// classifyForTest invokes the package's writeError path indirectly by
// asserting on the §15.1 envelope a fail-closed 409 produces. Keeps the
// test independent of the classifier internals while still proving the
// (code → category, retryable) edge holds.
func classifyForTest(code string) (string, bool) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusConflict, code, "test", nil)
	var env map[string]any
	_ = json.NewDecoder(w.Body).Decode(&env)
	body, _ := env["error"].(map[string]any)
	if body == nil {
		return "", false
	}
	retryable, _ := body["retryable"].(bool)
	cat, _ := body["category"].(string)
	return cat, retryable
}
