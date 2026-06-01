// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/remediationlock"
)

// lockErrorMap maps each §25.4 canonical remediation-lock error code to
// its documented HTTP status and §25.2 category.
var lockErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	coordination.ErrCodeConflict:       {http.StatusConflict, conventions.CategoryPolicy},
	coordination.ErrCodeNotFound:       {http.StatusNotFound, conventions.CategoryPermanent},
	coordination.ErrCodeNotOwned:       {http.StatusForbidden, conventions.CategoryAuth},
	coordination.ErrCodeNoCoordination: {http.StatusServiceUnavailable, conventions.CategoryTransient},
}

// writeLockError maps a §25.4 RemediationLockService error to the §25.2
// canonical error envelope and writes it. A REMEDIATION_LOCK_CONFLICT
// raised by the split-brain rule carries splitBrain:true in the error
// details.
func writeLockError(w http.ResponseWriter, err error) {
	code := coordination.CodeOf(err)
	mapping, ok := lockErrorMap[code]
	if !ok {
		conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
			conventions.CategoryTransient, err.Error())
		return
	}
	body := conventions.NewError(code, mapping.category, err.Error())
	if coordination.IsSplitBrain(err) {
		body.Error.Details = map[string]any{"splitBrain": true}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mapping.status)
	writeJSONValue(w, body)
}

// registerLockRoutes wires the §25.4 remediation-lock endpoints onto the
// Server's mux.
func (s *Server) registerLockRoutes() {
	s.mux.HandleFunc("POST /v1/admin/remediation-locks", s.handleAcquireLock)
	s.mux.HandleFunc("GET /v1/admin/remediation-locks", s.handleListLocks)
	s.mux.HandleFunc("GET /v1/admin/remediation-locks/{id}", s.handleGetLock)
	s.mux.HandleFunc("PATCH /v1/admin/remediation-locks/{id}", s.handleExtendLock)
	s.mux.HandleFunc("DELETE /v1/admin/remediation-locks/{id}", s.handleReleaseLock)
	s.mux.HandleFunc("POST /v1/admin/remediation-locks/{id}/steal", s.handleStealLock)
}

// locksUnavailable reports the §25.4 remediation-lock surface as
// unconfigured.
func (s *Server) locksUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, "REMEDIATION_LOCK_UNAVAILABLE",
		conventions.CategoryTransient, "the remediation-lock subsystem is not configured")
}

// callerRole returns the §25.4 caller role for a lock request, mapped
// from the verified principal the §25.4 auth middleware attached. The
// requireAdminRole gate guarantees an authenticated caller holds
// platform-admin or tenant-admin, so the principal resolves to one of
// the two; platform-admin wins when both are present. The X-Lenny-Role
// header is consulted only when no principal is present (dev / embedded
// with no AuthConfig wired).
func callerRole(r *http.Request) remediationlock.Role {
	if p, ok := callerPrincipal(r); ok && len(p.Roles) > 0 {
		if p.HasRole(auth.RolePlatformAdmin) {
			return remediationlock.PlatformAdmin
		}
		if p.HasRole(auth.RoleTenantAdmin) {
			return remediationlock.TenantAdmin
		}
	}
	switch r.Header.Get("X-Lenny-Role") {
	case "tenant-admin":
		return remediationlock.TenantAdmin
	default:
		return remediationlock.PlatformAdmin
	}
}

// callerTenant returns the §25.4 caller tenant for a lock request,
// read from the verified principal's tenant claim. The X-Lenny-Tenant-ID
// header is consulted only when no principal is present (dev / embedded
// with no AuthConfig wired).
func callerTenant(r *http.Request) string {
	if p, ok := callerPrincipal(r); ok && p.TenantID != "" {
		return p.TenantID
	}
	return r.Header.Get("X-Lenny-Tenant-ID")
}

// authorizeLockScope applies the §25.4 scope-based remediation-lock
// authorization (which caller roles may touch which scopes). It writes
// the 403 LOCK_SCOPE_FORBIDDEN envelope and returns false when the
// caller's role does not authorize the scope.
//
// ownerTenant is the tenant that owns the scoped resource for a pool,
// credential-pool, or session scope. Pending the gateway lookup that
// resolves it, the caller's own tenant is supplied; this keeps the
// control fail-closed for tenant-admin (a tenant-admin can act only on
// its own scopes) without weakening platform-admin, which is authorized
// for every scope.
func (s *Server) authorizeLockScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	role := callerRole(r)
	tenant := callerTenant(r)
	if err := remediationlock.Authorize(role, tenant, scope, tenant); err != nil {
		conventions.WriteError(w, http.StatusForbidden, "LOCK_SCOPE_FORBIDDEN",
			conventions.CategoryAuth, "the caller's role does not authorize lock scope "+scope)
		return false
	}
	return true
}

// handleAcquireLock serves POST /v1/admin/remediation-locks: acquire a
// §25.4 remediation lock. The scope-based authorization runs before the
// store; the caller becomes acquiredBy.
func (s *Server) handleAcquireLock(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	var body struct {
		Scope       string `json:"scope"`
		Operation   string `json:"operation"`
		TTLSeconds  int    `json:"ttlSeconds"`
		OperationID string `json:"operationId"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	if body.Scope == "" {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "scope is required")
		return
	}
	if !s.authorizeLockScope(w, r, body.Scope) {
		return
	}
	lock, err := s.locks.Acquire(r.Context(), coordination.LockRequest{
		Scope:       body.Scope,
		Operation:   body.Operation,
		TTLSeconds:  body.TTLSeconds,
		OperationID: body.OperationID,
		AcquiredBy:  callerIdentity(r),
	})
	if err != nil {
		writeLockError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, lock)
}

// handleListLocks serves GET /v1/admin/remediation-locks: list the
// §25.4 active locks.
func (s *Server) handleListLocks(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	locks, err := s.locks.List(r.Context())
	if err != nil {
		writeLockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locks": locks})
}

// handleGetLock serves GET /v1/admin/remediation-locks/{id}: a single
// §25.4 lock's current state, used to validate ownership before
// continuing a remediation.
func (s *Server) handleGetLock(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	lock, err := s.locks.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lock)
}

// handleExtendLock serves PATCH /v1/admin/remediation-locks/{id}: extend
// a §25.4 lock's TTL. §25.4 authorization is identity-based — the caller
// must be the current acquiredBy.
func (s *Server) handleExtendLock(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	var body struct {
		TTLSeconds int `json:"ttlSeconds"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	ctx := coordination.WithCaller(r.Context(), callerIdentity(r))
	lock, err := s.locks.Extend(ctx, r.PathValue("id"), body.TTLSeconds)
	if err != nil {
		writeLockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lock)
}

// handleReleaseLock serves DELETE /v1/admin/remediation-locks/{id}:
// release a §25.4 lock. §25.4 authorization is identity-based — the
// caller must be the current acquiredBy; to release another holder's
// lock the caller must Steal it (audited).
func (s *Server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	ctx := coordination.WithCaller(r.Context(), callerIdentity(r))
	if err := s.locks.Release(ctx, r.PathValue("id")); err != nil {
		writeLockError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStealLock serves POST /v1/admin/remediation-locks/{id}/steal:
// take over an existing §25.4 lock. §25.4 makes steal an audited
// action; without confirm:true the §25.2 dry-run preview is returned.
func (s *Server) handleStealLock(w http.ResponseWriter, r *http.Request) {
	if s.locks == nil {
		s.locksUnavailable(w)
		return
	}
	var body struct {
		Confirm    bool   `json:"confirm"`
		Reason     string `json:"reason"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	// Read the current lock so the steal scope-authorizes against the
	// real scope and the preview reflects the lock being stolen.
	current, err := s.locks.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeLockError(w, err)
		return
	}
	if !s.authorizeLockScope(w, r, current.Scope) {
		return
	}
	// §25.2 dry-run/confirm: without confirm:true, return a preview.
	if !body.Confirm {
		writeJSON(w, http.StatusOK, map[string]any{
			"dryRun": true,
			"preview": map[string]any{
				"resourcesAffected": []string{"remediation-lock:" + current.ID},
				"estimatedDowntime": "0s",
				"warnings": []string{
					"This takes lock " + current.ID + " from " + current.AcquiredBy +
						". Re-run with confirm:true and a reason to apply.",
				},
			},
		})
		return
	}
	if body.Reason == "" {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "a reason is required to steal a lock")
		return
	}
	lock, err := s.locks.Steal(r.Context(), r.PathValue("id"), coordination.StealRequest{
		Confirm:    true,
		Reason:     body.Reason,
		TTLSeconds: body.TTLSeconds,
		AcquiredBy: callerIdentity(r),
	})
	if err != nil {
		writeLockError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lock)
}
