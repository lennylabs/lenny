// SPDX-License-Identifier: MIT

// Package ratelimit is the §11.1 requests-per-minute admission
// middleware. It increments a global and a per-user request counter
// on every API request and rejects with 429 RATE_LIMITED once a
// scope's count exceeds its configured per-minute limit.
//
// A counter error fails open: §11.1 admission must not block traffic
// on a transient counter outage. The per-runtime and per-pool scopes
// the §11.1 table also names need the request's resolved runtime and
// pool, which are not available at the middleware boundary; they are
// enforced deeper once the pool model is wired.
package ratelimit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

// Options configures the rate-limit middleware.
type Options struct {
	// Counter records and reports per-minute request counts. A nil
	// Counter disables rate limiting (the middleware passes through).
	Counter rlcounter.Counter

	// GlobalPerMinute caps total gateway requests per minute. Zero or
	// less leaves the global scope unlimited.
	GlobalPerMinute int

	// PerUserPerMinute caps one authenticated user's requests per
	// minute. Zero or less leaves the per-user scope unlimited.
	PerUserPerMinute int

	// Clock overrides time.Now for the window computation. Tests inject
	// a fixed clock; production leaves this nil.
	Clock func() time.Time
}

// Wrap returns the §11.1 rate-limit middleware around inner.
func Wrap(inner http.Handler, opts Options) http.Handler {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &middleware{inner: inner, opts: opts, clock: clock}
}

type middleware struct {
	inner http.Handler
	opts  Options
	clock func() time.Time
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.opts.Counter == nil || isInfraPath(r.URL.Path) {
		m.inner.ServeHTTP(w, r)
		return
	}
	now := m.clock()

	if m.opts.GlobalPerMinute > 0 {
		count, err := m.opts.Counter.Incr(r.Context(), "global", now)
		if err == nil && count > m.opts.GlobalPerMinute {
			writeRateLimited(w, "global", m.opts.GlobalPerMinute, now)
			return
		}
	}

	if m.opts.PerUserPerMinute > 0 {
		if key, ok := userKey(r); ok {
			count, err := m.opts.Counter.Incr(r.Context(), key, now)
			if err == nil && count > m.opts.PerUserPerMinute {
				writeRateLimited(w, "user", m.opts.PerUserPerMinute, now)
				return
			}
		}
	}

	m.inner.ServeHTTP(w, r)
}

// userKey returns the per-user counter key for the request, or
// ("", false) when the request carries no authenticated user.
func userKey(r *http.Request) (string, bool) {
	p, ok := authmw.FromContext(r.Context())
	if !ok || p.Subject == "" {
		return "", false
	}
	return "u:" + p.TenantID + ":" + p.Subject, true
}

// isInfraPath reports whether a path is an operational endpoint that
// monitoring scrapes and that the §11.1 admission limits exempt.
func isInfraPath(p string) bool {
	switch p {
	case "/healthz", "/metrics", "/openapi.yaml", "/v1/openapi.json":
		return true
	default:
		return false
	}
}

// writeRateLimited writes the §15.1 429 RATE_LIMITED envelope and a
// Retry-After header pointing at the next window boundary.
func writeRateLimited(w http.ResponseWriter, scope string, limit int, now time.Time) {
	retryAfter := 60 - now.Second()
	if retryAfter <= 0 {
		retryAfter = 1
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":      "RATE_LIMITED",
			"category":  "POLICY",
			"message":   "the " + scope + " request-rate limit was exceeded",
			"retryable": true,
			"details": map[string]any{
				"scope":          scope,
				"limitPerMinute": limit,
			},
		},
	})
}
