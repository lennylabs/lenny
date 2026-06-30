// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantaccessstore"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// requirePoolSelectable honors or rejects a client-pinned pool (the §14.1
// CreateSessionRequest.pool selector). The spec treats `pool` as a
// first-class scheduling selector (§7.1 step 1 CreateSession(runtime,
// pool, …), §7.1 step 4 "Select pool", §14.1 CreateSessionRequest.pool),
// so a pin the gateway cannot satisfy is rejected rather than silently
// ignored and scheduled elsewhere. An empty pin is a no-op: resolution
// falls back to selecting a pool by runtime and §5.3 profile.
//
// When pinnedPool is set, the pin is validated fail-closed:
//
//   - Backing and isolation consistency: ResolvePool constrains resolution
//     to the named pool and requires it to be backed by runtimeRef and to
//     satisfy isolationProfile (symmetric with the §15.1 replay targetPool
//     rule "Must be a pool backed by targetRuntime"). An absent,
//     not-backed, or isolation-inconsistent pin is a deterministic client
//     fault, rejected 400 VALIDATION_ERROR.
//   - Authorization: the §4 pool_tenant_access grants gate which pools a
//     tenant may pin. A pin the tenant is not granted is rejected
//     403 FORBIDDEN, mirroring tenantReachesRuntime for runtimes. The
//     authorization check runs only after the pin is confirmed
//     satisfiable, so an unauthorized pin to a real pool returns 403 while
//     an unsatisfiable pin returns 400.
//   - Transient lookup failure: a non-deterministic error from the pool
//     lookup (a kube-apiserver or store outage, not the typed
//     not-satisfiable error) fails closed with the §15.1 503
//     SERVICE_UNAVAILABLE + Retry-After rather than admitting a session
//     whose pool the gateway could not confirm.
//
// It returns true when the create may proceed (no pin, or a satisfiable
// and authorized pin); when it returns false it has already written the
// §15.1 error envelope. spec: §7.1 (pool selector), §14.1
// (CreateSessionRequest.pool), §4 / §15.1 (pool_tenant_access grant).
func (s *Server) requirePoolSelectable(w http.ResponseWriter, r *http.Request, tenantID, runtimeRef, isolationProfile, pinnedPool string) bool {
	if pinnedPool == "" {
		return true
	}
	// No pool resolver wired (the Postgres-only posture does no CRD pool
	// binding): there is no warm-pool inventory to validate the pin
	// against, so the selector cannot be honored. Fail closed rather than
	// accept-echo-ignore: a client that pinned a pool must not get a
	// session silently scheduled without it.
	if s.podBinder == nil || s.podBinder.Client == nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"pool selection is not available in this deployment posture; omit the pool field",
			map[string]any{"field": "pool", "pool": pinnedPool, "reason": "pool_selection_unavailable"})
		return false
	}
	_, err := podsession.ResolvePool(r.Context(), s.podBinder.Client, s.poolPolicyReader(),
		s.agentNamespace, runtimeRef, isolationProfile, pinnedPool)
	if err != nil {
		return s.writePoolSelectionError(w, runtimeRef, isolationProfile, pinnedPool, err)
	}
	// spec: §4 pool_tenant_access / §15.1 — a client may not pin a pool its
	// tenant is not granted. Checked against the same §4 grant store the
	// admin pools/{name}/tenant-access surface writes, mirroring
	// tenantReachesRuntime. The grant gate runs after the pin is confirmed
	// satisfiable so an unauthorized pin to a real pool reads as 403, not 400.
	if !s.tenantReachesPool(r.Context(), tenantID, pinnedPool) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"the tenant is not granted access to the requested pool",
			map[string]any{"field": "pool", "pool": pinnedPool, "reason": "pool_access_denied"})
		return false
	}
	return true
}

// writePoolSelectionError maps a ResolvePool failure on a client-pinned
// pool to its §15.1 envelope. A typed ErrPoolNotSatisfiable (absent,
// not-backed, or isolation-inconsistent pin) is a deterministic client
// fault, mapped 400 VALIDATION_ERROR. Any other error is a transient
// lookup failure (a kube-apiserver or store outage), mapped to the
// fail-closed §15.1 503 SERVICE_UNAVAILABLE + Retry-After. It always
// returns false so the caller propagates the rejection. spec: §7.1, §14.1,
// §15.1 (SERVICE_UNAVAILABLE).
func (s *Server) writePoolSelectionError(w http.ResponseWriter, runtimeRef, isolationProfile, pinnedPool string, err error) bool {
	if errors.Is(err, podsession.ErrPoolNotSatisfiable) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"the requested pool does not exist, is not backed by the requested runtime, or does not satisfy the requested isolation profile",
			map[string]any{
				"field":            "pool",
				"pool":             pinnedPool,
				"runtimeRef":       runtimeRef,
				"isolationProfile": isolationProfile,
				"reason":           "pool_not_satisfiable",
			})
		return false
	}
	// spec: §15.1 SERVICE_UNAVAILABLE — a transient pool lookup failure is
	// not a definite answer; fail closed rather than admit a session against
	// a pool the gateway could not confirm. The granular cause stays in the
	// gateway logs rather than the client envelope.
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
		"the pool selection could not be resolved because a dependency was transiently unavailable",
		map[string]any{"field": "pool", "pool": pinnedPool, "reason": "pool_lookup_unavailable"})
	return false
}

// tenantReachesPool reports whether tenantID holds a §4 pool_tenant_access
// grant for the named pool. It fails closed when the tenant-access
// registry is not wired or the tenant id is empty, mirroring
// tenantReachesRuntime for the runtime_tenant_access table. spec: §4
// (pool_tenant_access), §15.1 (pools/{name}/tenant-access).
func (s *Server) tenantReachesPool(ctx context.Context, tenantID, pool string) bool {
	if s.tenantAccess == nil || tenantID == "" {
		return false
	}
	grants, err := s.tenantAccess.List(ctx, tenantaccessstore.KindPool, pool)
	if err != nil {
		return false
	}
	for _, g := range grants {
		if g.TenantID == tenantID {
			return true
		}
	}
	return false
}
