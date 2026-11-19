// SPDX-License-Identifier: MIT

package gatewaymetrics_test

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
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

// TestMetricsRegistersTheScrapeEndpointOnTheInternalListener checks that the scrape endpoint registers on the internal listener and serves the registry.
//
// spec: GWSEAM-RegisterRoutes
func TestMetricsRegistersTheScrapeEndpointOnTheInternalListener(t *testing.T) {
	m, err := gatewaymetrics.New()
	if err != nil {
		t.Fatal(err)
	}
	table := routetable.New()
	if err := m.RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	if got, want := routeKeys(t, table), "internal GET /metrics"; got != want {
		t.Errorf("routes = %s, want %s", got, want)
	}
	rec := httptest.NewRecorder()
	table.Handler(seam.ListenerInternal).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /metrics = %d, want 200", rec.Code)
	}
}
