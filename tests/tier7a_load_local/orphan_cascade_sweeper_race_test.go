// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the §8.10 cancel_all cascade
// racing orphan cleanup. The gateway's inline cascade and two orphan-cleanup
// sweepers, as two replicas run them, walk one delegation tree concurrently
// over one session store after its root was cancelled. The locked-row guard
// in every writer's terminal Update admits one write per session, and the
// terminal claim gives each applied write one post-terminal hook.
//
// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
// diagnosis: the inline cancel_all cascade and two orphan-cleanup sweepers
// run as goroutines with randomized start offsets over one store after a
// randomized tree's root was cancelled. A failure means two writers both
// applied a terminal write to one session or both ran its post-terminal hook
// (the locked-row guard or the terminal claim does not fence the cascade
// against the sweeper), or a live delegated session was left under a terminal
// cancel_all parent once the writers quiesced.
func TestCascadeAndTwoSweepersEndEachSessionOnce_spec_8_10(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	const maxDepth = 3
	for round := 0; round < 60; round++ {
		n := 0
		newID := func() string { n++; return fmt.Sprintf("r%d_s%02d", round, n) }
		nodes := orphanfixture.RandomTree(rng, newID, maxDepth, 3)
		writes := orphanfixture.NewWriteCounter(memstore.New())
		archive := orphanfixture.NewArchiveCounter()
		srv := sessionserver.New(writes, sessionserver.Options{DevTenantHeader: true, TreeArchive: archive})
		orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: "acme", RuntimeRef: "echo"}, nodes)
		ctx := context.Background()

		end := srv.BeginTerminal(nodes[0].ID)
		from, row, err := orphanfixture.Cancel(ctx, writes, "acme", nodes[0].ID, time.Now())
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		tenants := orphancleanup.StaticTenants{"acme"}
		actors := []func(){
			func() { defer end(); srv.OnSessionTerminal(ctx, from, row) },
		}
		for i := 0; i < 2; i++ {
			sw := orphancleanup.New(writes, tenants, orphancleanup.Options{Terminal: srv})
			actors = append(actors, func() {
				if _, err := sw.Tick(ctx, time.Now()); err != nil {
					t.Errorf("round %d: Tick: %v", round, err)
				}
			})
		}
		offsets := make([]time.Duration, len(actors))
		for i := range offsets {
			offsets[i] = time.Duration(rng.Intn(200)) * time.Microsecond
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, act := range actors {
			wg.Add(1)
			go func(act func(), delay time.Duration) {
				defer wg.Done()
				<-start
				time.Sleep(delay)
				runtime.Gosched()
				act()
			}(act, offsets[i])
		}
		close(start)
		wg.Wait()
		if err := srv.WaitTerminalWork(ctx); err != nil {
			t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
		}

		// Quiesce: further passes end whatever the racing passes listed
		// before an ancestor's end committed.
		sw := orphancleanup.New(writes, tenants, orphancleanup.Options{Terminal: srv})
		converged := false
		for pass := 0; pass <= maxDepth+1; pass++ {
			rows, err := writes.List(ctx, "acme", sessionstore.ListFilter{})
			if err != nil {
				t.Fatalf("round %d: list: %v", round, err)
			}
			if len(orphanfixture.LiveUnderTerminalCancelAll(rows)) == 0 {
				converged = true
				break
			}
			if _, err := sw.Tick(ctx, time.Now()); err != nil {
				t.Fatalf("round %d: Tick: %v", round, err)
			}
			if err := srv.WaitTerminalWork(ctx); err != nil {
				t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
			}
		}
		if !converged {
			t.Fatalf("round %d: live sessions remain under a terminal cancel_all parent after quiescence", round)
		}
		orphanfixture.CheckOnce(t, nodes, writes, archive.Hooks, orphanfixture.CancelAllReach(nodes))
	}
}
