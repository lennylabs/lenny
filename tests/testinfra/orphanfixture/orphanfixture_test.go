// SPDX-License-Identifier: MIT

package orphanfixture_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

func counterIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("s%02d", n)
	}
}

// spec: §8.10 (Orphan cleanup)
//
// The convergence invariant names exactly the live delegated sessions under a
// terminal `cancel_all` parent: a live session under a terminal `detach`
// parent, under a live parent, or a live root is not named.
func TestLiveUnderTerminalCancelAllNamesOnlyCancelAllOrphans_spec_8_10(t *testing.T) {
	rows := []sessionstore.Session{
		{ID: "root", State: session.StateCancelled, CascadeOnFailure: session.CascadeCancelAll},
		{ID: "a", ParentSessionID: "root", DelegationDepth: 1, State: session.StateRunning},
		{ID: "d", ParentSessionID: "root", DelegationDepth: 1, State: session.StateCancelled, CascadeOnFailure: session.CascadeDetach},
		{ID: "d1", ParentSessionID: "d", DelegationDepth: 2, State: session.StateRunning},
		{ID: "a1", ParentSessionID: "a", DelegationDepth: 2, State: session.StateRunning},
		{ID: "live", State: session.StateRunning},
	}
	got := orphanfixture.LiveUnderTerminalCancelAll(rows)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("LiveUnderTerminalCancelAll = %v, want [a]", got)
	}
}

// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
//
// The write counter records one terminal write for the applied cancel and
// none for a refused second cancel, and CancelAllReach stops below a
// `detach` node.
func TestWriteCounterCountsAppliedTerminalWritesOnly_spec_7_2(t *testing.T) {
	ctx := context.Background()
	nodes := []orphanfixture.Node{
		{ID: "root", Policy: session.CascadeCancelAll},
		{ID: "a", Parent: "root", Depth: 1, Policy: session.CascadeDetach},
		{ID: "a1", Parent: "a", Depth: 2, Policy: session.CascadeCancelAll},
	}
	store := orphanfixture.NewWriteCounter(memstore.New())
	orphanfixture.Seed(t, store, sessionstore.Session{TenantID: "acme"}, nodes)
	if _, _, err := orphanfixture.Cancel(ctx, store, "acme", "a", time.Now()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, _, err := orphanfixture.Cancel(ctx, store, "acme", "a", time.Now()); err == nil {
		t.Fatalf("second cancel of a terminal row applied")
	}
	if n := store.Writes("a"); n != 1 {
		t.Fatalf("writes(a) = %d, want 1", n)
	}
	reach := orphanfixture.CancelAllReach(nodes)
	if !reach["a"] || reach["a1"] || reach["root"] {
		t.Fatalf("CancelAllReach = %v, want only a", reach)
	}
	tree := orphanfixture.RandomTree(rand.New(rand.NewSource(1)), counterIDs(), 3, 2)
	if tree[0].Depth != 0 || tree[0].Policy != session.CascadeCancelAll {
		t.Fatalf("RandomTree root = %+v", tree[0])
	}
	for i := 1; i < len(tree); i++ {
		if tree[i].Depth < tree[i-1].Depth {
			t.Fatalf("RandomTree not in ascending depth at %d", i)
		}
	}
	if chain := orphanfixture.Chain(counterIDs(), 3, 2); len(chain) != 15 {
		t.Fatalf("Chain(3,2) has %d nodes, want 15", len(chain))
	}
}
