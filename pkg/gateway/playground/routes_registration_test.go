// SPDX-License-Identifier: MIT

package playground

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TestRegisterRoutesMountsThePlaygroundOnTheClientListener checks that the
// playground registers its page family and its bearer mint on the client
// listener for any method, with no check by the API front, and that the
// mux then dispatches by method inside the playground's own handlers.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesMountsThePlaygroundOnTheClientListener(t *testing.T) {
	h := New(Config{Enabled: true, AuthMode: AuthModeOIDC}, Options{})
	table := routetable.New()
	if err := h.RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/playground": true, "/playground/": true, "/v1/playground/token": true}
	for _, r := range table.Routes() {
		if !want[r.Pattern] || r.Listener != seam.ListenerClient || r.Method != seam.MethodAny || r.Access != seam.AccessHandler {
			t.Errorf("route %+v is not a client any-method route of the playground", r)
		}
		delete(want, r.Pattern)
	}
	if len(want) != 0 {
		t.Errorf("routes not registered: %v", want)
	}
	rec := httptest.NewRecorder()
	table.Handler(seam.ListenerClient).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/playground/token", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/playground/token = %d, want 405 from the playground's own mux", rec.Code)
	}
}
