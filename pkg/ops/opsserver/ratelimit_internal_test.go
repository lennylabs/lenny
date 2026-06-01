// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// withSub returns a request whose context carries a §10.2 principal with
// the given sub, the way the auth middleware would after verifying a
// bearer.
func withSub(sub string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{Subject: sub})
	return req.WithContext(ctx)
}

// spec: §25.4 lines 2001-2003 — the per-service-account token bucket
// admits up to `burst` requests, then rejects with 429 + Retry-After and
// increments lenny_ops_rate_limited_total. The clock is frozen so no
// token refills between calls.
func TestRateLimiterExhaustsBucket_spec_25_4_2001(t *testing.T) {
	rl := NewRateLimiter(20, 2)
	frozen := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return frozen }

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rl.Wrap(ok)

	before := testutil.ToFloat64(rateLimitedTotal.WithLabelValues("alice@acme.com"))

	// burst=2 -> first two pass, third is limited.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, withSub("alice@acme.com"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withSub("alice@acme.com"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if ra := rec.Header().Get("Retry-After"); ra != "1" {
		t.Errorf("Retry-After = %q, want \"1\"", ra)
	}
	if got := testutil.ToFloat64(rateLimitedTotal.WithLabelValues("alice@acme.com")) - before; got != 1 {
		t.Errorf("lenny_ops_rate_limited_total delta = %v, want 1", got)
	}
}

// spec: §25.4 line 2001 — buckets are keyed per service account, so one
// account exhausting its budget does not throttle another.
func TestRateLimiterPerSubIsolation_spec_25_4_2001(t *testing.T) {
	rl := NewRateLimiter(20, 1)
	frozen := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return frozen }

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rl.Wrap(ok)

	// Exhaust alice's single-token bucket.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, withSub("alice@acme.com"))
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("alice second request: status = %d, want 429", rec.Code)
		}
	}
	// bob's bucket is untouched.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withSub("bob@acme.com"))
	if rec.Code != http.StatusOK {
		t.Fatalf("bob request: status = %d, want 200 (independent bucket)", rec.Code)
	}
}

// spec: §25.4 line 2003 — a refill of the clock restores capacity. With
// rps=20 a 100ms advance refills ~2 tokens, so the next request passes.
func TestRateLimiterRefillsOverTime_spec_25_4_2003(t *testing.T) {
	rl := NewRateLimiter(20, 1)
	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rl.Wrap(ok)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withSub("alice@acme.com")) // consumes the single token
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withSub("alice@acme.com")) // empty bucket
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429", rec.Code)
	}
	now = now.Add(200 * time.Millisecond) // refill > 1 token at 20 rps
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withSub("alice@acme.com"))
	if rec.Code != http.StatusOK {
		t.Fatalf("after refill: status = %d, want 200", rec.Code)
	}
}

// NewRateLimiter clamps non-positive parameters to the §25.4 defaults.
func TestNewRateLimiterDefaults_spec_25_4_2001(t *testing.T) {
	rl := NewRateLimiter(0, 0)
	if float64(rl.rps) != DefaultRateLimitRPS {
		t.Errorf("rps = %v, want default %v", float64(rl.rps), DefaultRateLimitRPS)
	}
	if rl.burst != DefaultRateLimitBurst {
		t.Errorf("burst = %d, want default %d", rl.burst, DefaultRateLimitBurst)
	}
}
