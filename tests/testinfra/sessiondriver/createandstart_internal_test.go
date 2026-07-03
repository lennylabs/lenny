// SPDX-License-Identifier: MIT

// White-box tier-1 tests for CreateAndStart's terminal-error
// classification. They run in-process against an httptest server, so
// they need neither the Kind cluster nor a warm pool; the goal is to
// pin how CreateAndStart labels a persistent pool-not-ready 503 versus
// any other failure, which the tier-9 live-session tests rely on to
// skip cleanly on a degraded cluster.

package sessiondriver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestDriver builds a Driver wired to baseURL with a short-timeout
// HTTP client. It bypasses New/kind.InstallLenny so the classification
// logic runs without a cluster.
func newTestDriver(baseURL string) *Driver {
	return &Driver{
		baseURL:             baseURL,
		hc:                  &http.Client{Timeout: 5 * time.Second},
		bootstrappedTenants: map[string]struct{}{},
	}
}

// spec: 5.2 (RUNTIME_UNAVAILABLE / WARM_POOL_EXHAUSTED pool-not-ready
// envelope), 15.1 (create-and-start)
//
// A gateway that keeps returning a transient §5.2 pool-not-ready 503 for
// the whole retry window is an environmental warm-pool failure. The
// tier-9 live-session tests distinguish it from a real session-surface
// defect by errors.Is-checking ErrPoolNotReady; a plain error (the
// pre-fix behavior) would make them hard-fail on a degraded cluster
// instead of skipping. This asserts the terminal error wraps the
// sentinel and that the retry loop actually retried before giving up.
func TestCreateAndStartWrapsPoolNotReadyAfterRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"RUNTIME_UNAVAILABLE","message":"Pool echo-pool-sidecar is warming up — no idle pods are available yet"}}`))
	}))
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess, err := d.CreateAndStart(ctx, "acme", EchoRuntimeSidecar)
	if sess != nil {
		t.Fatalf("expected no session on a persistent pool-not-ready 503, got %+v", sess)
	}
	if !errors.Is(err, ErrPoolNotReady) {
		t.Fatalf("expected error to wrap ErrPoolNotReady, got %v", err)
	}
	// The retry loop must have made more than one attempt; a single call
	// would mean the transient 503 was not retried at all.
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected CreateAndStart to retry the transient 503, made %d call(s)", got)
	}
}

// spec: 15.1 (create-and-start), 5.2 (non-pool-not-ready failures)
//
// A 503 whose body does not carry a pool-not-ready code, or a non-503
// status, is not an environmental warm-pool condition. CreateAndStart
// must return a plain error that does NOT wrap ErrPoolNotReady, so the
// live-session tests hard-fail on it (a genuine session-surface defect)
// rather than silently skipping. This pins the negative side of the
// classification so the skip path cannot swallow real failures.
func TestCreateAndStartDoesNotWrapPoolNotReadyOnOtherFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "503 without a pool-not-ready code",
			status: http.StatusServiceUnavailable,
			body:   `{"error":{"code":"INTERNAL","message":"gateway backend error"}}`,
		},
		{
			name:   "500 internal error",
			status: http.StatusInternalServerError,
			body:   `{"error":{"code":"INTERNAL","message":"boom"}}`,
		},
		{
			name:   "403 tenant not active",
			status: http.StatusForbidden,
			body:   `{"error":{"code":"TENANT_NOT_ACTIVE","message":"tenant deleted"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			d := newTestDriver(srv.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			sess, err := d.CreateAndStart(ctx, "acme", EchoRuntimeSidecar)
			if sess != nil {
				t.Fatalf("expected no session on a %d failure, got %+v", tc.status, sess)
			}
			if err == nil {
				t.Fatalf("expected an error on a %d failure", tc.status)
			}
			if errors.Is(err, ErrPoolNotReady) {
				t.Errorf("a %s must not wrap ErrPoolNotReady; a live-session test would wrongly skip it", tc.name)
			}
		})
	}
}
