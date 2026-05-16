// SPDX-License-Identifier: MIT

package interactionstore_test

import (
	"context"
	"errors"
	"testing"

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
