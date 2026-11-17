//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
)

// spec: 9.2 (Elicitation Chain), 8.2 (Delegation Mechanism)
// diagnosis: a failure means the Postgres expiry of a session's raised
// elicitations deletes a row it must keep (a child's elicitation held for the
// session, a resolved one, a lease-extension elicitation, a tool-use row, or
// another tenant's row), leaves a raised pending elicitation answerable, or
// scans the tenant's resolved interactions instead of the pending-origin
// partial index.
func TestInteractionStoreExpireRaisedBy_spec_9_2(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := interactionpg.New(pg.Pool)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	other := freshTenant(t, ctx, pg)
	put := func(in interactionstore.Interaction) {
		t.Helper()
		if err := store.Put(ctx, in); err != nil {
			t.Fatalf("Put %s: %v", in.ID, err)
		}
	}
	origin := func(id string) map[string]any { return map[string]any{interactionstore.DetailOriginPod: id} }
	put(interactionstore.Interaction{ID: "raised", Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: origin("sess-a")})
	put(interactionstore.Interaction{ID: "child", Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: origin("sess-child")})
	put(interactionstore.Interaction{ID: "lease-ext", Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: map[string]any{"message": "extend?"}})
	put(interactionstore.Interaction{ID: "tool", Kind: interactionstore.KindToolUse, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: origin("sess-a")})
	put(interactionstore.Interaction{ID: "other-tenant", Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: other, UserID: "bob", Detail: origin("sess-a")})
	put(interactionstore.Interaction{ID: "answered", Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: origin("sess-a")})
	if _, err := store.Resolve(ctx, tenant, "sess-a", "alice", "answered", func(in *interactionstore.Interaction) error {
		in.Phase = interactionstore.PhaseResponded
		return nil
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Resolved rows the delete must not read.
	for i := range 200 {
		id := fmt.Sprintf("old-%d", i)
		put(interactionstore.Interaction{ID: id, Kind: interactionstore.KindElicitation, SessionID: "sess-a", TenantID: tenant, UserID: "alice", Detail: origin("sess-a")})
		if _, err := store.Resolve(ctx, tenant, "sess-a", "alice", id, func(in *interactionstore.Interaction) error {
			in.Phase = interactionstore.PhaseResponded
			return nil
		}); err != nil {
			t.Fatalf("Resolve %s: %v", id, err)
		}
	}

	n, err := store.ExpireRaisedBy(ctx, tenant, "sess-a")
	if err != nil || n != 1 {
		t.Fatalf("ExpireRaisedBy = %d, %v; want 1", n, err)
	}
	if _, err := store.Resolve(ctx, tenant, "sess-a", "alice", "raised", func(*interactionstore.Interaction) error { return nil }); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Fatalf("Resolve of the expired elicitation = %v, want ErrNotFound", err)
	}
	for _, id := range []string{"child", "lease-ext", "tool", "answered"} {
		if _, err := store.Get(ctx, tenant, "sess-a", "alice", id); err != nil {
			t.Errorf("row %s was removed: %v", id, err)
		}
	}
	if _, err := store.Get(ctx, other, "sess-a", "bob", "other-tenant"); err != nil {
		t.Errorf("another tenant's row was removed: %v", err)
	}

	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatalf("set: %v", err)
	}
	plan := explain(t, ctx, tx, `EXPLAIN DELETE FROM interactions WHERE tenant_id = $1 AND kind = 'elicitation'
		AND phase = 'pending' AND detail->>'originPod' = $2`, tenant, "sess-a")
	if !strings.Contains(plan, "idx_interactions_pending_elicitation_origin") {
		t.Errorf("plan does not use idx_interactions_pending_elicitation_origin:\n%s", plan)
	}
}
