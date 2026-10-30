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
)

// Alice is the default owner subject the suites authenticate as.
const Alice = "alice@acme.com"

// Context returns ctx carrying a principal whose Subject is subject and
// whose tenant is empty, so the tenant still resolves from the caller's
// service tenant or the X-Lenny-Tenant-ID dev header.
func Context(ctx context.Context, subject string) context.Context {
	return authmw.WithPrincipal(ctx, authmw.Principal{Subject: subject})
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
