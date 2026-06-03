// SPDX-License-Identifier: MIT

package sessionserver

import (
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// requirePoolNotDraining enforces the §15.1 line 797 pool-drain
// backpressure on session creation. When the (runtimeRef, isolation
// profile) pair resolves to a pool that has entered the `draining`
// phase, the create is rejected with 503 POOL_DRAINING and a
// `Retry-After` header set to the estimated drain-completion seconds
// (ceil of the longest active session age in the pool, capped at the
// pool's maxSessionAgeSeconds). The error envelope carries
// `details.pool` and `details.estimatedDrainSeconds` per §15.1 line
// 1034.
//
// The gate is a no-op when no pool resolver is wired or no pool matches
// the pair (the Postgres-only posture does no pool binding, so there is
// no warm pool to drain), and when no pool store is wired. It returns
// true when the create may proceed; when it returns false it has already
// written the 503 response.
//
// spec: §15.1 line 797 (drain backpressure); §15.1 line 1034
// (POOL_DRAINING envelope).
func (s *Server) requirePoolNotDraining(w http.ResponseWriter, r *http.Request, runtimeRef string, requested isolation.Profile) bool {
	if s.pools == nil || s.poolNameResolver == nil || runtimeRef == "" {
		return true
	}
	poolName, ok := s.poolNameResolver(r.Context(), runtimeRef, requested)
	if !ok || poolName == "" {
		return true
	}
	pool, err := s.pools.Get(r.Context(), poolName)
	if err != nil || !pool.IsDraining() {
		// A lookup error fails open: a transient pool-store outage must
		// not block admission against a pool that is not draining. The
		// dual-store gate in createSession already rejects when the
		// coordination stores are unreachable.
		return true
	}
	est := 0
	if s.store != nil {
		if _, oldest, statErr := s.store.PoolDrainStats(r.Context(), poolName); statErr == nil && !oldest.IsZero() {
			est = poolstore.EstimatedDrainSeconds(pool, int(s.clock().Sub(oldest).Seconds()))
		}
	}
	w.Header().Set("Retry-After", strconv.Itoa(est))
	s.writeError(w, http.StatusServiceUnavailable, "POOL_DRAINING",
		"the target pool is draining and is not accepting new sessions",
		map[string]any{"pool": poolName, "estimatedDrainSeconds": est})
	return false
}
