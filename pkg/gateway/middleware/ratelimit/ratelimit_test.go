// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

// recordingMetrics is a test double that captures every Metrics call.
// spec: §11.1 line 7; §16.5 RateLimitDegraded.
type recordingMetrics struct {
	mu              sync.Mutex
	rejected        map[string]int
	failopenSet     []bool
	counterFailures int
}

func (rm *recordingMetrics) IncRateLimitRejected(scope string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.rejected == nil {
		rm.rejected = map[string]int{}
	}
	rm.rejected[scope]++
}

func (rm *recordingMetrics) SetRateLimitFailopenActive(active bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.failopenSet = append(rm.failopenSet, active)
}

func (rm *recordingMetrics) IncRateLimitCounterFailure() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.counterFailures++
}

func (rm *recordingMetrics) snapshot() (map[string]int, []bool, int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	cp := make(map[string]int, len(rm.rejected))
	for k, v := range rm.rejected {
		cp[k] = v
	}
	gauge := append([]bool(nil), rm.failopenSet...)
	return cp, gauge, rm.counterFailures
}

// spec: §11.1 requests-per-minute rate limiting.

// fixedClock pins the rate-limit window so every request in a test
// lands in the same minute.
func fixedClock() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

// noContent is the inner handler — a request that reaches it returns 204.
var noContent = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
})

// fire sends one request through h, optionally as user subject (empty
// subject means no authenticated principal), and returns the status.
func fire(h http.Handler, path, subject string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if subject != "" {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: subject, TenantID: "acme",
		}))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

func TestUnderGlobalLimitPasses(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 5, Clock: fixedClock,
	})
	for i := 1; i <= 5; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Errorf("request %d: status %d, want 204", i, code)
		}
	}
}

func TestOverGlobalLimitRejected(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 3, Clock: fixedClock,
	})
	for i := 1; i <= 3; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Fatalf("request %d under limit: status %d, want 204", i, code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/sessions", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: status %d, want 429", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "RATE_LIMITED") {
		t.Errorf("rejection should carry RATE_LIMITED: %s", rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a 429 rate-limit response must carry a Retry-After header")
	}
}

func TestOverPerUserLimitRejected(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), PerUserPerMinute: 2, Clock: fixedClock,
	})
	for i := 1; i <= 2; i++ {
		if code := fire(h, "/v1/sessions", "alice@acme.com"); code != http.StatusNoContent {
			t.Fatalf("request %d under limit: status %d, want 204", i, code)
		}
	}
	if code := fire(h, "/v1/sessions", "alice@acme.com"); code != http.StatusTooManyRequests {
		t.Errorf("3rd request: status %d, want 429", code)
	}
}

func TestPerUserLimitIsPerUser(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), PerUserPerMinute: 2, Clock: fixedClock,
	})
	// alice exhausts her allowance.
	fire(h, "/v1/sessions", "alice@acme.com")
	fire(h, "/v1/sessions", "alice@acme.com")
	// bob's first request is unaffected.
	if code := fire(h, "/v1/sessions", "bob@acme.com"); code != http.StatusNoContent {
		t.Errorf("bob's request: status %d, want 204 (per-user limits are isolated)", code)
	}
}

func TestNilCounterPassesThrough(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{GlobalPerMinute: 1, Clock: fixedClock})
	for i := 1; i <= 10; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Fatalf("request %d with no counter: status %d, want 204", i, code)
		}
	}
}

func TestInfraPathsExempt(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 1, Clock: fixedClock,
	})
	for i := 1; i <= 5; i++ {
		if code := fire(h, "/healthz", ""); code != http.StatusNoContent {
			t.Errorf("/healthz request %d: status %d, want 204 (infra paths are exempt)", i, code)
		}
	}
}

// erroringCounter always fails, exercising the §11.1 fail-open path.
type erroringCounter struct{}

func (erroringCounter) Incr(context.Context, string, time.Time) (int, error) {
	return 0, errors.New("ratelimit: counter unavailable")
}

func TestCounterErrorFailsOpen(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: erroringCounter{}, GlobalPerMinute: 1, Clock: fixedClock,
	})
	for i := 1; i <= 10; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Fatalf("request %d during a counter outage: status %d, want 204 (fail open)", i, code)
		}
	}
}

// TestGlobalRejectionEmitsCounter_spec_11_1 — §11.1 line 7 admission
// rejection must increment lenny_rate_limit_rejected_total{scope}.
func TestGlobalRejectionEmitsCounter_spec_11_1(t *testing.T) {
	rm := &recordingMetrics{}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 2, Clock: fixedClock, Metrics: rm,
	})
	// Two under-limit requests must not bump the rejection counter.
	fire(h, "/v1/sessions", "")
	fire(h, "/v1/sessions", "")
	if code := fire(h, "/v1/sessions", ""); code != http.StatusTooManyRequests {
		t.Fatalf("over-limit: status %d, want 429", code)
	}
	rejected, _, _ := rm.snapshot()
	if rejected["global"] != 1 {
		t.Errorf("global rejections = %d, want 1; full=%v", rejected["global"], rejected)
	}
	if rejected["user"] != 0 {
		t.Errorf("user rejections = %d, want 0 (global only); full=%v", rejected["user"], rejected)
	}
}

// TestPerUserRejectionEmitsCounter_spec_11_1 — the user-scope 429 must
// attribute the rejection to scope="user" so operators can split global
// vs per-user pressure.
func TestPerUserRejectionEmitsCounter_spec_11_1(t *testing.T) {
	rm := &recordingMetrics{}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), PerUserPerMinute: 1, Clock: fixedClock, Metrics: rm,
	})
	fire(h, "/v1/sessions", "alice@acme.com")
	if code := fire(h, "/v1/sessions", "alice@acme.com"); code != http.StatusTooManyRequests {
		t.Fatalf("over-limit: status %d, want 429", code)
	}
	rejected, _, _ := rm.snapshot()
	if rejected["user"] != 1 {
		t.Errorf("user rejections = %d, want 1; full=%v", rejected["user"], rejected)
	}
}

// TestCounterErrorFlipsFailopen_spec_11_1 — §16.5 RateLimitDegraded
// reads `lenny_rate_limit_failopen_active == 1`, so a counter outage
// must flip the gauge to true and bump the counter-failure counter.
func TestCounterErrorFlipsFailopen_spec_11_1(t *testing.T) {
	rm := &recordingMetrics{}
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: erroringCounter{}, GlobalPerMinute: 1, Clock: fixedClock, Metrics: rm, Logger: logger,
	})
	// First failure must flip the gauge and log.
	fire(h, "/v1/sessions", "")
	_, gauge, failures := rm.snapshot()
	if failures != 1 {
		t.Errorf("counter failures = %d after first error, want 1", failures)
	}
	if len(gauge) != 1 || !gauge[0] {
		t.Errorf("failopen gauge sequence = %v, want [true] on entry", gauge)
	}
	if !strings.Contains(buf.String(), "ratelimit: counter unavailable") {
		t.Errorf("expected fail-open log, got %q", buf.String())
	}
	// A second failure must bump the counter but not the gauge — the
	// edge has already fired, the gauge stays pinned at true.
	fire(h, "/v1/sessions", "")
	_, gauge, failures = rm.snapshot()
	if failures != 2 {
		t.Errorf("counter failures = %d after second error, want 2", failures)
	}
	if len(gauge) != 1 {
		t.Errorf("failopen gauge sequence = %v, want still [true] (edge only)", gauge)
	}
}

// recoveringCounter fails the first N Incrs, then succeeds for ever.
// spec: §16.5 RateLimitDegraded recovery edge.
type recoveringCounter struct{ remaining atomic.Int32 }

func (rc *recoveringCounter) Incr(_ context.Context, key string, _ time.Time) (int, error) {
	_ = key
	if rc.remaining.Add(-1) >= 0 {
		return 0, errors.New("ratelimit: degraded")
	}
	return 1, nil
}

// TestCounterRecoveryClearsFailopen_spec_16_5 — once Incr succeeds
// after a degraded window the gauge must clear back to 0 so the
// RateLimitDegraded alert resolves.
func TestCounterRecoveryClearsFailopen_spec_16_5(t *testing.T) {
	rm := &recordingMetrics{}
	rc := &recoveringCounter{}
	rc.remaining.Store(2)
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rc, GlobalPerMinute: 5, Clock: fixedClock, Metrics: rm,
	})
	fire(h, "/v1/sessions", "")
	fire(h, "/v1/sessions", "")
	// The next request lands on a successful Incr — recovery edge.
	fire(h, "/v1/sessions", "")
	_, gauge, _ := rm.snapshot()
	if len(gauge) != 2 || gauge[0] != true || gauge[1] != false {
		t.Errorf("failopen gauge sequence = %v, want [true,false]", gauge)
	}
}

// movingClock is a test clock advanced one second per Now() call so a
// fail-open episode can cross the cap deterministically.
type movingClock struct {
	mu    sync.Mutex
	cur   time.Time
	step  time.Duration
}

func (c *movingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.cur
	c.cur = c.cur.Add(c.step)
	return t
}

// TestDefaultFailOpenMaxIsSixtySeconds_spec_11_3_222 pins the §11.3
// line 222 / §12.4 line 220 default to 60s. F-11.3.22.
func TestDefaultFailOpenMaxIsSixtySeconds_spec_11_3_222(t *testing.T) {
	if ratelimitmw.DefaultFailOpenMaxSeconds != 60*time.Second {
		t.Errorf("DefaultFailOpenMaxSeconds = %s, want 60s per §11.3 line 222 / §12.4 line 220",
			ratelimitmw.DefaultFailOpenMaxSeconds)
	}
}

// TestFailOpenEpisodeBoundedFailsClosed_spec_11_3_222 proves a single
// fail-open episode that crosses the cap switches to fail-closed
// (rejecting the request with 429 RATE_LIMITED). The §15.1 envelope's
// `details.failOpenMaxSeconds` carries the configured cap so clients
// can attribute the rejection. F-11.3.22.
func TestFailOpenEpisodeBoundedFailsClosed_spec_11_3_222(t *testing.T) {
	clk := &movingClock{cur: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), step: time.Second}
	rm := &recordingMetrics{}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:         erroringCounter{},
		GlobalPerMinute: 1,
		Clock:           clk.Now,
		Metrics:         rm,
		FailOpenMax:     2 * time.Second,
	})
	// First three requests at t=0,1,2 — each has age ∈ {0,1,2} which is
	// ≤ the 2s cap. The cap is strictly greater-than so all three admit.
	for i := 1; i <= 3; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Fatalf("request %d within fail-open cap: status %d, want 204", i, code)
		}
	}
	// At t=3, episode age = 3s > 2s cap → fail-closed.
	if code := fire(h, "/v1/sessions", ""); code != http.StatusTooManyRequests {
		t.Fatalf("request beyond fail-open cap: status %d, want 429", code)
	}
	rejected, _, _ := rm.snapshot()
	if rejected["failopen_exceeded"] != 1 {
		t.Errorf("failopen_exceeded rejections = %d, want 1; full=%v", rejected["failopen_exceeded"], rejected)
	}
}

// TestFailOpenEpisodeResetsOnRecovery_spec_11_3_222 proves the
// per-episode timer clears when the counter recovers, so a later
// outage starts a fresh window rather than carrying old time forward.
// F-11.3.22.
func TestFailOpenEpisodeResetsOnRecovery_spec_11_3_222(t *testing.T) {
	clk := &movingClock{cur: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), step: time.Second}
	// Counter that fails for the first two calls, recovers on the third,
	// then fails for the next two calls so we observe two episodes.
	flap := &flappingCounter{pattern: []bool{false, false, true, false, false}}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:         flap,
		GlobalPerMinute: 5,
		Clock:           clk.Now,
		FailOpenMax:     3 * time.Second,
	})
	// Two failures (t=0,1): fail-open admits.
	fire(h, "/v1/sessions", "")
	fire(h, "/v1/sessions", "")
	// Recovery (t=2): timer clears.
	fire(h, "/v1/sessions", "")
	// Two more failures (t=3,4): start a fresh episode.
	fire(h, "/v1/sessions", "")
	// The episode age here is t4-t3 = 1, which is well within the 3s
	// cap; the request must be admitted by fail-open.
	if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
		t.Errorf("fresh fail-open episode after recovery: status %d, want 204", code)
	}
}

// TestFailOpenCapNegativeDisablesCheck_spec_11_3_222 proves a negative
// override disables the cap entirely — the legacy unbounded fail-open
// behaviour that earlier tests rely on. F-11.3.22.
func TestFailOpenCapNegativeDisablesCheck_spec_11_3_222(t *testing.T) {
	clk := &movingClock{cur: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), step: time.Minute}
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:         erroringCounter{},
		GlobalPerMinute: 1,
		Clock:           clk.Now,
		FailOpenMax:     -1,
	})
	// 10 minutes pass between requests — uncapped behaviour admits them.
	for i := 1; i <= 5; i++ {
		if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
			t.Fatalf("request %d with cap disabled: status %d, want 204", i, code)
		}
	}
}

// flappingCounter rotates through a boolean pattern: true = success,
// false = error.
type flappingCounter struct {
	mu      sync.Mutex
	pattern []bool
	at      int
}

func (f *flappingCounter) Incr(_ context.Context, _ string, _ time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.at >= len(f.pattern) {
		return 1, nil // ride out: succeed after the pattern ends.
	}
	ok := f.pattern[f.at]
	f.at++
	if !ok {
		return 0, errors.New("ratelimit: degraded")
	}
	return 1, nil
}

// TestNoMetricsIsSafe_spec_11_1 — Metrics is optional; a nil interface
// must not panic on any rejection or fail-open transition.
func TestNoMetricsIsSafe_spec_11_1(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 1, Clock: fixedClock,
	})
	fire(h, "/v1/sessions", "")
	if code := fire(h, "/v1/sessions", ""); code != http.StatusTooManyRequests {
		t.Errorf("over-limit with nil metrics: status %d, want 429", code)
	}
	h = ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: erroringCounter{}, GlobalPerMinute: 1, Clock: fixedClock,
	})
	if code := fire(h, "/v1/sessions", ""); code != http.StatusNoContent {
		t.Errorf("fail-open with nil metrics: status %d, want 204", code)
	}
}
