// SPDX-License-Identifier: MIT

package correctionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/correctionstore"
)

// spec: §11.2.1 pending billing-correction registry.

func newPending(tenant string, seq uint64) correctionstore.PendingCorrection {
	return correctionstore.PendingCorrection{
		TenantID:         tenant,
		CorrectsSequence: seq,
		ReasonCode:       billingstore.ReasonOperatorManualAdjustment,
		TokensInput:      40,
		SubmittedBy:      "alice@acme.com",
		DualControl:      true,
	}
}

func TestCreateRecordsPendingState(t *testing.T) {
	store := correctionstore.NewMemory()
	got, err := store.Create(context.Background(), newPending("acme", 7))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Error("Create must assign an approval_request_id")
	}
	if got.State != correctionstore.StatePending {
		t.Errorf("a new correction starts pending, got %q", got.State)
	}
	if got.SubmittedAt.IsZero() {
		t.Error("Create must stamp SubmittedAt")
	}
	if got.CorrectsSequence != 7 {
		t.Errorf("CorrectsSequence: got %d, want 7", got.CorrectsSequence)
	}
}

func TestGetUnknownIsNotFound(t *testing.T) {
	store := correctionstore.NewMemory()
	if _, err := store.Get(context.Background(), "ghost"); !errors.Is(err, correctionstore.ErrNotFound) {
		t.Errorf("Get of an unknown id: got %v, want ErrNotFound", err)
	}
}

func TestTransitionToApproved(t *testing.T) {
	store := correctionstore.NewMemory()
	ctx := context.Background()
	created, _ := store.Create(ctx, newPending("acme", 7))

	approved, err := store.Transition(ctx, created.ID, correctionstore.StateApproved,
		func(c *correctionstore.PendingCorrection) {
			c.DecidedBy = "bob@acme.com"
			c.CommittedSequence = 12
		})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if approved.State != correctionstore.StateApproved {
		t.Errorf("state: got %q, want approved", approved.State)
	}
	if approved.DecidedBy != "bob@acme.com" {
		t.Errorf("DecidedBy: got %q, want bob@acme.com", approved.DecidedBy)
	}
	if approved.CommittedSequence != 12 {
		t.Errorf("CommittedSequence: got %d, want 12", approved.CommittedSequence)
	}
}

func TestTransitionRejectsDoubleDecision(t *testing.T) {
	store := correctionstore.NewMemory()
	ctx := context.Background()
	created, _ := store.Create(ctx, newPending("acme", 7))

	if _, err := store.Transition(ctx, created.ID, correctionstore.StateApproved, nil); err != nil {
		t.Fatalf("first Transition: %v", err)
	}
	// A second transition on an already-decided correction is rejected:
	// §11.2.1 a correction cannot be approved or rejected twice.
	_, err := store.Transition(ctx, created.ID, correctionstore.StateRejected, nil)
	if !errors.Is(err, correctionstore.ErrNotPending) {
		t.Errorf("a double decision: got %v, want ErrNotPending", err)
	}
}

func TestListFiltersByStateAndTenant(t *testing.T) {
	store := correctionstore.NewMemory()
	ctx := context.Background()
	acme1, _ := store.Create(ctx, newPending("acme", 1))
	store.Create(ctx, newPending("acme", 2))
	store.Create(ctx, newPending("globex", 3))
	// Approve one acme correction.
	store.Transition(ctx, acme1.ID, correctionstore.StateApproved, nil)

	pending, err := store.List(ctx, correctionstore.Filter{State: correctionstore.StatePending})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending filter: got %d, want 2", len(pending))
	}
	acme, _ := store.List(ctx, correctionstore.Filter{TenantID: "acme"})
	if len(acme) != 2 {
		t.Errorf("acme tenant filter: got %d, want 2", len(acme))
	}
	approved, _ := store.List(ctx, correctionstore.Filter{State: correctionstore.StateApproved})
	if len(approved) != 1 {
		t.Errorf("approved filter: got %d, want 1", len(approved))
	}
}

func TestCountsTracksEveryState(t *testing.T) {
	store := correctionstore.NewMemory()
	ctx := context.Background()
	a, _ := store.Create(ctx, newPending("acme", 1))
	b, _ := store.Create(ctx, newPending("acme", 2))
	store.Create(ctx, newPending("acme", 3)) // stays pending.
	store.Transition(ctx, a.ID, correctionstore.StateApproved, nil)
	store.Transition(ctx, b.ID, correctionstore.StateRejected, nil)

	counts, err := store.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts[correctionstore.StatePending] != 1 {
		t.Errorf("pending count: got %d, want 1", counts[correctionstore.StatePending])
	}
	if counts[correctionstore.StateApproved] != 1 {
		t.Errorf("approved count: got %d, want 1", counts[correctionstore.StateApproved])
	}
	if counts[correctionstore.StateRejected] != 1 {
		t.Errorf("rejected count: got %d, want 1", counts[correctionstore.StateRejected])
	}
	if counts[correctionstore.StateExpired] != 0 {
		t.Errorf("expired count: got %d, want 0", counts[correctionstore.StateExpired])
	}
}

func TestTransitionToExpired(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) }
	store := correctionstore.NewMemoryWithClock(clock)
	ctx := context.Background()
	created, _ := store.Create(ctx, newPending("acme", 7))

	expired, err := store.Transition(ctx, created.ID, correctionstore.StateExpired, nil)
	if err != nil {
		t.Fatalf("Transition to expired: %v", err)
	}
	if expired.State != correctionstore.StateExpired {
		t.Errorf("state: got %q, want expired", expired.State)
	}
	if !expired.State.Terminal() {
		t.Error("expired must be a terminal state")
	}
}
