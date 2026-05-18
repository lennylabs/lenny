//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §6 / §9.2 pending-interaction registry,
// exercising the Postgres-backed pkg/gateway/interactionstore/pgstore
// against a real container with the production migrations applied.
// Covers the put/get round-trip including the jsonb detail and
// response documents, the sentinel errors, the §15.1
// authorization-triple isolation, the resolve and dismiss lifecycle,
// the §9.1 CountElicitations budget query, and the §11.4 / §12.8
// by-user erasure adapters.
package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/interactionstore/pgstore"
)

// interactionSeed records a pending interaction directed at userID and
// fails the test on error.
func interactionSeed(t *testing.T, ctx context.Context, s interactionstore.Store, tenant, id, sess, userID string, kind interactionstore.Kind) {
	t.Helper()
	if err := s.Put(ctx, interactionstore.Interaction{
		ID: id, Kind: kind, SessionID: sess, TenantID: tenant, UserID: userID,
	}); err != nil {
		t.Fatalf("Put %s: %v", id, err)
	}
}

// spec: 9.2
// diagnosis: the Postgres-backed pending-interaction registry in
// pkg/gateway/interactionstore/pgstore did not behave as specified —
// the Put/Get/Resolve round-trip, the §15.1 authorization-triple
// scoping, ErrAlreadyResolved on double resolution, CountElicitations,
// or the §11.4 DismissByUser / §12.8 DeleteByUser adapters.
func TestInteractionStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := interactionpg.New(pg.Pool)
	ctx := context.Background()

	t.Run("put and get round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		want := interactionstore.Interaction{
			ID:        "tc_1",
			Kind:      interactionstore.KindToolUse,
			SessionID: "sess_1",
			TenantID:  tenant,
			UserID:    "alice",
			Detail: map[string]any{
				"tool": "shell",
				"args": map[string]any{"cmd": "ls"},
			},
		}
		if err := store.Put(ctx, want); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, "sess_1", "alice", "tc_1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Kind != want.Kind || got.UserID != want.UserID ||
			got.SessionID != want.SessionID {
			t.Errorf("scalar field mismatch:\n got %+v\nwant %+v", got, want)
		}
		// Put defaults a missing Phase to pending and stamps CreatedAt.
		if got.Phase != interactionstore.PhasePending {
			t.Errorf("Phase = %q, want pending", got.Phase)
		}
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt was not stamped on Put")
		}
		// The jsonb detail document round-trips.
		if got.Detail["tool"] != "shell" {
			t.Errorf("Detail lost in round-trip: got %+v", got.Detail)
		}
		args, ok := got.Detail["args"].(map[string]any)
		if !ok || args["cmd"] != "ls" {
			t.Errorf("nested Detail lost in round-trip: got %+v", got.Detail)
		}
	})

	t.Run("a re-Put of the same id overwrites the prior row", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "tc_dup", "sess_1", "alice",
			interactionstore.KindToolUse)
		// Re-record the same (tenant, session, id) as an elicitation.
		if err := store.Put(ctx, interactionstore.Interaction{
			ID: "tc_dup", Kind: interactionstore.KindElicitation,
			SessionID: "sess_1", TenantID: tenant, UserID: "alice",
		}); err != nil {
			t.Fatalf("re-Put: %v", err)
		}
		got, err := store.Get(ctx, tenant, "sess_1", "alice", "tc_dup")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Kind != interactionstore.KindElicitation {
			t.Errorf("re-Put did not overwrite: Kind = %q, want elicitation", got.Kind)
		}
	})

	t.Run("get rejects every authorization-triple mismatch", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		other := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "tc_1", "sess_1", "alice",
			interactionstore.KindToolUse)
		// bob may not see alice's interaction.
		if _, err := store.Get(ctx, tenant, "sess_1", "bob", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Errorf("wrong user: got %v, want ErrNotFound", err)
		}
		if _, err := store.Get(ctx, tenant, "sess_other", "alice", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Errorf("wrong session: got %v, want ErrNotFound", err)
		}
		if _, err := store.Get(ctx, other, "sess_1", "alice", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Errorf("cross-tenant: got %v, want ErrNotFound", err)
		}
		if _, err := store.Get(ctx, tenant, "sess_1", "alice", "tc_ghost"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Errorf("unknown id: got %v, want ErrNotFound", err)
		}
	})

	t.Run("resolve approves, stamps ResolvedAt, and persists the response", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "tc_1", "sess_1", "alice",
			interactionstore.KindToolUse)
		out, err := store.Resolve(ctx, tenant, "sess_1", "alice", "tc_1",
			func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseApproved
				in.Response = map[string]any{"decision": "approve"}
				return nil
			})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if out.Phase != interactionstore.PhaseApproved || out.ResolvedAt.IsZero() {
			t.Errorf("resolved: %+v", out)
		}
		got, err := store.Get(ctx, tenant, "sess_1", "alice", "tc_1")
		if err != nil {
			t.Fatalf("Get after Resolve: %v", err)
		}
		if got.Phase != interactionstore.PhaseApproved {
			t.Errorf("persisted phase = %q, want approved", got.Phase)
		}
		resp, ok := got.Response.(map[string]any)
		if !ok || resp["decision"] != "approve" {
			t.Errorf("Response lost in round-trip: got %+v", got.Response)
		}
	})

	t.Run("resolve rejects double resolution and a wrong user", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "tc_1", "sess_1", "alice",
			interactionstore.KindToolUse)
		if _, err := store.Resolve(ctx, tenant, "sess_1", "alice", "tc_1",
			func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseApproved
				return nil
			}); err != nil {
			t.Fatalf("first Resolve: %v", err)
		}
		if _, err := store.Resolve(ctx, tenant, "sess_1", "alice", "tc_1",
			func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseDenied
				return nil
			}); !errors.Is(err, interactionstore.ErrAlreadyResolved) {
			t.Errorf("double resolve: got %v, want ErrAlreadyResolved", err)
		}
		if _, err := store.Resolve(ctx, tenant, "sess_1", "bob", "tc_1",
			func(*interactionstore.Interaction) error { return nil }); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Errorf("wrong-user resolve: got %v, want ErrNotFound", err)
		}
		// A mutate error aborts the write.
		tenant2 := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant2, "tc_2", "sess_1", "alice",
			interactionstore.KindToolUse)
		sentinel := errors.New("mutate boom")
		if _, err := store.Resolve(ctx, tenant2, "sess_1", "alice", "tc_2",
			func(*interactionstore.Interaction) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Errorf("Resolve mutate error: got %v, want sentinel", err)
		}
		got, _ := store.Get(ctx, tenant2, "sess_1", "alice", "tc_2")
		if got.Phase != interactionstore.PhasePending {
			t.Errorf("aborted Resolve left phase = %q, want pending", got.Phase)
		}
	})

	t.Run("CountElicitations counts elicitations across every phase", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "el_1", "sess_1", "alice",
			interactionstore.KindElicitation)
		interactionSeed(t, ctx, store, tenant, "el_2", "sess_1", "alice",
			interactionstore.KindElicitation)
		interactionSeed(t, ctx, store, tenant, "tc_1", "sess_1", "alice",
			interactionstore.KindToolUse)
		interactionSeed(t, ctx, store, tenant, "el_other", "sess_2", "alice",
			interactionstore.KindElicitation)
		// Resolving an elicitation must not drop it from the lifetime cap.
		if _, err := store.Resolve(ctx, tenant, "sess_1", "alice", "el_1",
			func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseResponded
				return nil
			}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		n, err := store.CountElicitations(ctx, tenant, "sess_1")
		if err != nil {
			t.Fatalf("CountElicitations: %v", err)
		}
		if n != 2 {
			t.Errorf("CountElicitations = %d, want 2 (the tool-use and sess_2 elicitation excluded)", n)
		}
		// A session with no interactions counts zero.
		zero, err := store.CountElicitations(ctx, tenant, "sess_empty")
		if err != nil {
			t.Fatalf("CountElicitations empty: %v", err)
		}
		if zero != 0 {
			t.Errorf("CountElicitations of an empty session = %d, want 0", zero)
		}
	})

	t.Run("DeleteByUser erases the user's interactions and is tenant-scoped", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "i1", "sess-1", "alice",
			interactionstore.KindElicitation)
		interactionSeed(t, ctx, store, tenant, "i2", "sess-2", "alice",
			interactionstore.KindToolUse)
		interactionSeed(t, ctx, store, tenant, "i3", "sess-3", "bob",
			interactionstore.KindElicitation)

		deleted, err := store.DeleteByUser(ctx, tenant, "alice")
		if err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if deleted != 2 {
			t.Errorf("deleted = %d, want 2", deleted)
		}
		if _, err := store.Get(ctx, tenant, "sess-1", "alice", "i1"); !errors.Is(err, interactionstore.ErrNotFound) {
			t.Error("alice's interaction i1 should be erased")
		}
		if _, err := store.Get(ctx, tenant, "sess-3", "bob", "i3"); err != nil {
			t.Errorf("bob's interaction must survive alice's erasure: %v", err)
		}
		// Erasing a user with no interactions is a no-op.
		n, err := store.DeleteByUser(ctx, tenant, "nobody")
		if err != nil || n != 0 {
			t.Errorf("DeleteByUser of a user with no interactions = (%d, %v), want (0, nil)", n, err)
		}
	})

	t.Run("DismissByUser dismisses only the user's pending elicitations", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "el1", "sess-1", "alice",
			interactionstore.KindElicitation)
		interactionSeed(t, ctx, store, tenant, "el2", "sess-2", "alice",
			interactionstore.KindElicitation)
		interactionSeed(t, ctx, store, tenant, "tc1", "sess-1", "alice",
			interactionstore.KindToolUse)
		interactionSeed(t, ctx, store, tenant, "el3", "sess-3", "bob",
			interactionstore.KindElicitation)

		dismissed, err := store.DismissByUser(ctx, tenant, "alice")
		if err != nil {
			t.Fatalf("DismissByUser: %v", err)
		}
		if dismissed != 2 {
			t.Errorf("dismissed = %d, want 2 (alice's two pending elicitations)", dismissed)
		}
		for _, c := range []struct{ sess, id string }{{"sess-1", "el1"}, {"sess-2", "el2"}} {
			got, err := store.Get(ctx, tenant, c.sess, "alice", c.id)
			if err != nil {
				t.Fatalf("Get %s: %v", c.id, err)
			}
			if got.Phase != interactionstore.PhaseDismissed {
				t.Errorf("elicitation %s phase = %q, want dismissed", c.id, got.Phase)
			}
			if got.ResolvedAt.IsZero() {
				t.Errorf("elicitation %s ResolvedAt not stamped on dismiss", c.id)
			}
		}
		// A tool-use interaction is not an elicitation — left pending.
		tc, _ := store.Get(ctx, tenant, "sess-1", "alice", "tc1")
		if tc.Phase != interactionstore.PhasePending {
			t.Errorf("tool-use phase = %q, want pending (§11.4 step 7 dismisses elicitations only)", tc.Phase)
		}
		// Bob's elicitation survives alice's revocation.
		bob, _ := store.Get(ctx, tenant, "sess-3", "bob", "el3")
		if bob.Phase != interactionstore.PhasePending {
			t.Errorf("bob's elicitation phase = %q, want pending", bob.Phase)
		}
	})

	t.Run("DismissByUser skips already-resolved elicitations", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		interactionSeed(t, ctx, store, tenant, "el1", "sess-1", "alice",
			interactionstore.KindElicitation)
		if _, err := store.Resolve(ctx, tenant, "sess-1", "alice", "el1",
			func(in *interactionstore.Interaction) error {
				in.Phase = interactionstore.PhaseResponded
				return nil
			}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		dismissed, err := store.DismissByUser(ctx, tenant, "alice")
		if err != nil {
			t.Fatalf("DismissByUser: %v", err)
		}
		if dismissed != 0 {
			t.Errorf("dismissed = %d, want 0 — an already-resolved elicitation is not re-dismissed", dismissed)
		}
		got, _ := store.Get(ctx, tenant, "sess-1", "alice", "el1")
		if got.Phase != interactionstore.PhaseResponded {
			t.Errorf("resolved elicitation phase = %q, want responded (unchanged)", got.Phase)
		}
		// Dismissing a user with no interactions is a no-op.
		n, err := store.DismissByUser(ctx, tenant, "nobody")
		if err != nil || n != 0 {
			t.Errorf("DismissByUser of a user with no interactions = (%d, %v), want (0, nil)", n, err)
		}
	})
}
