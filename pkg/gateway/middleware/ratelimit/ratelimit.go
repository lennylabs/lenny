// SPDX-License-Identifier: MIT

// Package ratelimit is the §11.1 requests-per-minute admission
// middleware. It increments a global, a per-user, and a per-tenant
// request counter on every API request and rejects with 429
// RATE_LIMITED once a scope's count exceeds its configured per-minute
// limit.
//
// The §11.1 line 7 table names global, per-user, per-runtime, and
// per-pool scopes; the per-tenant scope is the §13.3 line 607
// per-tenant axis ("a separate global per-tenant limit … enforced …
// using the rate limiter in §11.1"). Global, per-user, and per-tenant
// run here at the HTTP boundary because the request principal carries
// the tenant and subject. The per-runtime and per-pool scopes need the
// request's resolved runtime and pool, which are not available at the
// middleware boundary; they are enforced on the session-creation path
// where the runtime and pool are known (sessionserver
// requireAdmissionRateLimit, F-11.1.2).
//
// A counter error fails open: §11.1 admission must not block traffic
// on a transient counter outage. The fail-open path emits the
// lenny_rate_limit_failopen_active gauge that the §16.5
// RateLimitDegraded alert reads and increments
// lenny_rate_limit_counter_failure_total so an outage is observable
// even before the alert's persistence window.
//
// Every admitted and rejected response also carries the §15.1 lines
// 1131-1138 rate-limit triplet (X-RateLimit-Limit, X-RateLimit-
// Remaining, X-RateLimit-Reset) for the binding scope so clients can
// proactively respect the budget, and the middleware injects Retry-After
// on any downstream 503 that did not set its own (F-15.1.7).
package ratelimit

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/storage/failopen"
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

	// PerTenantPerMinute caps one tenant's requests per minute across
	// all of its users. It is the §13.3 line 607 per-tenant axis (a
	// fair-share brake a single tenant cannot evade by spreading load
	// across users under the per-user cap). Zero or less leaves the
	// per-tenant scope unlimited. spec: §13.3 line 607; §11.1 line 7.
	PerTenantPerMinute int

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

	// FailOpen is the §12.4 lines 220-224 per-replica degraded-mode
	// controller. When set, a request that fails open on a counter error
	// is additionally evaluated against the in-memory per-user / per-tenant
	// emergency ceilings and the cumulative fail-open timer: a single user
	// cannot monopolize the tenant's per-replica allocation during the
	// outage, and the replica transitions to fail-closed once it has spent
	// more than quotaFailOpenCumulativeMaxSeconds in fail-open mode. The
	// controller's Enter/Exit edges are driven by the same counter
	// error/recovery edges that flip the failopen gauge. Nil leaves the
	// legacy allow-until-episode-cap behaviour. spec: §12.4 lines 220-224.
	FailOpen *failopen.Controller
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

	// spec: §15.1 lines 1131-1138 — every REST response carries the
	// rate-limit triplet, and 429/503 responses carry Retry-After.
	// Wrap the writer so a downstream 503 (circuit breaker, dual-store
	// outage, backend error) gets a Retry-After it would not otherwise
	// set; handlers that set their own value keep it. The triplet itself
	// is written onto the header map before the inner handler runs.
	rw := &rateLimitRW{ResponseWriter: w, retryAfter: strconv.Itoa(retryAfterSeconds(now))}

	// counterFailed tracks whether ANY scope had to fail-open this
	// request. We defer the per-episode cap evaluation to the end so the
	// metric/log side-effects fire on the very first error AND the cap
	// applies even when only the per-user scope sees the error. spec:
	// §11.3 line 222; §12.4 line 220. F-11.3.22.
	counterFailed := false

	// scopes accumulates the (limit, count) of every scope counted this
	// request so the §15.1 triplet can report the binding scope (the one
	// with the least headroom). spec: §15.1 lines 1131-1138. F-15.1.7.
	var scopes []scopeUsage

	if m.opts.GlobalPerMinute > 0 {
		count, err := m.opts.Counter.Incr(r.Context(), "global", now)
		if err != nil {
			counterFailed = true
			m.onCounterError("global", err, now)
		} else {
			m.onCounterSuccess()
			scopes = append(scopes, scopeUsage{limit: m.opts.GlobalPerMinute, count: count})
			if count > m.opts.GlobalPerMinute {
				m.writeRateLimited(rw, "global", m.opts.GlobalPerMinute, now)
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
				scopes = append(scopes, scopeUsage{limit: m.opts.PerUserPerMinute, count: count})
				if count > m.opts.PerUserPerMinute {
					m.writeRateLimited(rw, "user", m.opts.PerUserPerMinute, now)
					return
				}
			}
		}
	}

	// spec: §13.3 line 607 / §11.1 line 7 — per-tenant fair-share brake.
	// A tenant whose users collectively saturate the gateway while each
	// stays under PerUserPerMinute is still capped as a whole. F-11.1.8.
	if m.opts.PerTenantPerMinute > 0 {
		if key, ok := tenantKey(r); ok {
			count, err := m.opts.Counter.Incr(r.Context(), key, now)
			if err != nil {
				counterFailed = true
				m.onCounterError("tenant", err, now)
			} else {
				m.onCounterSuccess()
				scopes = append(scopes, scopeUsage{limit: m.opts.PerTenantPerMinute, count: count})
				if count > m.opts.PerTenantPerMinute {
					m.writeRateLimited(rw, "tenant", m.opts.PerTenantPerMinute, now)
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
		m.writeFailOpenExceeded(rw, now)
		return
	}

	// spec: §12.4 lines 220-224 — while failing open on a Redis outage,
	// apply the per-replica emergency ceilings (per-user / per-tenant) and
	// the cumulative fail-open timer so a single user cannot monopolize the
	// tenant allocation and a sustained-outage replica transitions to
	// fail-closed for quota once cumulative time exceeds the configured
	// maximum. F-12.4.9 / F-11.2.6.
	if counterFailed && m.opts.FailOpen != nil {
		if dec := m.opts.FailOpen.Evaluate(m.failOpenRequest(r), now); !dec.Admit {
			m.writeFailOpenRejected(rw, dec, now)
			return
		}
	}

	// spec: §15.1 lines 1131-1138 — admitted requests carry the triplet
	// for the binding scope so clients can proactively respect the
	// budget. With no configured scope (limits all zero, or an
	// unauthenticated request under only per-user/per-tenant caps) there
	// is nothing to report and the headers are omitted. F-15.1.7.
	if binding, ok := bindingScope(scopes); ok {
		setRateLimitHeaders(rw.Header(), binding.limit, binding.remaining(), windowResetUnix(now))
	}

	m.inner.ServeHTTP(rw, r)
}

// scopeUsage is one rate-limit scope's configured limit and the
// request's running count within the current window.
type scopeUsage struct {
	limit int
	count int
}

// remaining is the requests left in the window for this scope, floored
// at zero. spec: §15.1 line 1134 (X-RateLimit-Remaining). F-15.1.7.
func (s scopeUsage) remaining() int {
	if r := s.limit - s.count; r > 0 {
		return r
	}
	return 0
}

// bindingScope returns the scope with the least remaining headroom —
// the one a client is closest to exhausting and therefore the one the
// §15.1 triplet reports. ok is false when no scope was counted.
// spec: §15.1 lines 1131-1138. F-15.1.7.
func bindingScope(scopes []scopeUsage) (scopeUsage, bool) {
	if len(scopes) == 0 {
		return scopeUsage{}, false
	}
	binding := scopes[0]
	for _, s := range scopes[1:] {
		if s.remaining() < binding.remaining() {
			binding = s
		}
	}
	return binding, true
}

// windowResetUnix is the UTC epoch second at which the current
// fixed one-minute window resets — the next minute boundary. It mirrors
// the Counter's `now.Unix()/60` window bucketing. spec: §15.1 line 1135
// (X-RateLimit-Reset). F-15.1.7.
func windowResetUnix(now time.Time) int64 {
	return (now.Unix()/60 + 1) * 60
}

// retryAfterSeconds is the whole-second backoff to the next window
// boundary, floored at one so a request arriving in the final second of
// a window never advertises a zero wait. spec: §15.1 line 1136
// (Retry-After). F-15.1.7.
func retryAfterSeconds(now time.Time) int {
	s := 60 - now.Second()
	if s <= 0 {
		return 1
	}
	return s
}

// setRateLimitHeaders writes the §15.1 X-RateLimit triplet. It is the
// single source of header-name truth so the admitted path and the
// rejection envelopes stay consistent. spec: §15.1 lines 1131-1138.
// F-15.1.7.
func setRateLimitHeaders(h http.Header, limit, remaining int, reset int64) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
}

// rateLimitRW injects Retry-After on a 503 the inner handler emits
// without one, so the §15.1 "present on 429 and 503 responses"
// guarantee holds for downstream Service-Unavailable responses (circuit
// breaker, dual-store outage) that did not set their own backoff hint.
// A handler that set Retry-After keeps it. spec: §15.1 line 1136.
// F-15.1.7.
type rateLimitRW struct {
	http.ResponseWriter
	retryAfter string
	wrote      bool
}

func (rw *rateLimitRW) WriteHeader(code int) {
	if !rw.wrote {
		rw.wrote = true
		if code == http.StatusServiceUnavailable && rw.Header().Get("Retry-After") == "" {
			rw.Header().Set("Retry-After", rw.retryAfter)
		}
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *rateLimitRW) Write(b []byte) (int, error) {
	rw.wrote = true
	return rw.ResponseWriter.Write(b)
}

// Flush forwards to the underlying http.Flusher so this wrapper does
// not break §15.1 SSE streaming or the §4.9 LLM-proxy stream.
func (rw *rateLimitRW) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		rw.wrote = true
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to net/http's ResponseController so
// Hijack and deadline capabilities the inner handlers rely on traverse.
func (rw *rateLimitRW) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

// Hijack forwards to the underlying http.Hijacker. This wrapper is the
// topmost ResponseWriter the §15.2 / §27.5 MCP WebSocket handler at
// /mcp/v1/ws receives, and nhooyr.io/websocket performs a direct
// http.Hijacker type assertion (it does not consult Unwrap), so the
// method must be present on the concrete type for the upgrade to
// succeed. spec: §27.5 / §27.3.1 line 142.
func (rw *rateLimitRW) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("ratelimit: underlying ResponseWriter does not support hijacking")
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
		// spec: §12.4 line 224 — drive the per-replica fail-open controller
		// on the same leading edge so the cumulative timer starts and the
		// quota_failopen_started audit event fires exactly once per episode.
		if m.opts.FailOpen != nil {
			m.opts.FailOpen.Enter()
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
	// spec: §12.4 lines 222, 224 — close the cumulative episode and reset
	// the per-replica per-user backstop counters on the Redis-recovery edge.
	if m.opts.FailOpen != nil {
		m.opts.FailOpen.Exit()
	}
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
	retryAfter := retryAfterSeconds(now)
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
				"scope":              "failopen_exceeded",
				"failOpenMaxSeconds": int(m.effectiveFailOpenMax() / time.Second),
			},
		},
	})
}

// failOpenRequest builds the §12.4 per-replica ceiling-evaluation input
// for r. The per-tenant request limit (PerTenantPerMinute) is the
// `tenant_limit` the §12.4 ceiling formula divides by the cached replica
// count; the per-user ceiling is derived from it by the controller. The
// backstop keys reuse the same identity the shared-counter scopes use so a
// fail-open count for one user does not collide with a tenant bucket.
// spec: §12.4 lines 222-224. F-12.4.9.
func (m *middleware) failOpenRequest(r *http.Request) failopen.FailOpenRequest {
	req := failopen.FailOpenRequest{TenantLimit: int64(m.opts.PerTenantPerMinute)}
	if key, ok := userKey(r); ok {
		req.UserKey = key
	}
	if key, ok := tenantKey(r); ok {
		req.TenantKey = key
	}
	return req
}

// writeFailOpenRejected writes the §12.4 line 222/224 degraded-mode 429
// when a request is rejected by a per-replica emergency ceiling or by the
// cumulative fail-open timer. The envelope reuses the §15.1 RATE_LIMITED
// code so client error handling applies uniformly; details.scope names the
// binding control. spec: §12.4 lines 222-224. F-12.4.9.
func (m *middleware) writeFailOpenRejected(w http.ResponseWriter, dec failopen.Decision, now time.Time) {
	scope := "failopen_" + string(dec.Reason)
	if m.opts.Metrics != nil {
		m.opts.Metrics.IncRateLimitRejected(scope)
	}
	retryAfter := retryAfterSeconds(now)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
	details := map[string]any{"scope": scope}
	if dec.Ceiling > 0 {
		details["failOpenCeiling"] = dec.Ceiling
	}
	message := "rate-limit counter unavailable; per-replica fail-open ceiling reached"
	if dec.Reason == failopen.ReasonCumulativeExceeded {
		message = "rate-limit counter unavailable; cumulative fail-open budget exhausted — failing closed for quota"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":      "RATE_LIMITED",
			"category":  "POLICY",
			"message":   message,
			"retryable": true,
			"details":   details,
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

// tenantKey returns the per-tenant counter key for the request, or
// ("", false) when the request carries no authenticated tenant. The
// "t:" prefix keeps the tenant bucket disjoint from the "u:" per-user
// and "global" buckets so the three scopes count independently.
// spec: §13.3 line 607; §11.1 line 7. F-11.1.8.
func tenantKey(r *http.Request) (string, bool) {
	p, ok := authmw.FromContext(r.Context())
	if !ok || p.TenantID == "" {
		return "", false
	}
	return "t:" + p.TenantID, true
}

// isInfraPath reports whether a path is an operational endpoint that
// monitoring scrapes and that the §11.1 admission limits exempt.
func isInfraPath(p string) bool {
	switch p {
	case "/healthz", "/metrics",
		"/openapi.yaml", "/openapi.json", "/v1/openapi.json":
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
	retryAfter := retryAfterSeconds(now)
	// spec: §15.1 lines 1131-1138 — the 429 carries the triplet for the
	// rejecting scope (remaining 0) alongside Retry-After. F-15.1.7.
	setRateLimitHeaders(w.Header(), limit, 0, windowResetUnix(now))
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
