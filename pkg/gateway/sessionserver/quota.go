// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// requireSessionQuota enforces the §11.2 per-tenant concurrent-session
// quota on the session-creation path. When the tenant already holds
// MaxConcurrentSessions non-terminal sessions, the create is rejected
// with 429 QUOTA_EXCEEDED.
//
// A zero limit, an unknown tenant, or an unwired tenant registry means
// the tenant has no concurrent-session limit. requireSessionQuota
// returns true when the create may proceed; when it returns false it
// has already written the response.
func (s *Server) requireSessionQuota(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.tenants == nil {
		return true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session quota check failed: "+err.Error(), nil)
		return false
	}
	if tenant.MaxConcurrentSessions <= 0 {
		return true
	}
	rows, err := s.store.List(r.Context(), tenantID, sessionstore.ListFilter{})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session quota check failed: "+err.Error(), nil)
		return false
	}
	active := 0
	for _, row := range rows {
		if !session.IsTerminal(row.State) {
			active++
		}
	}
	if active >= tenant.MaxConcurrentSessions {
		s.writeError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED",
			"the tenant has reached its concurrent-session limit",
			map[string]any{
				"limit":  tenant.MaxConcurrentSessions,
				"active": active,
			})
		return false
	}
	return true
}
