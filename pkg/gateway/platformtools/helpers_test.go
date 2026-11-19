// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/toolwire"
)

// testOwner is the subject the tests in this package authenticate as when a
// test sets no principal of its own, and therefore the owner of every
// session such a test creates, as in the session server's tests.
const testOwner = "alice@acme.com"

// asOwner wraps h so that a request reaching it with no principal carries a
// principal whose Subject is testOwner, so the session access checks the
// platform tools seam's routes run behind admit the owner. A request that
// already carries a principal keeps it. The principal carries no tenant, so
// the tenant still resolves from the request's X-Lenny-Tenant-ID dev header.
func asOwner(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authmw.FromContext(r.Context()); !ok {
			r = r.WithContext(authmw.WithPrincipal(r.Context(), authmw.Principal{Subject: testOwner}))
		}
		h.ServeHTTP(w, r)
	})
}

// newServer builds a session server over store with core, and attaches the
// platform tools seam's client routes with routes, as the composition root
// does: the routes walk the same session store, and resolve interactions
// in the interaction store the session server was given unless routes
// names another.
func newServer(t *testing.T, store sessionstore.Store, core sessionserver.Options, routes platformtools.Options) *sessionserver.Server {
	t.Helper()
	srv := sessionserver.New(store, core)
	routes.Store = store
	if routes.Interactions == nil && core.Interactions != nil {
		routes.Interactions = core.Interactions
	}
	toolwire.Attach(t, srv, routes)
	return srv
}

// decodeError decodes the client API error envelope of a response.
func decodeError(t *testing.T, rr *httptest.ResponseRecorder) (code, msg string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}
