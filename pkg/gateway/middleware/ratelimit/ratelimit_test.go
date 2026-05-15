// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	ratelimitmw "github.com/lennylabs/lenny/pkg/gateway/middleware/ratelimit"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

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
