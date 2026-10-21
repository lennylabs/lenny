// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a randomized interleavings of the §12.8 lifecycle operations against
// concurrent creates on every creation path and concurrent terminal writes.
// In each round one goroutine suspends, re-suspends, and resumes the tenant
// in a random order, sometimes resuming while a suspend's drain runs, and
// then, at random, runs tenant-deletion Phases 1 and 2; a second goroutine erases alice, issuing two racing erase requests;
// create workers create sessions of alice and bob on every creation path;
// and terminal writers end admitted sessions. The lifecyclefence checker
// then asserts the fence's and the drain's invariants.
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecyclefence"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// lockedRand is a goroutine's own random source as a pick function.
func lockedRand(seed int64) func(n int) int {
	r := rand.New(rand.NewSource(seed))
	return r.Intn
}

// tenantLifecycle runs a random sequence of suspends, concurrent double
// suspends, resumes, and suspends whose drain a resume overtakes, then,
// half the time, tenant-deletion Phases 1 and 2. It is the only goroutine
// that resumes the tenant. A resume runs during a suspend's drain only in
// the raced branch, which asserts the resume-during-drain rule in place of
// the drained-scope rule every other suspend keeps: a session the verifying
// listing still sees must be reported in failedSessions or have been
// created after the resume. The checker's one already_suspended=false row
// per marker transition holds across every branch.
func tenantLifecycle(ctx context.Context, w *lifecyclefence.World, pick func(int) int) {
	for i, n := 0, 2+pick(3); i < n; i++ {
		lifecyclefence.Sleep(pick, 1500)
		switch pick(4) {
		case 0:
			if res := w.Suspend(ctx); res.Status != 200 {
				w.Violatef("suspend answered %d: %s", res.Status, res.Body)
			}
		case 1:
			var wg sync.WaitGroup
			for j := 0; j < 2; j++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if res := w.Suspend(ctx); res.Status != 200 {
						w.Violatef("concurrent suspend answered %d: %s", res.Status, res.Body)
					}
				}()
			}
			wg.Wait()
		case 2:
			w.SuspendRacingResume(ctx)
		default:
			if code := w.Resume(ctx); code != 200 {
				w.Violatef("resume answered %d", code)
			}
		}
	}
	if pick(2) == 0 {
		return
	}
	d := w.NewDeletion()
	if err := d.Start(ctx); err != nil {
		w.Violatef("start deletion: %v", err)
		return
	}
	if _, err := d.ThroughPhase2(ctx, 10); err != nil {
		w.Violatef("deletion through Phase 2: %v", err)
	}
}

// userErasure erases alice after a random delay.
func userErasure(ctx context.Context, w *lifecyclefence.World, pick func(int) int) {
	lifecyclefence.Sleep(pick, 2000)
	e := w.NewErasure()
	job, err := e.Start(ctx, "alice")
	if err != nil {
		w.Violatef("start erasure: %v", err)
		return
	}
	if _, err := e.RunToCompletion(ctx, job.ID, 20); err != nil {
		w.Violatef("run erasure: %v", err)
	}
}

// runLifecycleScenario runs one randomized round over a fresh World.
func runLifecycleScenario(t *testing.T, s *lifecyclefence.Stores, master *rand.Rand, creators int) {
	ctx := context.Background()
	w := lifecyclefence.NewWorld(t, s, 2)
	stop := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < creators; i++ {
		pick := lockedRand(master.Int63())
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				path := lifecyclefence.Paths[pick(len(lifecyclefence.Paths))]
				w.Create(ctx, path, lifecyclefence.Users[pick(len(lifecyclefence.Users))])
				lifecyclefence.Sleep(pick, 300)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		pick := lockedRand(master.Int63())
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				w.TerminalWriter(ctx, pick)
				lifecyclefence.Sleep(pick, 500)
			}
		}()
	}
	var ops sync.WaitGroup
	tenantPick, userPick := lockedRand(master.Int63()), lockedRand(master.Int63())
	ops.Add(2)
	go func() { defer ops.Done(); tenantLifecycle(ctx, w, tenantPick) }()
	go func() { defer ops.Done(); userErasure(ctx, w, userPick) }()
	ops.Wait()
	close(stop)
	workers.Wait()
	t.Log(w.Summary())
	w.Check(t)
}

// TestLifecycleFenceInterleavings_spec_12_8 runs randomized rounds, under
// -race, of user erasure, tenant suspension and resume, and tenant-deletion
// Phases 1 and 2 against concurrent creates on every creation path and
// concurrent terminal writes, over the in-memory and the Postgres postures.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18)), 12.8 (Tenant deletion lifecycle), 15.1 (REST API)
// diagnosis: a failure names the broken invariant. A live session after a
// drain or at a phase exit means a create committed into a fenced scope or
// the drain missed a row; residue under a refused or erased session ID
// means a creation path's reservation, undo, or the erasure's reach to
// reserved IDs is broken; an extra already_suspended=false row means
// suspend decides newness outside the row lock; a raced-suspend violation
// means a suspend whose drain a resume overtook either left a session that
// predates the resume live or reported it, or omitted from failedSessions a
// session created after the resume that its verifying listing saw; two
// in-progress jobs mean
// the one-job-per-user rule does not hold on that store.
func TestLifecycleFenceInterleavings_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	master := rand.New(rand.NewSource(seed))
	postures := []struct {
		name     string
		stores   func(t *testing.T) *lifecyclefence.Stores
		rounds   int
		creators int
	}{
		{"memory", func(*testing.T) *lifecyclefence.Stores { return lifecyclefence.MemoryStores() }, 60, 6},
		{"postgres", func(t *testing.T) *lifecyclefence.Stores {
			pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
			s, err := lifecyclefence.PostgresStores(pg.Pool)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}, 8, 4},
	}
	for _, p := range postures {
		t.Run(p.name, func(t *testing.T) {
			s := p.stores(t)
			for r := 0; r < p.rounds; r++ {
				t.Run(fmt.Sprintf("round-%d", r), func(t *testing.T) {
					// Memory postures get fresh stores per round, so no
					// round's jobs or users leak into the next.
					if p.name == "memory" {
						s = lifecyclefence.MemoryStores()
					}
					runLifecycleScenario(t, s, master, p.creators)
				})
			}
		})
	}
}
