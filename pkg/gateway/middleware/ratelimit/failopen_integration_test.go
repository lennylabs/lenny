// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/failopen"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
)

// togglingCounter fails (forcing fail-open) while fail is true and returns
// a healthy count otherwise, so a test can drive the outage/recovery edge.
type togglingCounter struct {
	fail atomic.Bool
	n    atomic.Int64
}

func (c *togglingCounter) Incr(_ context.Context, _ string, _ time.Time) (int, error) {
	if c.fail.Load() {
		return 0, errors.New("ratelimit: counter unavailable")
	}
	return int(c.n.Add(1)), nil
}

// movableClock is a mutable clock shared between a test and the failopen
// controller it builds.
type movableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *movableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *movableClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func fireUser(h http.Handler, subject string) int {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: subject, TenantID: "acme",
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// spec: §12.4 line 222 — during a Redis outage the per-replica per-user
// fail-open ceiling rejects a single user once it reaches the ceiling,
// even though the shared counter is unreachable. F-12.4.9 / F-11.2.6.
func TestFailOpenPerUserCeilingRejects_spec_12_4_222(t *testing.T) {
	clk := &movableClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	ctrl := failopen.NewController(failopen.ControllerConfig{
		Timer:             failopen.NewCumulativeTimer(failopen.CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: "-", Now: clk.now}),
		Backstop:          failopen.NewBackstop(clk.now),
		Replicas:          failopen.NewReplicaCount(),
		UserFraction:      0.25,
		PerReplicaHardCap: 20, // tenant ceiling = min(20/1, 20) = 20; user = 20*0.25 = 5
		Now:               clk.now,
	})
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:            erroringCounter{}, // forces fail-open on every request
		PerTenantPerMinute: 20,
		PerUserPerMinute:   20,
		FailOpenMax:        -1, // disable the per-episode cap so the ceiling is the brake
		Clock:              clk.now,
		FailOpen:           ctrl,
	})
	// The first 5 requests are under the per-user ceiling (5) and admit.
	for i := 1; i <= 5; i++ {
		if code := fireUser(h, "alice"); code != http.StatusNoContent {
			t.Fatalf("request %d: code = %d, want 204", i, code)
		}
	}
	// The 6th crosses the per-user fail-open ceiling.
	if code := fireUser(h, "alice"); code != http.StatusTooManyRequests {
		t.Fatalf("request past the per-user ceiling: code = %d, want 429", code)
	}
	// A different user in the same tenant is unaffected (ceiling is per-user).
	if code := fireUser(h, "bob"); code != http.StatusNoContent {
		t.Fatalf("a second user under its own ceiling: code = %d, want 204", code)
	}
}

// spec: §12.4 line 224 — once cumulative fail-open time exceeds the
// maximum, the replica fails closed for quota: every request is rejected
// until Redis recovers. F-12.4.9 / F-11.2.6.
func TestFailOpenCumulativeFailsClosed_spec_12_4_224(t *testing.T) {
	clk := &movableClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	ctrl := failopen.NewController(failopen.ControllerConfig{
		Timer:    failopen.NewCumulativeTimer(failopen.CumulativeConfig{MaxSeconds: 1 * time.Second, StatePath: "-", Now: clk.now}),
		Backstop: failopen.NewBackstop(clk.now),
		Replicas: failopen.NewReplicaCount(),
		Now:      clk.now,
	})
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:            erroringCounter{},
		PerTenantPerMinute: 1000,
		FailOpenMax:        -1,
		Clock:              clk.now,
		FailOpen:           ctrl,
	})
	// The first request opens the episode and is admitted (cumulative 0).
	if code := fireUser(h, "alice"); code != http.StatusNoContent {
		t.Fatalf("first fail-open request: code = %d, want 204", code)
	}
	// Spend more than the 1s cumulative cap in fail-open mode.
	clk.advance(2 * time.Second)
	if code := fireUser(h, "alice"); code != http.StatusTooManyRequests {
		t.Fatalf("request after cumulative cap exceeded: code = %d, want 429 (fail closed)", code)
	}
}

// spec: §12.4 line 222 — per-user fail-open counters reset on the Redis
// recovery edge so a recovered window starts clean. F-12.4.9.
func TestFailOpenCountersResetOnRecovery_spec_12_4_222(t *testing.T) {
	clk := &movableClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	ctrl := failopen.NewController(failopen.ControllerConfig{
		Timer:             failopen.NewCumulativeTimer(failopen.CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: "-", Now: clk.now}),
		Backstop:          failopen.NewBackstop(clk.now),
		Replicas:          failopen.NewReplicaCount(),
		UserFraction:      0.25,
		PerReplicaHardCap: 20,
		Now:               clk.now,
	})
	// A counter that fails the first burst then recovers.
	fc := &togglingCounter{}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:            fc,
		PerTenantPerMinute: 20,
		FailOpenMax:        -1,
		Clock:              clk.now,
		FailOpen:           ctrl,
	})
	fc.fail.Store(true)
	for i := 0; i < 5; i++ {
		fireUser(h, "alice") // exhaust the per-user ceiling (5)
	}
	if code := fireUser(h, "alice"); code != http.StatusTooManyRequests {
		t.Fatalf("expected the per-user ceiling to bind, got %d", code)
	}
	// Redis recovers: the next successful Incr drives the recovery edge,
	// which resets the per-user backstop counters.
	fc.fail.Store(false)
	if code := fireUser(h, "alice"); code != http.StatusNoContent {
		t.Fatalf("recovered request: code = %d, want 204", code)
	}
	// Outage again: the per-user counter restarts from zero.
	fc.fail.Store(true)
	if code := fireUser(h, "alice"); code != http.StatusNoContent {
		t.Fatalf("post-recovery fail-open request: code = %d, want 204 (counters reset)", code)
	}
}
