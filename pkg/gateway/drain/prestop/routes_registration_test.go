// SPDX-License-Identifier: MIT

package prestop_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/drain/prestop"
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

// TestHookRegistersOnTheInternalListener checks that the preStop hook registers for POST and GET on the internal listener.
//
// spec: GWSEAM-RegisterRoutes
func TestHookRegistersOnTheInternalListener(t *testing.T) {
	table := routetable.New()
	if err := (&prestop.Hook{}).RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	if got, want := routeKeys(t, table), "internal GET /internal/prestop\ninternal POST /internal/prestop"; got != want {
		t.Errorf("routes =\n%s\nwant\n%s", got, want)
	}
}
