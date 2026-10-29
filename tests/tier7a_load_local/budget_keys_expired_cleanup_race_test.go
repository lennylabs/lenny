// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §8.3 BUDGET_KEYS_EXPIRED
// tree cleanup. Several reservation detections for one root, as concurrent
// delegate_task calls on two replicas meet them, run the cleanup at once
// over one session store, racing the root's own cancel_all cascade. The root
// write's locked-row guard admits one writer, so the root fails once and one
// event is emitted, while every detection is counted and the guarded cancels
// end each member once. Each cancelled member's post-terminal hook runs its
// budget return against the production return script on miniredis, so each
// member returns once, and a return that meets the lost sentinel recreates no
// key and starts no further cleanup.
//
// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §8.10
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

const budgetKeysExpiredEventType = "dev.lenny.delegation.budget_keys_expired"

// keysExpiredRecorder counts the delegation.budget_keys_expired operational
// events and the detection-counter increments by label pair.
type keysExpiredRecorder struct {
	mu     sync.Mutex
	events int
	incs   map[string]int
}

func (r *keysExpiredRecorder) Emit(_ context.Context, e events.OperationalEvent) error {
	if e.Type != budgetKeysExpiredEventType {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events++
	return nil
}

func (r *keysExpiredRecorder) inc(pool, tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.incs[pool+"/"+tenantID]++
}

func (r *keysExpiredRecorder) snapshot() (int, map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.incs))
	for k, v := range r.incs {
		out[k] = v
	}
	return r.events, out
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §7.1, §8.10
// diagnosis: between two and eight HandleBudgetKeysExpired calls for one
// running root run as goroutines with randomized start offsets over a
// randomized tree. A failure means the root write applied more than once or
// carried the wrong failure class (the root guard does not fence concurrent
// detections), more than one or no delegation.budget_keys_expired event was
// emitted (the event is not tied to the applied root write), a detection was
// not counted under the root's pool, a member received two terminal writes or
// two post-terminal hooks, a member was left live after the cleanup
// quiesced, a member's budget return ran twice or not at all (the return is
// not tied to the single applied terminal write), or a return that met the
// absent sentinel created or modified a budget key or was counted as a
// detection.
func TestBudgetKeysExpiredConcurrentDetectionsEndTheTreeOnce_spec_8_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < 60; round++ {
		n := 0
		newID := func() string { n++; return fmt.Sprintf("r%d_s%02d", round, n) }
		nodes := orphanfixture.RandomTree(rng, newID, 3, 3)
		writes := orphanfixture.NewWriteCounter(memstore.New())
		archive := orphanfixture.NewArchiveCounter()
		rec := &keysExpiredRecorder{incs: map[string]int{}}
		reserver, mr := orphanfixture.NewMiniReserver(t)
		returns := orphanfixture.NewReturnCounter(reserver)
		srv := sessionserver.New(writes, sessionserver.Options{
			DevTenantHeader: true, TreeArchive: archive, OpsEmitter: rec,
			IncDelegationBudgetKeysExpired: rec.inc,
			TreeBudgetReturner:             returns,
			TaskUsage:                      resultrollup.New(writes, sessionusage.NewMemory(), archive, nil),
			UsageQuiescenceTimer:           orphanfixture.FiredTimer,
		})
		orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: "acme", RuntimeRef: "echo", PoolRef: "pool-a"}, nodes)
		rootID := nodes[0].ID
		ctx := context.Background()
		slices := orphanfixture.AssignSlices(t, writes, "acme", nodes, 1000)
		orphanfixture.ReserveMembers(t, reserver, nodes, slices)
		loss := orphanfixture.KeyLoss(rng.Intn(3))
		orphanfixture.LoseKeys(mr, rootID, loss)
		before := orphanfixture.BudgetKeys(mr, rootID)

		detections := 2 + rng.Intn(7)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < detections; i++ {
			delay := time.Duration(rng.Intn(300)) * time.Microsecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				time.Sleep(delay)
				runtime.Gosched()
				srv.HandleBudgetKeysExpired(ctx, "acme", rootID)
			}()
		}
		close(start)
		wg.Wait()
		if err := srv.WaitTerminalWork(ctx); err != nil {
			t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
		}

		root, err := writes.Get(ctx, "acme", rootID)
		if err != nil {
			t.Fatalf("round %d: get root: %v", round, err)
		}
		if root.State != session.StateFailed || root.FailureClass != session.FailureClassBudgetKeysExpired {
			t.Errorf("round %d: root = (%q, %q), want (failed, budget_keys_expired)", round, root.State, root.FailureClass)
		}
		if w := writes.Writes(rootID); w != 1 {
			t.Errorf("round %d: root terminal writes = %d, want 1", round, w)
		}
		evs, incs := rec.snapshot()
		if evs != 1 {
			t.Errorf("round %d: delegation.budget_keys_expired events = %d, want 1", round, evs)
		}
		if len(incs) != 1 || incs["pool-a/acme"] != detections {
			t.Errorf("round %d: detection counter = %v, want pool-a/acme=%d", round, incs, detections)
		}
		exact := map[string]bool{}
		for _, node := range nodes[1:] {
			exact[node.ID] = true
			row, err := writes.Get(ctx, "acme", node.ID)
			if err != nil {
				t.Fatalf("round %d: get %s: %v", round, node.ID, err)
			}
			if !session.IsTerminal(row.State) {
				t.Errorf("round %d: member %s is %q after the cleanup, want terminal", round, node.ID, row.State)
			}
		}
		orphanfixture.CheckOnce(t, nodes, writes, archive.Hooks, exact)
		orphanfixture.CheckReturns(t, fmt.Sprintf("round %d (key loss %d)", round, loss), mr, rootID, slices, returns, loss, before)
		if open := srv.OpenTerminalWork(); open != 0 {
			t.Errorf("round %d: OpenTerminalWork = %d after quiescence, want 0", round, open)
		}
		srv.Close()
	}
}
