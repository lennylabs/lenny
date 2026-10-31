// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// registrySealer stands in for the production Checkpointer: it sends a
// Checkpoint RPC through the session's local binding and returns nil when the
// registry holds none. Each RPC is recorded with the sandbox it reached.
type registrySealer struct {
	registry *podsession.Registry
	mu       sync.Mutex
	rpcs     []string
}

func (r *registrySealer) Seal(_ context.Context, _, sessionID string) error {
	bind, ok := r.registry.Get(sessionID)
	if !ok {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rpcs = append(r.rpcs, "checkpoint:"+bind.SandboxName)
	return nil
}

func (r *registrySealer) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.rpcs...)
}

// boundFromStates are the pre-terminal states in which a coordinator binds a
// pod, from running onward; the hook's stale-binding step covers each.
var boundFromStates = []session.State{
	session.StateRunning, session.StateInputRequired, session.StateSuspended,
	session.StateResumePending, session.StateResuming, session.StateAwaitingClientAction,
}

// TestPostTerminalHookEvictsAStaleBindingBeforeTheSeal_spec_10_1_5 runs the
// post-terminal hook over a table of the local binding {stamped at the row's
// generation, below it, at 0, absent} by every pre-terminal state from
// running onward. A stale binding is evicted before the seal: the hook sends
// the pod no Checkpoint RPC and makes no binder call, because the replica
// that advanced the generation owns the pod's release. A binding at the
// row's generation is sealed and released, and an absent binding gets
// neither.
//
// spec: 10.1.1 (Generation counters:), 10.1.5 (Stale Replica Behavior),
// 7.3 (Resume driver)
func TestPostTerminalHookEvictsAStaleBindingBeforeTheSeal_spec_10_1_5(t *testing.T) {
	const rowGen = 3
	for _, from := range boundFromStates {
		for _, tc := range []struct {
			name  string
			stamp int64 // -1 leaves the session unbound
			stale bool
		}{
			{"at", rowGen, false},
			{"below", rowGen - 1, true},
			{"zero", 0, true},
			{"absent", -1, false},
		} {
			t.Run(fmt.Sprintf("%s/%s", from, tc.name), func(t *testing.T) {
				f := newFunnelFixture(t, funnelOpts{})
				sealer := &registrySealer{registry: f.registry}
				f.srv.sealer = sealer
				stamp := tc.stamp
				if stamp < 0 {
					stamp = rowGen
				}
				row, _ := bindAt(t, f, "s", session.StateCancelled, rowGen, stamp)
				if tc.stamp < 0 {
					f.registry.Remove("s")
				}

				f.srv.AfterTerminalWrite(context.Background(), from, row, nil)

				if _, ok := f.registry.Get("s"); ok {
					t.Error("the hook left a binding registered")
				}
				rpcs, events := sealer.calls(), f.log.list()
				switch {
				case tc.stale:
					if len(rpcs) != 0 {
						t.Errorf("Checkpoint RPCs = %v, want none through a stale binding", rpcs)
					}
					if len(events) != 0 {
						t.Errorf("release events = %v, want no release of a stale binding", events)
					}
				case tc.stamp < 0:
					if len(rpcs) != 0 || len(events) != 0 {
						t.Errorf("RPCs %v, events %v; want none for an unbound session", rpcs, events)
					}
				default:
					if len(rpcs) != 1 || rpcs[0] != "checkpoint:pod-x" {
						t.Errorf("Checkpoint RPCs = %v, want one to pod-x", rpcs)
					}
					if len(events) != 1 || events[0] != "release:cancelled" {
						t.Errorf("release events = %v, want [release:cancelled]", events)
					}
				}
				for _, c := range f.clientCalls.list() {
					if tc.stale || tc.stamp < 0 {
						t.Errorf("binder client call %q, want none", c)
					}
				}
			})
		}
	}
}

// TestStaleBindingStepLeavesPreRunningStatesAlone_spec_10_1_5 asserts the
// stale-binding step does nothing for a pre-running fromState, whose pod the
// pre-running reclaim settles, and nothing without a registry.
//
// spec: 10.1.5 (Stale Replica Behavior), 7.3 (Resume driver)
func TestStaleBindingStepLeavesPreRunningStatesAlone_spec_10_1_5(t *testing.T) {
	for _, from := range []session.State{session.StateCreated, session.StateFinalizing, session.StateReady} {
		t.Run(string(from), func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			row, bind := bindAt(t, f, "s", session.StateCancelled, 3, 0)
			f.srv.settleStaleBinding(from, row)
			if got, ok := f.registry.Get("s"); !ok || got != bind {
				t.Error("the step evicted a binding for a pre-running fromState")
			}
		})
	}
	t.Run("no registry", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{noRegistry: true})
		f.srv.settleStaleBinding(session.StateRunning, sessionstore.Session{UserID: testOwner, ID: "s", CoordinationGeneration: 3})
	})
}

// TestBindingStaleCallSitesAgree_spec_10_1_1 drives every call site of the
// stale-binding predicate over a grid of binding and row generations: the
// stream-failure report's peer check, the terminal binding release, and the
// post-terminal hook's stale-binding step each treat a binding as stale
// exactly when bindingStale does.
//
// spec: 10.1.1 (Generation counters:), 10.1.5 (Stale Replica Behavior)
func TestBindingStaleCallSitesAgree_spec_10_1_1(t *testing.T) {
	gens := []int64{0, 1, 2, 3}
	for _, bindGen := range gens {
		for _, rowGen := range gens {
			want := bindGen == 0 || rowGen > bindGen
			name := fmt.Sprintf("bind_%d/row_%d", bindGen, rowGen)
			t.Run(name, func(t *testing.T) {
				if got := bindingStale(bindGen, rowGen); got != want {
					t.Fatalf("bindingStale = %v, want %v", got, want)
				}
				peer := reportSupersededByPeer(sessionstore.Session{UserID: testOwner, ID: "s", State: session.StateRunning, CoordinationGeneration: rowGen},
					&podsession.BindResult{CoordinationGeneration: bindGen})
				if peer != want {
					t.Errorf("reportSupersededByPeer = %v, want %v", peer, want)
				}

				f := newFunnelFixture(t, funnelOpts{})
				row, _ := bindAt(t, f, "s", session.StateCancelled, rowGen, bindGen)
				_, _ = f.srv.releaseTerminalBinding(context.Background(), row)
				if evicted := !releasedIn(f.log.list()); evicted != want {
					t.Errorf("releaseTerminalBinding evicted = %v, want %v (events %v)", evicted, want, f.log.list())
				}

				g := newFunnelFixture(t, funnelOpts{})
				row, _ = bindAt(t, g, "s", session.StateCancelled, rowGen, bindGen)
				g.srv.settleStaleBinding(session.StateRunning, row)
				if _, ok := g.registry.Get("s"); ok == want {
					t.Errorf("settleStaleBinding evicted = %v, want %v", !ok, want)
				}
			})
		}
	}
}

func releasedIn(events []string) bool {
	for _, e := range events {
		if strings.HasPrefix(e, "release:") {
			return true
		}
	}
	return false
}
