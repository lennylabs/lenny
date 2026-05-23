// SPDX-License-Identifier: MIT

package loadctl

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig caps how fast clients can hit the write-heavy and
// runner-callback endpoints. Three independent buckets:
//
//   - RunCreatePerMinute caps POST /api/v1/runs. Zero or negative
//     leaves the bucket unlimited.
//   - ProgressPerSecond caps POST /api/v1/progress. Progress posts
//     are the highest-volume runner callback; a misconfigured runner
//     can otherwise saturate the fsync-per-event sink. Zero leaves
//     the bucket unlimited.
//   - AckPerSecond caps POST /api/v1/ack. Acks are bounded by the
//     scenario count of in-flight runs, so a permissive default
//     suffices.
//
// Limits apply per source identifier. The identifier is the bearer
// token if one is supplied, otherwise the request's RemoteAddr.
type RateLimitConfig struct {
	RunCreatePerMinute int
	ProgressPerSecond  int
	AckPerSecond       int
}

// rateLimiter holds per-route, per-source token buckets.
type rateLimiter struct {
	cfg RateLimitConfig

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	return &rateLimiter{cfg: cfg, buckets: map[string]*rate.Limiter{}}
}

func (l *rateLimiter) allow(route, source string, lim rate.Limit, burst int) bool {
	if lim <= 0 {
		return true
	}
	key := route + "|" + source
	l.mu.Lock()
	r, ok := l.buckets[key]
	if !ok {
		r = rate.NewLimiter(lim, burst)
		l.buckets[key] = r
	}
	l.mu.Unlock()
	return r.Allow()
}

// rateLimitMiddleware returns a middleware that enforces the
// configured rate limits on the protected routes. Routes outside the
// protected set are passed through unconditionally.
func rateLimitMiddleware(cfg RateLimitConfig) func(http.Handler) http.Handler {
	if cfg == (RateLimitConfig{}) {
		// Fast-path: empty config, no middleware overhead.
		return func(next http.Handler) http.Handler { return next }
	}
	l := newRateLimiter(cfg)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var lim rate.Limit
			var burst int
			route := ""
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
				lim = rate.Limit(float64(cfg.RunCreatePerMinute) / 60.0)
				burst = max(1, cfg.RunCreatePerMinute)
				route = "runs"
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/progress":
				lim = rate.Limit(cfg.ProgressPerSecond)
				burst = max(1, cfg.ProgressPerSecond*2)
				route = "progress"
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/ack":
				lim = rate.Limit(cfg.AckPerSecond)
				burst = max(1, cfg.AckPerSecond*2)
				route = "ack"
			}
			if route != "" {
				if !l.allow(route, sourceKey(r), lim, burst) {
					w.Header().Set("Retry-After", "1")
					writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded for "+route)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// sourceKey identifies the caller for rate-limit bucketing: the
// bearer token if present, otherwise the connection's remote IP
// (stripped of the port so reconnects share a bucket). Tokens take
// precedence so well-known callers (the loadrunner pool) bucket
// together rather than per-pod-IP.
func sourceKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return "tok:" + strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Limit is exported so callers can express the rate in human terms.
// (We re-export rate.Limit so callers don't import golang.org/x/time
// just to construct a RateLimitConfig.)
type Limit = rate.Limit

var _ = time.Second // keep import for the public Limit alias's intent
