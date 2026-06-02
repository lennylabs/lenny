// SPDX-License-Identifier: MIT

package interactionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
)

func seed(t *testing.T, s interactionstore.Store, id, sess, user string, kind interactionstore.Kind) {
	t.Helper()
	err := s.Put(context.Background(), interactionstore.Interaction{
		ID: id, Kind: kind, SessionID: sess, TenantID: "acme", UserID: user,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func TestPutAndGet(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	got, err := s.Get(context.Background(), "acme", "sess_1", "alice", "tc_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Phase != interactionstore.PhasePending {
		t.Errorf("phase: %q", got.Phase)
	}
}

func TestGetRejectsWrongUser(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	// bob may not see alice's interaction.
	if _, err := s.Get(context.Background(), "acme", "sess_1", "bob", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Errorf("wrong user: got %v, want ErrNotFound", err)
	}
}

func TestGetRejectsWrongSession(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	if _, err := s.Get(context.Background(), "acme", "sess_other", "alice", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Errorf("wrong session: got %v, want ErrNotFound", err)
	}
}

func TestGetRejectsWrongTenant(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	if _, err := s.Get(context.Background(), "globex", "sess_1", "alice", "tc_1"); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Errorf("wrong tenant: got %v, want ErrNotFound", err)
	}
}

func TestResolveApprove(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	out, err := s.Resolve(context.Background(), "acme", "sess_1", "alice", "tc_1",
		func(in *interactionstore.Interaction) error {
			in.Phase = interactionstore.PhaseApproved
			return nil
		})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if out.Phase != interactionstore.PhaseApproved || out.ResolvedAt.IsZero() {
		t.Errorf("resolved: %+v", out)
	}
}

func TestResolveRejectsDoubleResolution(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	_, _ = s.Resolve(context.Background(), "acme", "sess_1", "alice", "tc_1",
		func(in *interactionstore.Interaction) error {
			in.Phase = interactionstore.PhaseApproved
			return nil
		})
	_, err := s.Resolve(context.Background(), "acme", "sess_1", "alice", "tc_1",
		func(in *interactionstore.Interaction) error {
			in.Phase = interactionstore.PhaseDenied
			return nil
		})
	if !errors.Is(err, interactionstore.ErrAlreadyResolved) {
		t.Errorf("double resolve: got %v, want ErrAlreadyResolved", err)
	}
}

func TestResolveRejectsWrongUser(t *testing.T) {
	s := interactionstore.NewMemory()
	seed(t, s, "tc_1", "sess_1", "alice", interactionstore.KindToolUse)
	_, err := s.Resolve(context.Background(), "acme", "sess_1", "bob", "tc_1",
		func(*interactionstore.Interaction) error { return nil })
	if !errors.Is(err, interactionstore.ErrNotFound) {
		t.Errorf("wrong-user resolve: got %v, want ErrNotFound", err)
	}
}

func TestDeleteByUserErasesUserInteractions(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	seed(t, s, "i1", "sess-1", "alice", interactionstore.KindElicitation)
	seed(t, s, "i2", "sess-2", "alice", interactionstore.KindToolUse)
	seed(t, s, "i3", "sess-3", "bob", interactionstore.KindElicitation)

	deleted, err := s.DeleteByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	if _, err := s.Get(ctx, "acme", "sess-1", "alice", "i1"); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Error("alice's interaction i1 should be erased")
	}
	if _, err := s.Get(ctx, "acme", "sess-3", "bob", "i3"); err != nil {
		t.Errorf("bob's interaction must survive alice's erasure: %v", err)
	}
}

func TestDeleteByUserNoInteractionsIsNoOp(t *testing.T) {
	s := interactionstore.NewMemory()
	deleted, err := s.DeleteByUser(context.Background(), "acme", "nobody")
	if err != nil || deleted != 0 {
		t.Errorf("DeleteByUser of a user with no interactions = (%d, %v), want (0, nil)", deleted, err)
	}
}

// spec: §12.1 line 5 / §12.8 Phase 4 — DeleteByTenant is mandatory on
// the Store interface (lifted from the concrete type by F-12.2.11) and
// erases exactly one tenant's interactions.
func TestDeleteByTenantScopesToTenant_spec_12_1(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	put := func(id, tenant, user string) {
		t.Helper()
		if err := s.Put(ctx, interactionstore.Interaction{
			ID: id, Kind: interactionstore.KindToolUse, SessionID: "sess-" + id, TenantID: tenant, UserID: user,
		}); err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}
	put("a1", "acme", "alice")
	put("a2", "acme", "bob")
	put("g1", "globex", "carol")

	deleted, err := s.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (acme's two interactions)", deleted)
	}
	if _, err := s.Get(ctx, "globex", "sess-g1", "carol", "g1"); err != nil {
		t.Errorf("globex interaction must survive acme's tenant deletion: %v", err)
	}
	// Idempotent: a second deletion of the now-empty tenant is a no-op.
	if d, err := s.DeleteByTenant(ctx, "acme"); err != nil || d != 0 {
		t.Errorf("repeat DeleteByTenant = (%d, %v), want (0, nil)", d, err)
	}
}

// spec: §11.4 full_revoke — dismiss a revoked user's pending elicitations.

func TestDismissByUserDismissesPendingElicitations(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	seed(t, s, "el1", "sess-1", "alice", interactionstore.KindElicitation)
	seed(t, s, "el2", "sess-2", "alice", interactionstore.KindElicitation)
	seed(t, s, "tc1", "sess-1", "alice", interactionstore.KindToolUse)
	seed(t, s, "el3", "sess-3", "bob", interactionstore.KindElicitation)

	dismissed, err := s.DismissByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DismissByUser: %v", err)
	}
	if dismissed != 2 {
		t.Errorf("dismissed = %d, want 2 (alice's two pending elicitations)", dismissed)
	}
	for _, c := range []struct{ sess, id string }{{"sess-1", "el1"}, {"sess-2", "el2"}} {
		got, err := s.Get(ctx, "acme", c.sess, "alice", c.id)
		if err != nil {
			t.Fatalf("Get %s: %v", c.id, err)
		}
		if got.Phase != interactionstore.PhaseDismissed {
			t.Errorf("elicitation %s phase = %q, want dismissed", c.id, got.Phase)
		}
	}
	// A tool-use interaction is not an elicitation — left pending.
	tc, _ := s.Get(ctx, "acme", "sess-1", "alice", "tc1")
	if tc.Phase != interactionstore.PhasePending {
		t.Errorf("tool-use phase = %q, want pending (§11.4 step 7 dismisses elicitations only)", tc.Phase)
	}
	// Bob's elicitation survives alice's revocation.
	bob, _ := s.Get(ctx, "acme", "sess-3", "bob", "el3")
	if bob.Phase != interactionstore.PhasePending {
		t.Errorf("bob's elicitation phase = %q, want pending", bob.Phase)
	}
}

func TestDismissByUserSkipsAlreadyResolvedElicitations(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	seed(t, s, "el1", "sess-1", "alice", interactionstore.KindElicitation)
	if _, err := s.Resolve(ctx, "acme", "sess-1", "alice", "el1",
		func(in *interactionstore.Interaction) error {
			in.Phase = interactionstore.PhaseResponded
			return nil
		}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	dismissed, err := s.DismissByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DismissByUser: %v", err)
	}
	if dismissed != 0 {
		t.Errorf("dismissed = %d, want 0 — an already-resolved elicitation is not re-dismissed", dismissed)
	}
	got, _ := s.Get(ctx, "acme", "sess-1", "alice", "el1")
	if got.Phase != interactionstore.PhaseResponded {
		t.Errorf("resolved elicitation phase = %q, want responded (unchanged)", got.Phase)
	}
}

func TestDismissByUserNoInteractionsIsNoOp(t *testing.T) {
	s := interactionstore.NewMemory()
	dismissed, err := s.DismissByUser(context.Background(), "acme", "nobody")
	if err != nil || dismissed != 0 {
		t.Errorf("DismissByUser of a user with no interactions = (%d, %v), want (0, nil)", dismissed, err)
	}
}

// TestListPendingReturnsOnlyPendingOldestFirst covers the §7.2 line
// 153 lookup used by `children_reattached`. The list omits resolved
// rows and orders by CreatedAt ascending so the resumed parent
// addresses the longest-waiting request first.
// spec: §7.2 line 153; F-7.2.16.
func TestListPendingReturnsOnlyPendingOldestFirst(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert in reverse chronological order so a naive iteration would
	// surface the newer one first.
	mustPut := func(id string, at time.Time, phase interactionstore.Phase) {
		err := s.Put(ctx, interactionstore.Interaction{
			ID: id, Kind: interactionstore.KindElicitation,
			SessionID: "sess_x", TenantID: "acme", UserID: "alice",
			Phase: phase, CreatedAt: at,
		})
		if err != nil {
			t.Fatalf("Put %s: %v", id, err)
		}
	}
	mustPut("el_new", base.Add(time.Hour), interactionstore.PhasePending)
	mustPut("el_old", base, interactionstore.PhasePending)
	mustPut("el_resolved", base.Add(-time.Hour), interactionstore.PhaseResponded)

	got, err := s.ListPending(ctx, "acme", "sess_x")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListPending count = %d, want 2 (resolved excluded)", len(got))
	}
	if got[0].ID != "el_old" || got[1].ID != "el_new" {
		t.Errorf("order = [%s, %s], want [el_old, el_new]", got[0].ID, got[1].ID)
	}
}

// TestListPendingEmptyForUnknownSession — a session with no
// interactions returns an empty slice and no error.
// spec: §7.2 line 153; F-7.2.16.
func TestListPendingEmptyForUnknownSession(t *testing.T) {
	s := interactionstore.NewMemory()
	got, err := s.ListPending(context.Background(), "acme", "sess_empty")
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListPending on unknown session = %d entries, want 0", len(got))
	}
}

// TestListPendingScopedByTenantAndSession — entries leak across
// neither boundary.
// spec: §7.2 line 153 (tenant/session triple); F-7.2.16.
func TestListPendingScopedByTenantAndSession(t *testing.T) {
	s := interactionstore.NewMemory()
	ctx := context.Background()
	seed(t, s, "tc_a", "sess_a", "alice", interactionstore.KindToolUse)
	seed(t, s, "tc_b", "sess_b", "alice", interactionstore.KindToolUse)
	// Tenant boundary: same session id, different tenant.
	if err := s.Put(ctx, interactionstore.Interaction{
		ID: "tc_other", Kind: interactionstore.KindToolUse,
		SessionID: "sess_a", TenantID: "globex", UserID: "alice",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, _ := s.ListPending(ctx, "acme", "sess_a")
	if len(got) != 1 || got[0].ID != "tc_a" {
		t.Errorf("ListPending acme/sess_a = %+v, want [tc_a]", got)
	}
	got, _ = s.ListPending(ctx, "globex", "sess_a")
	if len(got) != 1 || got[0].ID != "tc_other" {
		t.Errorf("ListPending globex/sess_a = %+v, want [tc_other]", got)
	}
}
