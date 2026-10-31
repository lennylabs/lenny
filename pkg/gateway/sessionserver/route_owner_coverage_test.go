// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"net/http"
	"strings"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
)

// TestEverySessionRouteIsOwnershipWrapped_spec_10_2 guards the single seam
// of the ownership rule: every /v1/sessions/{id} route Handler() mounts,
// except eval and usage, is mounted through the read or manage ownership
// wrapper and answers bob, a `user` who does not own alice's session, with
// a body byte-identical to a missing session's. A handler spy mounted
// behind the route's wrapper is never reached by bob and is reached once by
// alice, so a route added to the table outside the wrappers fails on its
// wrapper name and a wrapper that let bob through fails on the spy.
//
// spec: §10.2 (Session ownership) rules 5, 6, and 7.
func TestEverySessionRouteIsOwnershipWrapped_spec_10_2(t *testing.T) {
	bob := accessCaller{"bob/user", principalOf(accessBob, pkgauth.RoleUser)}
	alice := accessCaller{"alice/user", principalOf(accessAlice, pkgauth.RoleUser)}
	checked := 0
	for _, route := range handlerRoutes(t) {
		if !route.sessionIDRoute() || route.ownershipExempt() {
			continue
		}
		checked++
		t.Run(route.pattern, func(t *testing.T) {
			if route.wrapper != "readID" && route.wrapper != "manageID" {
				t.Fatalf("%s is mounted through %q, want readID or manageID", route.pattern, route.wrapper)
			}
			if spy := spyRequest(t, route, false, bob, "sess_alice"); spy.calls != 0 {
				t.Fatalf("bob on alice's session reached the handler spy behind %s %d times", route.wrapper, spy.calls)
			}
			if spy := spyRequest(t, route, false, alice, "sess_alice"); spy.calls != 1 {
				t.Fatalf("alice on her own session reached the handler spy behind %s %d times, want 1", route.wrapper, spy.calls)
			}
			missing := missingSessionBody(t, route)
			h, _ := ownershipMatrixServer(t, false)
			rr := matrixRequest(t, h, bob, route.method(), route.path("sess_alice"), "{}")
			if rr.Code != http.StatusNotFound || rr.Body.String() != missing {
				t.Fatalf("bob on alice's session = %d %s, want the missing-session body %s", rr.Code, rr.Body.String(), missing)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no /v1/sessions/{id} route was checked")
	}
}

// TestRouteSpecificLookupsAnswerAfterOwnership_spec_10_2 pins that the
// tool-use, elicitation, and memory routes answer RESOURCE_NOT_FOUND from
// the ownership check for a missing session and for another user's
// session, before their own lookup answers a route-specific code.
//
// spec: §10.2 (Session ownership) rule 7; §9.2 (respond_to_elicitation
// authorization).
func TestRouteSpecificLookupsAnswerAfterOwnership_spec_10_2(t *testing.T) {
	bob := accessCaller{"bob/user", principalOf(accessBob, pkgauth.RoleUser)}
	for _, route := range ownedSessionRoutes(t) {
		p := route.pattern
		if !strings.Contains(p, "/tool-use/") && !strings.Contains(p, "/elicitations/") && !strings.Contains(p, "/memory") {
			continue
		}
		for _, sess := range []string{"sess_alice", "sess_missing"} {
			t.Run(p+"/"+sess, func(t *testing.T) {
				h, _ := ownershipMatrixServer(t, false)
				rr := matrixRequest(t, h, bob, route.method(), route.path(sess), "{}")
				if rr.Code != http.StatusNotFound || errorCode(t, rr) != "RESOURCE_NOT_FOUND" {
					t.Fatalf("%s %s = %d %s, want 404 RESOURCE_NOT_FOUND", route.method(), route.path(sess), rr.Code, rr.Body.String())
				}
			})
		}
	}
}
