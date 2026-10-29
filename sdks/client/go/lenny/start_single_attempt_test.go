// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// startStub counts /start requests and answers each with respond.
func startStub(t *testing.T, respond func(w http.ResponseWriter)) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sessions/sess_1/start" {
			atomic.AddInt32(&calls, 1)
		}
		respond(w)
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

// TestStartSendsOneRequestOnStartingFailed_spec_15_6 asserts Start does not
// retry a retryable 503 STARTING_FAILED under a retry policy that retries
// other calls: a failed /start leaves the session failed, so a repeat is
// refused. The request count is the discriminating assertion.
//
// spec: §15.6 (Client SDKs), §15.1 (/start), §7.2 (starting → failed)
func TestStartSendsOneRequestOnStartingFailed_spec_15_6(t *testing.T) {
	ts, calls := startStub(t, func(w http.ResponseWriter) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": "STARTING_FAILED", "category": "TRANSIENT",
			"message": "could not place the session on a warm pod", "retryable": true,
		}})
	})
	c, err := New(ts.URL, WithRetryPolicy(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Start(context.Background(), "sess_1")
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.Code != "STARTING_FAILED" {
		t.Fatalf("Start error = %v, want APIError STARTING_FAILED", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("/start requests = %d, want 1", got)
	}
}

// TestStartSendsOneRequestOnDroppedConnection_spec_15_6 asserts Start does
// not retry a transport failure: the client cannot tell whether the gateway
// wrote starting before the connection dropped.
//
// spec: §15.6 (Client SDKs), §15.1 (/start)
func TestStartSendsOneRequestOnDroppedConnection_spec_15_6(t *testing.T) {
	ts, calls := startStub(t, func(w http.ResponseWriter) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer cannot hijack")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	c, err := New(ts.URL, WithRetryPolicy(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Start(context.Background(), "sess_1"); err == nil {
		t.Fatal("Start = nil error, want the transport failure")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("/start requests = %d, want 1", got)
	}
}

// TestFinalizeStillRetriesUnderThePolicy_spec_15_6 asserts the single attempt
// is scoped to Start: another transition keeps the Client's retry policy.
//
// spec: §15.6 (Client SDKs)
func TestFinalizeStillRetriesUnderThePolicy_spec_15_6(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
			"code": "SESSION_CREATION_FAILED", "category": "TRANSIENT", "message": "x", "retryable": true,
		}})
	}))
	defer ts.Close()
	c, err := New(ts.URL, WithRetryPolicy(RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Finalize(context.Background(), "sess_1"); err == nil {
		t.Fatal("Finalize = nil error, want the 503")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("/finalize requests = %d, want 3", got)
	}
}
