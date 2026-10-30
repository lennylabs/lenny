// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionstate "github.com/lennylabs/lenny/pkg/session/state"
)

// spec: §7.2 (Terminal writes from any non-terminal state), §8.10 (Orphan cleanup)
//
// Orphan cleanup cancels an orphan of every non-terminal state under a
// terminal `cancel_all` parent, and the §7.2 state contract admits each edge
// the sweeper writes. Before the contract carried the → cancelled any-state
// edge, a created, finalizing, ready, or starting orphan's cancel was an edge
// the contract rejected.
func TestOrphanCancelEdgeIsValidFromEveryNonTerminalState_spec_7_2(t *testing.T) {
	for _, from := range session.AllStates() {
		if session.IsTerminal(from) {
			continue
		}
		from := from
		t.Run(string(from), func(t *testing.T) {
			store := memstore.New()
			seedNodes(
				t, store,
				node{id: "root", state: session.StateRunning, policy: session.CascadeDetach},
				node{id: "parent", parent: "root", depth: 1, state: session.StateFailed, policy: session.CascadeCancelAll, terminatedAt: dispBase},
				node{id: "child", parent: "parent", depth: 2, state: from},
			)
			if n, _ := tick(t, store, dispBase.Add(time.Second)); n != 1 {
				t.Fatalf("pass ended %d orphans, want 1", n)
			}
			got, _ := stateOf(t, store, "child")
			if got != session.StateCancelled {
				t.Fatalf("child = %q, want cancelled", got)
			}
			if err := sessionstate.IsValid(sessionstate.State(from), sessionstate.State(got)); err != nil {
				t.Errorf("sweeper wrote %q → %q, which the §7.2 contract rejects: %v", from, got, err)
			}
		})
	}
}
