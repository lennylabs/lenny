// SPDX-License-Identifier: MIT

package health_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/opsendpoints/health"
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

// TestRegisterRoutesMountsThePlatformHealthAPIOnTheClientListener checks that the platform health API registers at /v1/admin/health and under it for any method on the client listener.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesMountsThePlatformHealthAPIOnTheClientListener(t *testing.T) {
	table := routetable.New()
	if err := health.RegisterRoutes(table, health.NewAggregator(), nil); err != nil {
		t.Fatal(err)
	}
	if got, want := routeKeys(t, table), "client * /v1/admin/health\nclient * /v1/admin/health/"; got != want {
		t.Errorf("routes =\n%s\nwant\n%s", got, want)
	}
	rec := httptest.NewRecorder()
	table.Handler(seam.ListenerClient).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /v1/admin/health = %d, want 200", rec.Code)
	}
}
