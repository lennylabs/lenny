// SPDX-License-Identifier: MIT

// Package ratelimit is the §11.1 requests-per-minute admission
// middleware. It increments a global and a per-user request counter
// on every API request and rejects with 429 RATE_LIMITED once a
// scope's count exceeds its configured per-minute limit.
//
// A counter error fails open: §11.1 admission must not block traffic
// on a transient counter outage. The fail-open path emits the
// lenny_rate_limit_failopen_active gauge that the §16.5
// RateLimitDegraded alert reads and increments
// lenny_rate_limit_counter_failure_total so an outage is observable
// even before the alert's persistence window. The per-runtime and
// per-pool scopes the §11.1 table also names need the request's
// resolved runtime and pool, which are not available at the
// middleware boundary; they are enforced deeper once the pool model
// is wired.
package ratelimit

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

// DefaultFailOpenMaxSeconds is the §11.3 line 222 /
// §12.4 line 220 default for `rateLimitFailOpenMaxSeconds`: 60 s. Once
// the gateway has been in fail-open mode for this long, the
// middleware switches to fail-closed and rejects requests with 429
// until the counter recovers. spec: §11.3 line 222; §12.4 line 220.
const DefaultFailOpenMaxSeconds = 60 * time.Second

// Metrics is the subset of gatewaymetrics.Metrics the §11.1 ratelimit
// middleware emits. A nil Metrics passes every recorder call through
// to a no-op so the middleware remains usable in tests that do not
// register a registry. spec: §11.1 line 7; §16.5 RateLimitDegraded.
type Metrics interface {
	IncRateLimitRejected(scope string)
	SetRateLimitFailopenActive(active bool)
	IncRateLimitCounterFailure()
}

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

	// Metrics receives §11.1 rejection counters and the fail-open
	// gauge flip. Nil leaves observability disabled — the middleware
	// still enforces and fails open identically. spec: §11.1 line 7.
	Metrics Metrics

	// Logger receives a structured WARN line on each counter error so
	// a Redis outage is visible even when no rejection ever fires.
	// Nil uses the default log package. spec: §11.1 line 7 fail-open
	// observability.
	Logger *log.Logger

	// FailOpenMax is the §11.3 line 222 /
	// §12.4 line 220 cap on a single fail-open episode. A request that
	// arrives after the cap is rejected with 429 / RATE_LIMITED so a
	// sustained Redis outage cannot keep the gateway fail-open
	// indefinitely. The cap is reset on the recovery edge (the next
	// successful Incr). Zero selects DefaultFailOpenMaxSeconds; a
	// negative value disables the cap (the legacy unbounded behaviour
	// preserved for tests). spec: §11.3 line 222; §12.4 line 220.
	FailOpenMax time.Duration
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
	// failopen is the per-replica fail-open state. We mirror it onto
	// the metrics gauge so the §16.5 RateLimitDegraded alert reflects
	// the live state without depending on metrics scrape ordering.
	// Storing the latched bool here lets repeated outages skip the
	// gauge write (a constant 1 reads identically), and lets the
	// recovery edge clear back to 0 the next time Incr succeeds.
	// spec: §16.5 RateLimitDegraded; §11.1 line 7.
	failopen atomic.Bool

	// failOpenSinceMu guards failOpenSince so the per-episode bound is
	// observed consistently across concurrent requests. The pointer
	// shape allows the recovery edge to clear it back to nil. spec:
	// §11.3 line 222; §12.4 line 220. F-11.3.22.
	failOpenSinceMu sync.Mutex
	failOpenSince   time.Time
}

// effectiveFailOpenMax returns the §11.3 line 222 cap with defaulting.
// A negative override disables the cap so existing tier-1 fail-open
// tests that exercise the legacy unbounded path keep their wiring.
// spec: §11.3 line 222; §12.4 line 220.
func (m *middleware) effectiveFailOpenMax() time.Duration {
	if m.opts.FailOpenMax < 0 {
		return 0
	}
	if m.opts.FailOpenMax == 0 {
		return DefaultFailOpenMaxSeconds
	}
	return m.opts.FailOpenMax
}

// failOpenEpisodeExpired reports whether the current fail-open
// episode has exceeded the configured cap as of `now`. A non-positive
// effective cap disables the check (legacy unbounded behaviour). The
// caller MUST already have observed a counter error this request.
// spec: §11.3 line 222; §12.4 line 220. F-11.3.22.
func (m *middleware) failOpenEpisodeExpired(now time.Time) bool {
	cap := m.effectiveFailOpenMax()
	if cap <= 0 {
		return false
	}
	m.failOpenSinceMu.Lock()
	since := m.failOpenSince
	m.failOpenSinceMu.Unlock()
	if since.IsZero() {
		return false
	}
	return now.Sub(since) > cap
}

// noteFailOpenEpisode records the start time of the current fail-open
// episode if one is not already in flight. spec: §11.3 line 222;
// §12.4 line 220. F-11.3.22.
func (m *middleware) noteFailOpenEpisode(now time.Time) {
	m.failOpenSinceMu.Lock()
	defer m.failOpenSinceMu.Unlock()
	if m.failOpenSince.IsZero() {
		m.failOpenSince = now
	}
}

// clearFailOpenEpisode is invoked on the recovery edge so the next
// outage starts a fresh per-episode timer. spec: §11.3 line 222;
// §12.4 line 220. F-11.3.22.
func (m *middleware) clearFailOpenEpisode() {
	m.failOpenSinceMu.Lock()
	m.failOpenSince = time.Time{}
	m.failOpenSinceMu.Unlock()
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.opts.Counter == nil || isInfraPath(r.URL.Path) {
		m.inner.ServeHTTP(w, r)
		return
	}
	now := m.clock()

	// counterFailed tracks whether ANY scope had to fail-open this
	// request. We defer the per-episode cap evaluation to the end so the
	// metric/log side-effects fire on the very first error AND the cap
	// applies even when only the per-user scope sees the error. spec:
	// §11.3 line 222; §12.4 line 220. F-11.3.22.
	counterFailed := false

	if m.opts.GlobalPerMinute > 0 {
		count, err := m.opts.Counter.Incr(r.Context(), "global", now)
		if err != nil {
			counterFailed = true
			m.onCounterError("global", err, now)
		} else {
			m.onCounterSuccess()
			if count > m.opts.GlobalPerMinute {
				m.writeRateLimited(w, "global", m.opts.GlobalPerMinute, now)
				return
			}
		}
	}

	if m.opts.PerUserPerMinute > 0 {
		if key, ok := userKey(r); ok {
			count, err := m.opts.Counter.Incr(r.Context(), key, now)
			if err != nil {
				counterFailed = true
				m.onCounterError("user", err, now)
			} else {
				m.onCounterSuccess()
				if count > m.opts.PerUserPerMinute {
					m.writeRateLimited(w, "user", m.opts.PerUserPerMinute, now)
					return
				}
			}
		}
	}

	// spec: §11.3 line 222; §12.4 line 220 — after the cumulative
	// fail-open episode exceeds the cap, switch to fail-closed: a
	// sustained Redis outage cannot keep the gateway in unbounded
	// allow-all mode. The decision uses `now` captured at the top of the
	// handler so the cap is measured against the request's arrival.
	// F-11.3.22.
	if counterFailed && m.failOpenEpisodeExpired(now) {
		m.writeFailOpenExceeded(w, now)
		return
	}

	m.inner.ServeHTTP(w, r)
}

// onCounterError emits the §11.1 fail-open observability triplet — a
// structured WARN log, the failopen-active gauge flip, and the
// counter-failure counter bump — so a silent counter outage no longer
// disables ratelimit enforcement without operator visibility. It also
// stamps the §11.3 per-episode fail-open start time on the leading
// edge so the cumulative cap (F-11.3.22) can fire downstream.
// spec: §11.1 line 7; §11.3 line 222.
func (m *middleware) onCounterError(scope string, err error, now time.Time) {
	if m.opts.Metrics != nil {
		m.opts.Metrics.IncRateLimitCounterFailure()
	}
	if !m.failopen.Swap(true) {
		// Edge: transitioning from healthy → degraded. Log once on
		// entry (subsequent errors still bump the counter) and flip
		// the gauge so the §16.5 alert can fire.
		m.logger().Printf("ratelimit: counter unavailable scope=%q err=%q fail_open=true", scope, err.Error())
		if m.opts.Metrics != nil {
			m.opts.Metrics.SetRateLimitFailopenActive(true)
		}
	}
	m.noteFailOpenEpisode(now)
}

// onCounterSuccess clears the fail-open state on the recovery edge so
// the §16.5 RateLimitDegraded alert resolves once Redis is back and the
// next outage starts a fresh per-episode timer (F-11.3.22).
// spec: §16.5 RateLimitDegraded; §11.3 line 222.
func (m *middleware) onCounterSuccess() {
	if !m.failopen.Swap(false) {
		// Already healthy; the gauge is already 0.
		return
	}
	m.logger().Printf("ratelimit: counter recovered, fail_open=false")
	if m.opts.Metrics != nil {
		m.opts.Metrics.SetRateLimitFailopenActive(false)
	}
	m.clearFailOpenEpisode()
}

// writeFailOpenExceeded writes the §11.3 line 222 / §12.4 line 220
// fail-closed response when a single fail-open episode has run past
// the configured cap. It uses the §15.1 RATE_LIMITED envelope so
// existing client error-handling paths apply uniformly. The retry-
// after hint is the next minute boundary — the same shape the limit
// path uses. F-11.3.22.
func (m *middleware) writeFailOpenExceeded(w http.ResponseWriter, now time.Time) {
	if m.opts.Metrics != nil {
		// Reuse the existing per-scope rejected counter under a
		// dedicated `failopen_exceeded` scope so the §16.5 alerts can
		// distinguish a fail-closed rejection from a regular limit hit.
		m.opts.Metrics.IncRateLimitRejected("failopen_exceeded")
	}
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
			"message":   "rate-limit counter unavailable; fail-open ceiling exceeded — failing closed",
			"retryable": true,
			"details": map[string]any{
				"scope":             "failopen_exceeded",
				"failOpenMaxSeconds": int(m.effectiveFailOpenMax() / time.Second),
			},
		},
	})
}

func (m *middleware) logger() *log.Logger {
	if m.opts.Logger != nil {
		return m.opts.Logger
	}
	return log.Default()
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
// Retry-After header pointing at the next window boundary. It also
// emits the §11.1 line 7 `lenny_rate_limit_rejected_total{scope}`
// counter so admission rejections are attributable by scope.
// spec: §11.1 line 7; §15.1 RATE_LIMITED envelope.
func (m *middleware) writeRateLimited(w http.ResponseWriter, scope string, limit int, now time.Time) {
	if m.opts.Metrics != nil {
		m.opts.Metrics.IncRateLimitRejected(scope)
	}
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
