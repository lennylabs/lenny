// SPDX-License-Identifier: MIT

package pipeline

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/errorclassify"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// guard wraps next in the check of access. A resolved tenant needs no
// wrapper, because the gate runs every route with a check behind the tenant
// check; read and manage access take the session check of their action;
// the evaluation, usage, creation, and list checks take the permission gate
// of their permission.
//
// spec: §10.2 (Session ownership) rules 5, 6, and 7.
func (p *Pipeline) guard(access seam.RouteAccess, next http.Handler) http.Handler {
	switch access {
	case seam.AccessSessionRead:
		return p.session(seam.SessionRead, auth.PermReadOwnSessions, next)
	case seam.AccessSessionManage:
		return p.session(seam.SessionManage, auth.PermManageOwnSessions, next)
	case seam.AccessSessionEval:
		return p.permission(auth.PermSessionEvalWrite, next)
	case seam.AccessUsageView:
		return p.permission(auth.PermViewUsage, next)
	case seam.AccessSessionCreate:
		return p.permission(auth.PermManageOwnSessions, next)
	case seam.AccessSessionList:
		return p.permission(auth.PermReadOwnSessions, next)
	default:
		return next
	}
}

// permission wraps next in the permission gate for perm, which
// GWSEAM-AuthorizePermission resolves. A caller the gate refuses receives
// 403 FORBIDDEN naming the permission, and next does not run.
//
// spec: GWSEAM-AuthorizePermission
func (p *Pipeline) permission(perm auth.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := p.cfg.Permissions.AuthorizePermission(r.Context(), perm); err != nil {
			writeAuthorizationError(w, perm, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// session wraps a route on /v1/sessions/{id} in the session check of
// action, which GWSEAM-AuthorizeSession resolves for the session the path
// value "id" names in the request's tenant: the permission gate of perm,
// then the refusal of a request with no principal subject, then the
// ownership check. A session the caller may not read or manage answers
// exactly as a missing session does, so the refusal does not reveal that
// the session exists, and next never runs on a refusal.
//
// spec: GWSEAM-AuthorizeSession
func (p *Pipeline) session(action seam.SessionAction, perm auth.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := p.cfg.Sessions.AuthorizeSession(r.Context(), p.RequestTenant(r), r.PathValue("id"), action); err != nil {
			writeAuthorizationError(w, perm, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeAuthorizationError writes the §15.1 envelope of a refused check:
// 403 FORBIDDEN naming perm, 401 UNAUTHORIZED for a request with no
// principal subject, 404 RESOURCE_NOT_FOUND for a session that does not
// exist or that the caller may not see, and 500 INTERNAL_ERROR for any
// other failure.
//
// spec: §10.2 (Session ownership) rules 3 and 7; §15.1 (UNAUTHORIZED).
func writeAuthorizationError(w http.ResponseWriter, perm auth.Permission, err error) {
	switch {
	case errors.Is(err, seam.ErrForbidden):
		errorclassify.WriteError(w, http.StatusForbidden, "FORBIDDEN",
			"this endpoint requires the "+string(perm)+" permission", nil)
	case errors.Is(err, seam.ErrUnauthenticated):
		errorclassify.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"request requires an authenticated principal with a subject",
			map[string]any{"reason": "principal_subject_required"})
	case errors.Is(err, seam.ErrSessionNotFound):
		errorclassify.WriteError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
	default:
		errorclassify.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

// writeUnauthenticated writes the §15.1 UNAUTHORIZED envelope for a
// request for which no tenant resolves. details.reason matches the
// authentication middleware's missing-credential rejection.
func writeUnauthenticated(w http.ResponseWriter) {
	errorclassify.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED",
		"request requires a bearer token",
		map[string]any{"reason": "auth_required"})
}

// accessName names a route access check in the pipeline's route patterns.
func accessName(a seam.RouteAccess) string {
	switch a {
	case seam.AccessTenant:
		return "tenant"
	case seam.AccessSessionRead:
		return "session-read"
	case seam.AccessSessionManage:
		return "session-manage"
	case seam.AccessSessionEval:
		return "session-eval"
	case seam.AccessUsageView:
		return "usage-view"
	case seam.AccessSessionCreate:
		return "session-create"
	case seam.AccessSessionList:
		return "session-list"
	case seam.AccessHandler:
		return "handler"
	}
	return "unknown"
}
