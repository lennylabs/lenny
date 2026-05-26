// SPDX-License-Identifier: MIT

package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/idempotency"
)

// stubStore lets a test inject Get/Put errors.
type stubStore struct {
	getErr error
	putErr error
	rec    idempotency.Record
	found  bool
	puts   int
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
			name:      "INVALID_IDEMPOTENCY_KEY",
			key:       strings.Repeat("x", idempotency.MaxKeyLength+1),
			tenant:    "acme",
			body:      strings.NewReader(`{}`),
			wantCode:  "INVALID_IDEMPOTENCY_KEY",
			wantCat:   "TRANSIENT", // unknown code → classifier fallback
			wantRetry: true,
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
