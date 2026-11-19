// SPDX-License-Identifier: MIT

package drainreadiness_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/drain/drainreadiness"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// routeKeys lists each route of table as its listener, method, and
// pattern, and fails the test on a route with a check by the API front.
func routeKeys(t *testing.T, table *routetable.Table) string {
	t.Helper()
	names := map[seam.Listener]string{seam.ListenerClient: "client", seam.ListenerInternal: "internal", seam.ListenerForward: "forward"}
	var keys []string
	for _, r := range table.Routes() {
		if r.Access != seam.AccessHandler {
			t.Errorf("%s %s takes access check %d, want none by the API front", r.Method, r.Pattern, r.Access)
		}
		keys = append(keys, names[r.Listener]+" "+r.Method+" "+r.Pattern)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

// TestHandlersRegisterOnTheInternalListener checks that the drain-readiness and forced-drain audit endpoints register on the internal listener, and that the readiness endpoint answers through the table'\”s mux.
//
// spec: GWSEAM-RegisterRoutes
func TestHandlersRegisterOnTheInternalListener(t *testing.T) {
	table := routetable.New()
	if err := (&drainreadiness.Handler{Prober: readyProber{}}).RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	if err := (&drainreadiness.ForcedDrainHandler{}).RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	if got, want := routeKeys(t, table), "internal GET /internal/drain-readiness\ninternal POST /internal/audit/node-drain-forced"; got != want {
		t.Errorf("routes =\n%s\nwant\n%s", got, want)
	}
	rec := httptest.NewRecorder()
	table.Handler(seam.ListenerInternal).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/drain-readiness", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /internal/drain-readiness = %d, want 200", rec.Code)
	}
}

type readyProber struct{}

func (readyProber) Probe(context.Context) error { return nil }
