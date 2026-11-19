// SPDX-License-Identifier: MIT

package mcp_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
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

// TestServerRegistersItsTransportsOnTheClientAndForwardListeners checks that the MCP server registers the WebSocket transport and the environment-scoped endpoint on the client listener and the MCP endpoint on the forward listener.
//
// spec: GWSEAM-RegisterRoutes
func TestServerRegistersItsTransportsOnTheClientAndForwardListeners(t *testing.T) {
	table := routetable.New()
	if err := mcp.NewServer().RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	want := "client * /mcp/v1/ws\nclient POST /mcp/environments/{name}\nforward * /mcp"
	if got := routeKeys(t, table); got != want {
		t.Errorf("routes =\n%s\nwant\n%s", got, want)
	}
}
