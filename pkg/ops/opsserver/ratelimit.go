// SPDX-License-Identifier: MIT

package opsserver

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

// Defaults from §25.4 line 2001 ("Default: 20 requests/second with burst
// of 50, configurable via ops.rateLimiting Helm values").
const (
	DefaultRateLimitRPS   = 20.0
	DefaultRateLimitBurst = 50
)

// rateLimitedTotal is the §25.4 line 2007 counter of requests rejected
// by the per-service-account rate limiter, labelled by the JWT sub
// claim. Registered on the default registry at package init so the
// §16.9 lenny-ops /metrics endpoint exposes it (F-16.8.1).
var rateLimitedTotal *prometheus.CounterVec

func init() {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_ops_rate_limited_total",
		Help: "§25.4: requests rejected by the lenny-ops per-service-account " +
			"rate limiter, labelled by the JWT sub claim.",
	}, []string{"service_account"})
	if err != nil {
		panic(err)
	}
	metrics.MustRegister(prometheus.DefaultRegisterer, c)
	rateLimitedTotal = c
}

// RateLimiter is the §25.4 line 2001 per-service-account token-bucket
// rate limiter. It keys an independent token bucket on the authenticated
// sub claim, so one service account saturating its budget never starves
// another. The limiter is in-process (§25.4 line 2003): with N replicas
// the effective per-account limit is rps*N.
type RateLimiter struct {
	rps   rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*rate.Limiter

	// now is the clock the underlying buckets refill against. Defaults to
	// time.Now; tests override it for deterministic exhaustion.
	now func() time.Time
}

// NewRateLimiter returns a per-sub rate limiter. A non-positive rps or
// burst selects the §25.4 default (20 rps, burst 50).
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	if rps <= 0 {
		rps = DefaultRateLimitRPS
	}
	if burst <= 0 {
		burst = DefaultRateLimitBurst
	}
	return &RateLimiter{
		rps:     rate.Limit(rps),
		burst:   burst,
		buckets: map[string]*rate.Limiter{},
		now:     time.Now,
	}
}

// Snapshot reports the §25.4 line 1611 rateLimits block for sub: the
// current token-bucket balance, the configured rps, and the burst. The
// GET /v1/admin/me endpoint surfaces tokens so an agent can self-pace
// precisely. Reading the balance creates the bucket on first use (so a
// caller that has never been rate-limited still sees a full bucket).
func (rl *RateLimiter) Snapshot(sub string) (tokens, rps float64, burst int) {
	l := rl.limiterFor(sub)
	return l.TokensAt(rl.now()), float64(rl.rps), rl.burst
}

// limiterFor returns the token bucket for sub, creating it on first use.
func (rl *RateLimiter) limiterFor(sub string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.buckets[sub]
	if !ok {
		l = rate.NewLimiter(rl.rps, rl.burst)
		rl.buckets[sub] = l
	}
	return l
}

// retryAfterSeconds is the §25.4 line 2003 Retry-After value: the time
// until the next token, rounded up to whole seconds with a one-second
// floor (a sub-second Retry-After serializes to "0", which clients read
// as "retry immediately" and would defeat the back-pressure).
func (rl *RateLimiter) retryAfterSeconds() int {
	if rl.rps <= 0 {
		return 1
	}
	s := int(math.Ceil(1.0 / float64(rl.rps)))
	if s < 1 {
		return 1
	}
	return s
}

// Wrap applies the per-sub rate limit ahead of inner. A request that
// exceeds its bucket is rejected with §25.2 429 TooManyRequests plus a
// Retry-After header, and increments lenny_ops_rate_limited_total for
// the offending service account. The sub is read from the authenticated
// principal; an unauthenticated request (no principal) shares the
// "anonymous" bucket, which only the dev path can reach.
//
// spec: §25.4 lines 2001-2003.
func (rl *RateLimiter) Wrap(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := "anonymous"
		if p, ok := authmw.FromContext(r.Context()); ok && p.Subject != "" {
			sub = p.Subject
		}
		if !rl.limiterFor(sub).AllowN(rl.now(), 1) {
			rateLimitedTotal.WithLabelValues(sub).Inc()
			w.Header().Set("Retry-After", strconv.Itoa(rl.retryAfterSeconds()))
			conventions.WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED",
				conventions.CategoryTransient,
				"per-service-account rate limit exceeded; retry after the Retry-After interval")
			return
		}
		inner.ServeHTTP(w, r)
	})
}
