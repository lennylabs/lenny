// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// requireActiveUser enforces §11.4 user invalidation and the §12.8
// erasure processing-restriction on the session-creation path. When
// the authenticated principal maps to a registered user that has been
// soft-disabled, hard-disabled, or fully-revoked, the request is
// denied with 403 USER_INVALIDATED; when the user has a pending GDPR
// erasure job, it is denied with 403 ERASURE_IN_PROGRESS.
//
// A principal with no user-registry row is admitted: §11.4 governs
// invalidation of known users, not registry membership, so dev-header
// principals and service tokens that were never provisioned through
// the admin user API still create sessions.
//
// requireActiveUser returns true when the request may proceed. When it
// returns false it has already written the response.
func (s *Server) requireActiveUser(w http.ResponseWriter, r *http.Request) bool {
	return s.requireActiveUserForAction(w, r, "cannot create sessions")
}

// requireActiveUserForAction is the caller check: the §11.4 gate on the
// requesting user and the caller refusal of the §12.8 Article 18
// restriction. It reads the principal's user row from the primary and
// checks invalidation before the restriction, so an invalidated caller
// answers USER_INVALIDATED whatever its restriction. The action clause is
// folded into both 403 messages so each call site names the operation it
// blocks (session creation, interaction resolution). The check runs once,
// when the request is admitted: it guards no residue, because a session a
// restricted caller would create belongs to the owner the creation fence
// reads.
// spec: §11.4 (User Invalidation), §12.8 (Processing restriction during
// erasure (GDPR Article 18)).
func (s *Server) requireActiveUserForAction(w http.ResponseWriter, r *http.Request, action string) bool {
	if s.users == nil {
		return true
	}
	principal, ok := getPrincipal(r)
	if !ok || principal.Subject == "" {
		return true
	}
	user, err := s.users.Get(r.Context(), principal.TenantID, principal.Subject)
	if errors.Is(err, userstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"user-invalidation check failed: "+err.Error(), nil)
		return false
	}
	if !user.IsActive() {
		s.writeError(w, http.StatusForbidden, "USER_INVALIDATED",
			"the authenticated user has been invalidated and "+action, nil)
		return false
	}
	// §12.8 / GDPR Article 18: a user with a pending erasure request is
	// blocked from the action until the job completes.
	if user.ProcessingRestricted {
		s.writeError(w, http.StatusForbidden, "ERASURE_IN_PROGRESS",
			"the authenticated user has a pending erasure request and "+action+" until erasure completes",
			map[string]any{"userId": principal.Subject, "jobId": user.ErasureJobID})
		return false
	}
	return true
}

// requireCreationAllowed is the early creation gate of the §12.8 creation
// fence. It reads the tenant row and, for a non-empty ownerUserID, the
// owner's user row, and refuses with the code CreationMarkers.Refusal
// answers for a root session (IsDelegatedChild false), so the gate and the
// fence in the store's insert transaction answer the same code for the
// same marker state. ownerUserID is the user who will own the new row,
// which can differ from the principal the caller check reads. A missing
// row carries no marker; any other read error answers 500 INTERNAL_ERROR.
// The gate is an early-out that refuses before any pod claim, rate
// counter, or other side effect; the fence remains the serialization
// point. It returns true when the create may proceed; when it returns
// false it has already written the response.
// spec: §12.8 (Lifecycle fence and drain).
func (s *Server) requireCreationAllowed(w http.ResponseWriter, r *http.Request, tenantID, ownerUserID string) bool {
	m, err := sessionstore.StoreMarkers(s.tenants, s.users)(r.Context(), tenantID, ownerUserID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"session-creation lifecycle check failed: "+err.Error(), nil)
		return false
	}
	if ref := m.Refusal(tenantID, ownerUserID); ref != nil {
		s.writeCreationRefusal(w, ref)
		return false
	}
	return true
}

// writeCreationRefusal writes a creation-fence refusal as 403 with its
// code, message, and details. Every create site maps a
// *sessionstore.CreationRefusal from the store through it, ahead of its
// retryable 5xx mapping, so a fence refusal never reaches the client as a
// transient failure.
// spec: §12.8 (Lifecycle fence and drain), §15.1 (Error code catalog).
func (s *Server) writeCreationRefusal(w http.ResponseWriter, ref *sessionstore.CreationRefusal) {
	s.writeError(w, http.StatusForbidden, ref.Code, ref.Message, ref.Details)
}

// requireSourceOwnerNotRestricted is the source-owner check of a derive or
// a replay: it refuses with 403 ERASURE_IN_PROGRESS, naming the source
// session's owner and the erasure job, when that owner's processing is
// restricted. It reads no other marker and is not the fence predicate,
// because the fence reads the new session's owner. An empty source owner
// and a missing registry row admit; any other read error answers 500
// INTERNAL_ERROR.
//
// The check runs once, when the request is admitted. A derive or replay
// admitted before the source owner's restriction commits copies into a
// session whose own owner's erasure reaches it, which leaves the outcome of
// a derive that completed before the erasure started, so the check guards
// no residue and needs no lock.
// spec: §12.8 (Processing restriction during erasure (GDPR Article 18)).
func (s *Server) requireSourceOwnerNotRestricted(w http.ResponseWriter, r *http.Request, tenantID, sourceOwner string) bool {
	if s.users == nil || sourceOwner == "" {
		return true
	}
	user, err := s.users.Get(r.Context(), tenantID, sourceOwner)
	if errors.Is(err, userstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"source-owner restriction check failed: "+err.Error(), nil)
		return false
	}
	if user.ProcessingRestricted {
		s.writeError(w, http.StatusForbidden, "ERASURE_IN_PROGRESS",
			"the source session's owner has a pending erasure request; the session cannot be derived or replayed until erasure completes",
			map[string]any{"userId": sourceOwner, "jobId": user.ErasureJobID})
		return false
	}
	return true
}
