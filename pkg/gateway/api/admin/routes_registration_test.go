// SPDX-License-Identifier: MIT

package admin_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
)

// TestRouterRegistersUnderTheAdminPrefixOnTheClientListener checks that
// the admin router registers one any-method route under /v1/admin/ on the
// client listener, with no route check by the API front.
//
// spec: GWSEAM-RegisterRoutes
func TestRouterRegistersUnderTheAdminPrefixOnTheClientListener(t *testing.T) {
	table := routetable.New()
	if err := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).RegisterRoutes(table); err != nil {
		t.Fatal(err)
	}
	got := table.Routes()
	if len(got) != 1 {
		t.Fatalf("registered %d routes, want 1", len(got))
	}
	r := got[0]
	if r.Pattern != "/v1/admin/" || r.Listener != seam.ListenerClient || r.Method != seam.MethodAny || r.Access != seam.AccessHandler {
		t.Errorf("route = %+v, want any method on /v1/admin/ on the client listener with no API front check", r)
	}
}
