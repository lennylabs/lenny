// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// The doubles below stand in for the API front's caller identity and the
// session core's session operations, which the MCP transport reaches
// through the seam interface.

// originKey carries the origin claim a test request names on its context.
type originKey struct{}

// withTestOrigin stores the X-Test-Origin header of each request on its
// context, as the API front's authentication stores the caller, so
// headerOriginCaller answers the origin the request named.
func withTestOrigin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), originKey{}, r.Header.Get("X-Test-Origin"))))
	})
}

// fixedCaller answers every request with one caller.
type fixedCaller seam.Caller

func (c fixedCaller) RequestCaller(context.Context) (seam.Caller, bool) {
	return seam.Caller(c), true
}

// headerOriginCaller answers a caller of tenant acme with the bearer j1 and
// the origin withTestOrigin stored on the context.
type headerOriginCaller struct{}

func (headerOriginCaller) RequestCaller(ctx context.Context) (seam.Caller, bool) {
	origin, _ := ctx.Value(originKey{}).(string)
	return seam.Caller{TenantID: "acme", JTI: "j1", Origin: origin}, true
}

// lookupSessions answers every session lookup with err, or with a session
// of the tenant it was asked for when err is nil.
type lookupSessions struct {
	seam.Sessions
	err error
}

func (l lookupSessions) LookupSession(_ context.Context, tenantID, sessionID string) (seam.Session, error) {
	if l.err != nil {
		return seam.Session{}, l.err
	}
	return seam.Session{ID: sessionID, TenantID: tenantID}, nil
}

// grantAccess grants or refuses every session access.
type grantAccess bool

func (g grantAccess) SessionAccessible(context.Context, seam.Session, seam.SessionAction) bool {
	return bool(g)
}
