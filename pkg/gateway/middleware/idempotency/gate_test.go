// SPDX-License-Identifier: MIT

package idempotency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// spec: 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send) Forwarding rules 6 and 7
func TestWrapDoesNotStoreAForwardRefusal_spec_11_5(t *testing.T) {
	var calls atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		DoNotStore(r.Context())
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"TARGET_NOT_READY"}}`))
	})
	store := NewMemoryStore()
	metrics := &recordingMetrics{}
	h := Wrap(inner, store, Options{Metrics: metrics})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/resume", strings.NewReader("{}"))
		req.Header.Set(HeaderName, "k-1")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusConflict {
			t.Fatalf("attempt %d status = %d, want 409", i, rr.Code)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("handler ran %d times, want 2: a same-key retry after a refusal runs again", n)
	}
	if _, ok, _ := store.Get(context.Background(), "acme", "k-1"); ok {
		t.Error("the refusal left a cache row")
	}
	if len(metrics.skipped) != 2 || metrics.skipped[0].reason != SkipReasonForwardRefused {
		t.Errorf("skip metrics = %+v, want two forward_refused skips", metrics.skipped)
	}
}

// spec: 11.5 (Idempotency) Mechanism
func TestWrapStillStoresAnUnmarked409_spec_11_5(t *testing.T) {
	var calls atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"TARGET_NOT_READY"}}`))
	})
	h := Wrap(inner, NewMemoryStore(), Options{})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s/start", strings.NewReader("{}"))
		req.Header.Set(HeaderName, "k-2")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("handler ran %d times, want 1: a coordinator's own 409 is stored and replayed", n)
	}
}

// spec: 11.5 (Idempotency) Mechanism
func TestGateIsInertWithoutWithGate_spec_11_5(t *testing.T) {
	ctx := context.Background()
	DoNotStore(ctx)
	if NotStored(ctx) {
		t.Error("NotStored is true on a context with no gate")
	}
	gated := WithGate(ctx)
	if NotStored(gated) {
		t.Error("a fresh gate reads as set")
	}
	DoNotStore(gated)
	if !NotStored(gated) {
		t.Error("DoNotStore did not set the gate")
	}
}
