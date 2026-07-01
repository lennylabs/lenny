// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// errOnCreate is the post-reserve failure point in the §8.2 admission
// pipeline. It embeds a real memstore (so the parent lookup and every
// other Store method behave normally) and forces store.Create to fail, so
// Delegate reaches the §8.2/§12.4 tree-budget reservation, takes it, and
// then errors on the child-row INSERT.
type errOnCreate struct {
	*memstore.Store
	createErr error
}

func (e *errOnCreate) Create(context.Context, sessionstore.Session) error {
	return e.createErr
}

// TestDelegate_BudgetReleasedOnPostReserveError_spec_8_2_18 pins the
// §8.2/§12.4 reservation-release contract that the R7 decomposition
// (proposal 0020) threads across stages: reserveTreeBudget takes the
// reservation inside the insert stage and writes it back to Delegate
// through the **treebudget.Reservation pointer, while the deferred
// release stays on Delegate's named-return path. When a step after the
// reserve fails (here, the child-row store.Create), the deferred release
// must return the reserved slice so a transient downstream error does not
// permanently consume tree budget.
//
// This asserts the corrected OUTCOME of the cross-stage thread-back: the
// tree-size counter is back to zero after the failed delegation. Against
// a broken thread-back (the helper setting a local copy rather than
// *resvOut, or Delegate dropping &budgetReservation), the reservation
// would leak and the counter would stay at 1, failing this test.
//
// spec: §8.2 lines 57, 127; §12.4 line 213. F-8.2.18 / F-8.2.12.
func TestDelegate_BudgetReleasedOnPostReserveError_spec_8_2_18(t *testing.T) {
	base := memstore.New()
	seedParentWithLease(t, base, "sess_parent", &sessionstore.DelegationLease{MaxTreeSize: 10})

	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	reserver := treebudget.New(cl, 0)

	wantErr := errors.New("delegation: simulated INSERT failure after budget reserve")
	store := &errOnCreate{Store: base, createErr: wantErr}

	svc := delegation.NewService(store, delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		TreeBudgetReserver: reserver,
	})

	_, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Delegate err = %v, want the post-reserve INSERT failure %v", err, wantErr)
	}

	// The parent is a root, so the tree counters are keyed by its id.
	// After the deferred release returns the reserved node, the live
	// tree-size counter must be back to zero.
	counters, snapErr := reserver.Snapshot(context.Background(), "sess_parent")
	if snapErr != nil {
		t.Fatalf("snapshot tree counters: %v", snapErr)
	}
	if counters.TreeSize != 0 {
		t.Fatalf("tree-size counter after failed delegation = %d, want 0 (the reservation must be released on the post-reserve error path)", counters.TreeSize)
	}
}
