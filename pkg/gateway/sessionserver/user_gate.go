// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/userstore"
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
			"the authenticated user has been invalidated and cannot create sessions", nil)
		return false
	}
	// §12.8 / GDPR Article 18: a user with a pending erasure request is
	// blocked from creating new sessions until the job completes.
	if user.ProcessingRestricted {
		s.writeError(w, http.StatusForbidden, "ERASURE_IN_PROGRESS",
			"this user has a pending erasure request; new sessions cannot be created until erasure completes",
			map[string]any{"userId": principal.Subject, "jobId": user.ErasureJobID})
		return false
	}
	return true
}
