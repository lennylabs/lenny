// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
)

// getPrincipal exposes the auth middleware's Principal lookup so the
// session handlers stay decoupled from the middleware package's
// internal context key naming.
func getPrincipal(r *http.Request) (authmw.Principal, bool) {
	return authmw.FromContext(r.Context())
}

// authValidateTenantID re-exports auth.ValidateTenantID under a name
// that does not collide with the local `auth` middleware alias.
func authValidateTenantID(s string) error { return auth.ValidateTenantID(s) }

// resolveTenant returns the tenant id for this request. It is the tenant
// lookupTenant resolves, or the empty string when lookupTenant resolves
// none. The API front's request pipeline refuses such a request with 401
// UNAUTHORIZED before any tenant-scoped handler runs, and the in-process
// service entry points apply guardTenant, so a handler never sees the
// empty string.
//
// The returned tenant id is otherwise always a §10.2-valid identifier or
// `default`. Handlers can therefore use it directly in store queries and
// §4.5 blob URIs without re-validating.
func (s *Server) resolveTenant(r *http.Request) string {
	tenant, _ := s.lookupTenant(r)
	return tenant
}

// lookupTenant resolves the tenant a request runs as, and reports whether
// it resolved one, through authmw.RequestTenant: the principal's tenant,
// then the service tenant an in-process service caller passed to
// ServiceCall, CreateSessionService, or CreateAndStartService, then, only
// under DevTenantHeader, the dev header or `default`. A service tenant
// resolves the tenant only. It does not admit a create: createSession and
// handleCreateAndStart take the owner from the principal through
// ownerFromPrincipal, so a caller with a service tenant and no principal
// subject is refused with 401.
func (s *Server) lookupTenant(r *http.Request) (string, bool) {
	return authmw.RequestTenant(r, s.devTenantHeader)
}

// errNoPrincipalSubject reports a request that carries no authenticated
// principal, or a principal whose Subject is empty. Such a request has no
// owner to bind a session to and no subject to compare with a session's
// owner, so the session surfaces refuse it with 401 UNAUTHORIZED through
// writeNoPrincipalSubject.
var errNoPrincipalSubject = errors.New("request carries no authenticated principal subject")

// ownerFromPrincipal returns the subject of the request's authenticated
// principal, which is the owner of any session the request creates. It is
// the only source of a new session's owner on the sessionserver create
// paths: no request field sets the owner. A request with no principal, or
// with a principal whose Subject is empty, yields errNoPrincipalSubject,
// in every deployment mode. The dev paths the specification defines
// synthesize a subject, and a dev-header caller sends X-Lenny-User-ID, so
// neither needs an exception here.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
func ownerFromPrincipal(ctx context.Context) (string, error) {
	p, ok := authmw.FromContext(ctx)
	if !ok || p.Subject == "" {
		return "", errNoPrincipalSubject
	}
	return p.Subject, nil
}

// writeNoPrincipalSubject writes the §15.1 UNAUTHORIZED envelope for a
// request ownerFromPrincipal refused. Every session surface that needs the
// caller's subject writes the no-subject refusal here, so the response is
// one envelope.
//
// spec: §10.2 (Session ownership) rule 3; §15.1 (UNAUTHORIZED).
func (s *Server) writeNoPrincipalSubject(w http.ResponseWriter) {
	s.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
		"request requires an authenticated principal with a subject",
		map[string]any{"reason": "principal_subject_required"})
}

// requireOwner resolves the request's session owner through
// ownerFromPrincipal. On a refusal it writes the 401 and returns false, and
// the caller returns without running any gate, store write, or claim.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner, err := ownerFromPrincipal(r.Context())
	if err != nil {
		s.writeNoPrincipalSubject(w)
		return "", false
	}
	return owner, true
}

// guardTenant applies the tenant check of the API front's request pipeline
// to a single handler, for the in-process service entry points that call a
// handler directly rather than through the pipeline.
//
// spec: §10.2 (Authentication); §15.1 (UNAUTHORIZED).
func (s *Server) guardTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.lookupTenant(r); !ok {
			s.writeUnauthenticated(w)
			return
		}
		next(w, r)
	}
}

// writeUnauthenticated writes the §15.1 UNAUTHORIZED envelope for a
// request that carries no authenticated principal. details.reason matches
// the auth middleware's missing-credential rejection.
func (s *Server) writeUnauthenticated(w http.ResponseWriter) {
	s.writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
		"request requires a bearer token",
		map[string]any{"reason": "auth_required"})
}

// contextWithServiceTenant records tenant as the request's service tenant
// through authmw.WithServiceTenant, which records no empty or malformed
// tenant.
func contextWithServiceTenant(ctx context.Context, tenant string) context.Context {
	return authmw.WithServiceTenant(ctx, tenant)
}
