// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
)

// fireRec sends one request through h and returns the full recorder so
// the §15.1 rate-limit headers can be inspected.
func fireRec(h http.Handler, path, subject string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if subject != "" {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: subject, TenantID: "acme",
		}))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §15.1 lines 1131-1138 — every REST response carries the
// X-RateLimit triplet for the binding scope. F-15.1.7.
func TestRateLimitHeadersOnSuccess_spec_15_1_1131(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 5, Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want 5", got)
	}
	// One request counted ⇒ 4 remaining.
	if got := rr.Header().Get("X-RateLimit-Remaining"); got != "4" {
		t.Errorf("X-RateLimit-Remaining = %q, want 4", got)
	}
	now := fixedClock()
	wantReset := strconv.FormatInt((now.Unix()/60+1)*60, 10)
	if got := rr.Header().Get("X-RateLimit-Reset"); got != wantReset {
		t.Errorf("X-RateLimit-Reset = %q, want %q (next minute boundary)", got, wantReset)
	}
}

// spec: §15.1 line 1134 — remaining decrements with each request in the
// window. F-15.1.7.
func TestRateLimitRemainingDecrements_spec_15_1_1134(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 3, Clock: fixedClock,
	})
	wants := []string{"2", "1", "0"}
	for i, want := range wants {
		rr := fireRec(h, "/v1/sessions", "")
		if got := rr.Header().Get("X-RateLimit-Remaining"); got != want {
			t.Errorf("request %d: X-RateLimit-Remaining = %q, want %q", i+1, got, want)
		}
	}
}

// spec: §15.1 lines 1131-1138 — the binding scope is the one with the
// least headroom (here per-user, not the looser global cap). F-15.1.7.
func TestRateLimitHeadersBindingScope_spec_15_1_1131(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter:          rlcounter.NewMemory(),
		GlobalPerMinute:  100,
		PerUserPerMinute: 3,
		Clock:            fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "alice@acme.com")
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "3" {
		t.Errorf("binding limit = %q, want 3 (per-user, the tighter cap)", got)
	}
	if got := rr.Header().Get("X-RateLimit-Remaining"); got != "2" {
		t.Errorf("binding remaining = %q, want 2", got)
	}
}

// spec: §15.1 lines 1131-1138 — a 429 rejection carries the triplet
// (remaining 0) and Retry-After. F-15.1.7.
func TestRateLimitHeadersOnRejection_spec_15_1_1131(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 1, Clock: fixedClock,
	})
	if rr := fireRec(h, "/v1/sessions", ""); rr.Code != http.StatusNoContent {
		t.Fatalf("first request: status %d, want 204", rr.Code)
	}
	rr := fireRec(h, "/v1/sessions", "")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Errorf("X-RateLimit-Limit = %q, want 1", got)
	}
	if got := rr.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining on 429 = %q, want 0", got)
	}
	if rr.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset missing on 429")
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("Retry-After missing on 429")
	}
}

// spec: §15.1 lines 1131-1138 — with no configured scope the triplet is
// omitted (there is no budget to report). F-15.1.7.
func TestRateLimitHeadersOmittedWhenNoScope_spec_15_1_1131(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("X-RateLimit-Limit = %q, want empty when no scope is configured", got)
	}
}

// spec: §15.1 lines 1131-1138 — an unauthenticated request under only a
// per-user cap counts no scope, so the triplet is omitted. F-15.1.7.
func TestRateLimitHeadersUnauthenticatedNoUserScope_spec_15_1_1131(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), PerUserPerMinute: 5, Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if got := rr.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("X-RateLimit-Limit = %q, want empty (no per-user key on anonymous request)", got)
	}
}

// spec: §15.1 line 1136 — Retry-After is injected on a downstream 503
// that did not set its own. F-15.1.7.
func TestRetryAfterInjectedOn503_spec_15_1_1136(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h := ratelimitmw.Wrap(inner, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 5, Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("Retry-After must be injected on a 503 that lacks one")
	}
	// The triplet still rides along on the 503.
	if rr.Header().Get("X-RateLimit-Limit") != "5" {
		t.Errorf("X-RateLimit-Limit on 503 = %q, want 5", rr.Header().Get("X-RateLimit-Limit"))
	}
}

// spec: §15.1 line 1136 — a handler that set its own Retry-After on a
// 503 keeps it; the middleware does not overwrite. F-15.1.7.
func TestRetryAfterNotOverwrittenOn503_spec_15_1_1136(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	h := ratelimitmw.Wrap(inner, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 5, Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if got := rr.Header().Get("Retry-After"); got != "10" {
		t.Errorf("Retry-After = %q, want 10 (handler's own value preserved)", got)
	}
}

// spec: §15.1 — a 200 from the inner handler does not receive a
// Retry-After (only 429 and 503 do). F-15.1.7.
func TestNoRetryAfterOnSuccess_spec_15_1_1136(t *testing.T) {
	h := ratelimitmw.Wrap(noContent, ratelimitmw.Options{
		Counter: rlcounter.NewMemory(), GlobalPerMinute: 5, Clock: fixedClock,
	})
	rr := fireRec(h, "/v1/sessions", "")
	if got := rr.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After = %q, want empty on a 2xx response", got)
	}
}
