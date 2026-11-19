// SPDX-License-Identifier: MIT

package streamproxy_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/streamproxy"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// testOwner is the subject the tests in this package authenticate as when a
// test sets no principal of its own, and therefore the owner of every
// session such a test creates, as in the session server's tests.
const testOwner = "alice@acme.com"

// asOwner wraps h so that a request reaching it with no principal carries a
// principal whose Subject is testOwner, so the session read check the
// stream proxy's routes run behind admits the owner. A request that already
// carries a principal keeps it. The principal carries no tenant, so the
// tenant still resolves from the request's X-Lenny-Tenant-ID dev header.
func asOwner(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authmw.FromContext(r.Context()); !ok {
			r = r.WithContext(authmw.WithPrincipal(r.Context(), authmw.Principal{Subject: testOwner}))
		}
		h.ServeHTTP(w, r)
	})
}

// newStreamServer builds a session server over store and attaches the
// stream proxy to it as the composition root does: the proxy reads
// opts.Events, reaches the session core through the server's stream seam,
// and registers its routes with the server before the server's handler is
// built. A nil opts.Events leaves the proxy without a bus.
func newStreamServer(t *testing.T, store sessionstore.Store, opts sessionserver.Options) *sessionserver.Server {
	t.Helper()
	srv := sessionserver.New(store, opts)
	core := srv.StreamSeam()
	streams, err := streamproxy.New(streamproxy.Options{
		Events:   opts.Events,
		Sessions: core,
		Frames:   core,
		Identity: core,
		Clock:    opts.Clock,
	})
	if err != nil {
		t.Fatalf("streamproxy.New: %v", err)
	}
	if err := streams.RegisterRoutes(apifront.Routes(srv)); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return srv
}
