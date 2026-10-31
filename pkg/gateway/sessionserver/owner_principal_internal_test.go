// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// testOwner is the subject the tests in this package authenticate as when a
// test sets no principal of its own, and therefore the owner of every
// session such a test creates.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
const testOwner = "alice@acme.com"

// asOwner wraps h so that a request reaching it with no principal carries a
// principal whose Subject is testOwner. The session server binds a new
// session's owner from the principal and refuses a create with no principal
// subject, so a test that mounts the handler without the auth middleware
// authenticates through this wrapper. A request that already carries a
// principal keeps it, so a test that sets its own principal, including one
// with an empty Subject, is unaffected. The principal carries no tenant, so
// the tenant still resolves from the request's X-Lenny-Tenant-ID dev header.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
func asOwner(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authmw.FromContext(r.Context()); !ok {
			r = r.WithContext(ownerCtx(r.Context()))
		}
		h.ServeHTTP(w, r)
	})
}

// ownerCtx returns ctx carrying a principal whose Subject is testOwner, for
// a test that calls CreateSessionService or CreateAndStartService directly.
//
// spec: §10.2 (Session ownership) rules 1 and 3.
func ownerCtx(ctx context.Context) context.Context {
	return authmw.WithPrincipal(ctx, authmw.Principal{Subject: testOwner})
}
