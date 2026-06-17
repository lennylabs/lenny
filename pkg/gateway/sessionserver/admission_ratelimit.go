// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// AdmissionRateLimitMetrics is the subset of §11.1 rate-limit metrics
// the per-runtime / per-pool session-creation admission gate emits.
// *gatewaymetrics.Metrics satisfies it (the same methods the §11.1
// HTTP middleware uses for its global/per-user/per-tenant scopes), so
// rejections and counter failures land on one metric vocabulary across
// both enforcement points. spec: §11.1 line 7.
type AdmissionRateLimitMetrics interface {
	IncRateLimitRejected(scope string)
	IncRateLimitCounterFailure()
}

// requireAdmissionRateLimit enforces the §11.1 line 7 per-runtime and
// per-pool requests-per-minute admission limits at session creation —
// the point where the target runtime (and, when the pool model is
// wired, the resolved pool) are known. The §11.1 HTTP middleware
// enforces the global, per-user, and per-tenant scopes at the request
// boundary; the per-runtime and per-pool scopes need the resolved
// runtime/pool and so are enforced here against the same Redis-backed
// per-minute counter.
//
// A counter error fails open per §11.1: admission must not block on a
// transient counter outage. requireAdmissionRateLimit returns true when
// the create may proceed; when it returns false it has already written
// the 429 RATE_LIMITED response.
//
// spec: §11.1 line 7. F-11.1.2.
func (s *Server) requireAdmissionRateLimit(w http.ResponseWriter, r *http.Request, tenantID, runtimeRef string, requested isolation.Profile) bool {
	if s.admissionRL == nil || runtimeRef == "" {
		return true
	}
	now := s.clock()
	if s.perRuntimePerMin > 0 {
		if !s.checkAdmissionScope(w, r, "runtime", "rt:"+tenantID+":"+runtimeRef, s.perRuntimePerMin, now) {
			return false
		}
	}
	if s.perPoolPerMin > 0 {
		if pool, ok := s.resolvePoolName(r.Context(), runtimeRef, requested); ok {
			if !s.checkAdmissionScope(w, r, "pool", "po:"+tenantID+":"+pool, s.perPoolPerMin, now) {
				return false
			}
		}
	}
	return true
}

// checkAdmissionScope increments one §11.1 scope counter and either
// admits, rejects with 429 RATE_LIMITED, or fails open on a counter
// error. spec: §11.1 line 7 (fail-open). F-11.1.2.
func (s *Server) checkAdmissionScope(w http.ResponseWriter, r *http.Request, scope, key string, limit int, now time.Time) bool {
	count, err := s.admissionRL.Incr(r.Context(), key, now)
	if err != nil {
		// §11.1 fail-open: a transient counter outage must not block
		// admission. Emit the same counter-failure metric the HTTP
		// middleware uses so a Redis outage is observable from either
		// enforcement point.
		if s.rlMetrics != nil {
			s.rlMetrics.IncRateLimitCounterFailure()
		}
		log.Printf("sessionserver: §11.1 %s rate-limit counter unavailable key=%q err=%q fail_open=true",
			scope, key, err.Error())
		return true
	}
	if count > limit {
		if s.rlMetrics != nil {
			s.rlMetrics.IncRateLimitRejected(scope)
		}
		retryAfter := 60 - now.Second()
		if retryAfter <= 0 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		s.writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
			"the "+scope+" request-rate limit was exceeded",
			map[string]any{"scope": scope, "limitPerMinute": limit})
		return false
	}
	return true
}

// resolvePoolName resolves the §5.2 warm pool a (runtime, isolation
// profile) pair maps to, for the §11.1 per-pool rate-limit key. It
// returns ("", false) when no pool resolver is wired (the Postgres-only
// posture) or no pool matches the pair — the per-pool scope is then
// skipped, the same fallback resolveIsolationLevel uses for the §7.1
// isolation level. spec: §11.1 line 7. F-11.1.2.
func (s *Server) resolvePoolName(ctx context.Context, runtimeRef string, requested isolation.Profile) (string, bool) {
	if s.podBinder == nil || s.podBinder.Client == nil {
		return "", false
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace, runtimeRef, string(requested))
	if err != nil || match.Pool == "" {
		return "", false
	}
	return match.Pool, true
}
