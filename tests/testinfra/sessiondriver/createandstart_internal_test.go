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

// spec: 7.1 line 28 (SESSION_CREATION_FAILED atomic-unit fallback), 15.1
// line 1138 (Retry-After)
//
// A freshly-claimed pod whose adapter the gateway cannot yet dial on :50051
// (the pod's network still settling on a loaded cluster) surfaces as a
// retryable SESSION_CREATION_FAILED 503 with a Retry-After header, the same
// transient class as a pool-not-ready churn. The driver must keep retrying
// within its window and succeed once a later attempt lands, so a single
// transient placement hiccup does not fail an e2e session-create.
//
// diagnosis: the retry predicate only matched the pool-not-ready codes, so a
// SESSION_CREATION_FAILED broke the loop on the first attempt and returned an
// error even though the gateway marked it retryable. Against the pre-fix code
// this test fails at the first 503; the fix retries and returns the session
// the later 201 carries.
func TestCreateAndStartRetriesTransientSessionCreationFailed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			// The gateway's §7.1 default atomic-unit fallback for a transient
			// adapter-dial failure: retryable 503 + Retry-After, no reason field.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"SESSION_CREATION_FAILED","message":"claim failed: dial tcp 10.245.1.7:50051: i/o timeout"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sess-1","state":"running","podAssignment":"pod-abc"}`))
	}))
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess, err := d.CreateAndStart(ctx, "acme", EchoRuntimeSidecar)
	if err != nil {
		t.Fatalf("CreateAndStart did not clear a transient SESSION_CREATION_FAILED: %v", err)
	}
	if sess == nil || sess.ID != "sess-1" {
		t.Fatalf("expected the session the later 201 carried, got %+v", sess)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 attempts (two transient 503s then a 201), made %d", got)
	}
}

// spec: 7.1 line 28 (SESSION_CREATION_FAILED), 5.2 (pool-not-ready
// classification)
//
// A SESSION_CREATION_FAILED that never clears within the retry window is not a
// pool-not-ready condition, so the terminal error must NOT wrap ErrPoolNotReady
// (a live-session test hard-fails on it as a genuine session-surface defect
// rather than skipping). This pins the classification boundary the retry
// widening must not blur.
func TestCreateAndStartDoesNotWrapPoolNotReadyOnPersistentSessionCreationFailed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"SESSION_CREATION_FAILED","message":"row_persistence_failed"}}`))
	}))
	defer srv.Close()

	d := newTestDriver(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess, err := d.CreateAndStart(ctx, "acme", EchoRuntimeSidecar)
	if sess != nil {
		t.Fatalf("expected no session on a persistent SESSION_CREATION_FAILED, got %+v", sess)
	}
	if err == nil {
		t.Fatal("expected an error on a persistent SESSION_CREATION_FAILED")
	}
	if errors.Is(err, ErrPoolNotReady) {
		t.Error("a persistent SESSION_CREATION_FAILED must not wrap ErrPoolNotReady; a live-session test would wrongly skip it")
	}
	// The retryable envelope was retried across the window before giving up.
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("expected the retryable 503 to be retried, made %d call(s)", got)
	}
}

// spec: 15.1 line 1138 (Retry-After) — retryAfterBackoff parses the
// delta-seconds Retry-After form, caps a large hint at 10s so a server value
// cannot stall the window past the caller's context, and yields zero for the
// absent or non-numeric (HTTP-date) forms so the caller keeps its linear
// schedule.
func TestRetryAfterBackoff(t *testing.T) {
	cases := []struct {
		header string
		want   time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"-3", 0},
		{"2", 2 * time.Second},
		{"10", 10 * time.Second},
		{"45", 10 * time.Second}, // capped
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0},
	}
	for _, c := range cases {
		if got := retryAfterBackoff(c.header); got != c.want {
			t.Errorf("retryAfterBackoff(%q) = %s, want %s", c.header, got, c.want)
		}
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
