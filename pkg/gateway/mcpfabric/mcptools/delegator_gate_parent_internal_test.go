// SPDX-License-Identifier: MIT

package mcptools

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestRequireActiveDelegatorIgnoresTerminalParent_spec_12_8 pins the early
// delegation gate against the parent-terminal fence rule: with clean tenant
// and user markers, a parent in each terminal state passes the gate. The gate
// leaves ParentTerminal unset, so the child's insert transaction is the only
// producer of DELEGATION_PARENT_REVOKED.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
func TestRequireActiveDelegatorIgnoresTerminalParent_spec_12_8(t *testing.T) {
	for _, state := range []session.State{session.StateCancelled, session.StateCompleted, session.StateFailed, session.StateExpired} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			tenants := tenantstore.NewMemory()
			users := userstore.NewMemory()
			if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}); err != nil {
				t.Fatalf("seed tenant: %v", err)
			}
			if err := users.Create(ctx, userstore.User{Subject: "user_alice", TenantID: "acme"}); err != nil {
				t.Fatalf("seed user: %v", err)
			}
			store := memstore.New()
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := store.Create(ctx, sessionstore.Session{
				ID: "sess_parent", TenantID: "acme", UserID: "user_alice", State: state,
				CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatalf("seed parent: %v", err)
			}
			deps := Deps{Store: store, Users: users, Tenants: tenants}
			if err := requireActiveDelegator(ctx, deps, "acme", "sess_parent"); err != nil {
				t.Fatalf("requireActiveDelegator with a %s parent = %v, want nil", state, err)
			}
		})
	}
}
