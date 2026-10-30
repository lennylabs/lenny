// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/gatewayleader"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// handoffElector is a gatewayleader.Elector the test drives: acquire hands
// leadership to Run's onStarted with a fresh leader context, and lose
// cancels that context, as the §12.5 lease does on loss.
type handoffElector struct {
	acquire chan struct{}
	lose    chan struct{}
}

func newHandoffElector() *handoffElector {
	return &handoffElector{acquire: make(chan struct{}), lose: make(chan struct{})}
}

func (e *handoffElector) IsLeader() bool { return false }

func (e *handoffElector) Run(ctx context.Context, onStarted func(context.Context), onStopped func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.acquire:
		}
		lctx, cancel := context.WithCancel(ctx)
		onStarted(lctx)
		select {
		case <-ctx.Done():
		case <-e.lose:
		}
		cancel()
		onStopped()
	}
}

var _ gatewayleader.Elector = (*handoffElector)(nil)

// leaderReplica is one gateway replica's orphan-session reconciler on its own
// gateway-leader gate, recording its terminal hooks separately.
type leaderReplica struct {
	elector *handoffElector
	hook    *claimHook
}

// startLeaderReplica registers a reconciler over store on a gate driven by a
// new elector and starts the gate. The reconciler ticks every few
// milliseconds so a handoff is observed within the test's wait.
func startLeaderReplica(ctx context.Context, store sessionstore.Store) *leaderReplica {
	rep := &leaderReplica{elector: newHandoffElector(), hook: newClaimHook(false)}
	r := orphansession.New(store, sweepTenantList(sweepTenants), orphanMirror{}, orphansession.Options{
		Interval: 5 * time.Millisecond,
		Terminal: rep.hook,
	})
	gate := gatewayleader.NewGate(rep.elector).Add("orphan-session-reconciler", func(ctx context.Context) {
		r.Run(ctx, nil)
	})
	go gate.Run(ctx)
	return rep
}

// orphanTerminal reports whether the row is terminal.
func orphanTerminal(store sessionstore.Store, tenant, id string) bool {
	row, err := store.Get(context.Background(), tenant, id)
	return err == nil && session.IsTerminal(row.State)
}

// spec: §10.1.4, §12.5, §10.1.1 (Primary:)
//
// diagnosis: a failure means the orphan-session reconciler ran on a replica
// that does not hold the gateway-leader lease, kept reconciling after the
// lease was lost, stopped a leader's pass at one tenant's listing fault or
// one row's write fault, failed to finish the faulted rows once a new leader
// took the lease, or ran a session's terminal hook more than once across the
// handoff.
func TestOrphanSessionReconcilerHandsOffFaultedRowsToTheNextLeader_spec_10_1_4(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mem := memstore.New()
	store := newSweepFaultStore(mem)
	seed := func(tenant, id string) {
		sweepCreate(t, mem, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateRunning, PodAssignment: "pod-" + id, PoolRef: "pool-a",
		})
	}
	seed("acme", "a-good")
	seed("acme", "a-bad")
	seed("globex", "g-listed-late")
	store.listErr["globex"] = errSweepListDown
	store.updateErr["a-bad"] = errSweepWriteDown

	first := startLeaderReplica(ctx, store)
	second := startLeaderReplica(ctx, store)

	time.Sleep(30 * time.Millisecond)
	for _, id := range []string{"a-good", "a-bad"} {
		if orphanTerminal(mem, "acme", id) {
			t.Fatalf("%s was failed while no replica held the lease", id)
		}
	}

	first.elector.acquire <- struct{}{}
	if !waitFor(5*time.Second, func() bool { return orphanTerminal(mem, "acme", "a-good") }) {
		t.Fatal("the leader did not fail the orphaned session past the faulted tenant and row")
	}
	time.Sleep(30 * time.Millisecond)
	if orphanTerminal(mem, "acme", "a-bad") || orphanTerminal(mem, "globex", "g-listed-late") {
		t.Fatal("a faulted row or tenant was failed while its fault held")
	}

	first.elector.lose <- struct{}{}
	// A pass in flight when the lease is lost finishes before Run returns;
	// wait past it before the faults heal, so only a reconciler still
	// running after the loss could end the healed rows.
	time.Sleep(30 * time.Millisecond)
	store.heal("globex", "a-bad")
	time.Sleep(50 * time.Millisecond)
	if orphanTerminal(mem, "acme", "a-bad") || orphanTerminal(mem, "globex", "g-listed-late") {
		t.Fatal("the former leader kept reconciling after it lost the lease")
	}

	second.elector.acquire <- struct{}{}
	if !waitFor(5*time.Second, func() bool {
		return orphanTerminal(mem, "acme", "a-bad") && orphanTerminal(mem, "globex", "g-listed-late")
	}) {
		t.Fatal("the new leader did not fail the rows the former leader's faults left")
	}
	for _, rep := range []*leaderReplica{first, second} {
		if !rep.hook.waitClaimsClosed(5 * time.Second) {
			t.Fatal("terminal claims still open after the handoff")
		}
	}
	for id, want := range map[string][2]int{
		"a-good":        {1, 0},
		"a-bad":         {0, 1},
		"g-listed-late": {0, 1},
	} {
		got := [2]int{first.hook.hookCount(id), second.hook.hookCount(id)}
		if got != want {
			t.Errorf("%s terminal hooks (first leader, second leader) = %v, want %v", id, got, want)
		}
	}
}
