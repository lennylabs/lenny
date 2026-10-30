// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §8.3 budget return. Several
// terminal writers race to end one delegated child: a watchdog-style writer
// through OnSessionTerminal, a cancel_session-style writer through
// AfterTerminalWrite, the parent's cancel_all cascade, and a
// BUDGET_KEYS_EXPIRED sweep. The single-winner terminal write admits one of
// them, and the post-terminal hook of that write returns the child's
// reservation once.
//
// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §11.2
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
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

// spec: §7.1 (Terminal pipeline context), §8.3 (Return on completion), §11.2
// diagnosis: four terminal writers end one delegated child of a root → parent
// → child tree concurrently with randomized start offsets, every hook's
// budget return running against the production return script on miniredis.
// A failure means the child issued no budget return or more than one (the
// return is not owned by the single applied terminal write and its one
// post-terminal hook), the child received two terminal writes, or a terminal
// claim stayed open after the hooks quiesced.
func TestConcurrentTerminalWritersReturnTheChildOnce_spec_8_3(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	for round := 0; round < 100; round++ {
		runReturnOnceRound(t, rng, round)
	}
}

func runReturnOnceRound(t *testing.T, rng *rand.Rand, round int) {
	t.Helper()
	ids := []string{"root", "parent", "child"}
	n := 0
	nodes := orphanfixture.Chain(func() string { id := fmt.Sprintf("r%d_%s", round, ids[n]); n++; return id }, 2, 1)
	rootID, parentID, childID := nodes[0].ID, nodes[1].ID, nodes[2].ID
	writes := orphanfixture.NewWriteCounter(memstore.New())
	archive := orphanfixture.NewArchiveCounter()
	reserver, _ := orphanfixture.NewMiniReserver(t)
	returns := orphanfixture.NewReturnCounter(reserver)
	srv := sessionserver.New(writes, sessionserver.Options{
		DevTenantHeader: true, TreeArchive: archive,
		TreeBudgetReturner:   returns,
		TaskUsage:            resultrollup.New(writes, sessionusage.NewMemory(), archive, nil),
		UsageQuiescenceTimer: orphanfixture.FiredTimer,
	})
	defer srv.Close()
	orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: "acme", RuntimeRef: "echo", PoolRef: "pool-a"}, nodes)
	slices := orphanfixture.AssignSlices(t, writes, "acme", nodes, 1000)
	orphanfixture.ReserveMembers(t, reserver, nodes, slices)
	ctx := context.Background()

	writers := []func(){
		func() { // watchdog, orphan, deadlock, admin, and budget terminator
			end := srv.BeginTerminal(childID)
			defer end()
			if from, row, err := orphanfixture.Cancel(ctx, writes, "acme", childID, time.Now()); err == nil {
				srv.OnSessionTerminal(ctx, from, row)
			}
		},
		func() { // cancel_session, inline with nil signals
			end := srv.BeginTerminal(childID)
			defer end()
			if from, row, err := orphanfixture.Cancel(ctx, writes, "acme", childID, time.Now()); err == nil {
				srv.AfterTerminalWrite(ctx, from, row, nil)
			}
		},
		func() { // the parent's cancel_all cascade
			end := srv.BeginTerminal(parentID)
			defer end()
			if from, row, err := orphanfixture.Cancel(ctx, writes, "acme", parentID, time.Now()); err == nil {
				srv.OnSessionTerminal(ctx, from, row)
			}
		},
		func() { srv.HandleBudgetKeysExpired(ctx, "acme", rootID) },
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, w := range writers {
		delay := time.Duration(rng.Intn(300)) * time.Microsecond
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(delay)
			runtime.Gosched()
			w()
		}()
	}
	close(start)
	wg.Wait()
	if err := srv.WaitTerminalWork(ctx); err != nil {
		t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
	}
	if c := returns.Calls(slices[childID]); c != 1 {
		t.Errorf("round %d: child budget returns = %d, want 1", round, c)
	}
	if w := writes.Writes(childID); w != 1 {
		t.Errorf("round %d: child terminal writes = %d, want 1", round, w)
	}
	row, err := writes.Get(ctx, "acme", childID)
	if err != nil || !session.IsTerminal(row.State) {
		t.Errorf("round %d: child = (%q, %v), want terminal", round, row.State, err)
	}
	if open := srv.OpenTerminalWork(); open != 0 {
		t.Errorf("round %d: OpenTerminalWork = %d after quiescence, want 0", round, open)
	}
}
