// SPDX-License-Identifier: MIT

// Package ownerprincipal gives a test that mounts a session surface without
// the gateway's auth middleware the authenticated principal that surface
// requires. The session server binds a new session's owner from the
// principal's subject and refuses a create with no principal subject, so a
// handler mounted with only the dev tenant header answers such a test 401.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
package ownerprincipal

import (
	"context"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// Alice is the default owner subject the suites authenticate as.
const Alice = "alice@acme.com"

// Context returns ctx carrying a principal whose Subject is subject and
// whose tenant is empty, so the tenant still resolves from the caller's
// service tenant or the X-Lenny-Tenant-ID dev header.
func Context(ctx context.Context, subject string) context.Context {
	return authmw.WithPrincipal(ctx, authmw.Principal{Subject: subject})
}

// Subject returns the Subject of the principal on ctx, or "" when ctx
// carries none. A fake session binder stamps it as the created row's owner,
// as the production binders do.
//
// spec: §10.2 (Session ownership) rule 1.
func Subject(ctx context.Context) string {
	p, _ := authmw.FromContext(ctx)
	return p.Subject
}

// Wrap returns h behind a layer that gives a request with no principal a
// principal whose Subject is subject. A request that already carries a
// principal keeps it, so a test that sets its own principal, including one
// with an empty Subject, is unaffected.
func Wrap(h http.Handler, subject string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authmw.FromContext(r.Context()); !ok {
			r = r.WithContext(Context(r.Context(), subject))
		}
		h.ServeHTTP(w, r)
	})
}

// SessionAccess is a session-service double for a suite that calls the MCP
// tools reading the session store directly. Its SessionAccessible grants
// the row's owner and nobody else, the owner half of the §10.2 ownership
// predicate, so a fixture that calls as the seeded owner reaches the tool
// body. Its ServiceCall refuses, because such a suite dispatches no
// client-facing tool through the shared service layer.
//
// spec: §10.2 (Session ownership) rules 4 and 5.
type SessionAccess struct{}

// ServiceCall refuses every call with INTERNAL_ERROR.
func (SessionAccess) ServiceCall(context.Context, string, string, string, []byte, string, map[string]string) (sessionserver.ServiceResult, *sessionserver.ServiceError) {
	return sessionserver.ServiceResult{}, &sessionserver.ServiceError{
		HTTPStatus: http.StatusInternalServerError, Code: "INTERNAL_ERROR",
		Message: "ownerprincipal.SessionAccess serves no client-facing tool",
	}
}

// SessionAccessible reports whether the principal on ctx owns row.
func (SessionAccess) SessionAccessible(ctx context.Context, row sessionstore.Session, _ bool) bool {
	p, ok := authmw.FromContext(ctx)
	return ok && p.Subject != "" && p.Subject == row.UserID
}
