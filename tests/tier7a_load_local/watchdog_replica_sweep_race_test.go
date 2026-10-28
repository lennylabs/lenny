// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the watchdog sweeps running on every
// gateway replica. Two watchdogs sweep one session set at the same instant
// while a third goroutine lands activity stamps and recovery round trips on
// the same rows. Each terminal write re-checks its state and deadline on the
// locked row, so every session ends with at most one terminal write and
// exactly one terminal hook per terminal write, and a row a concurrent write
// put back inside its deadline is not ended.
//
// spec: §10.1.1 (Primary:), §6.2 (Pod State Machine)
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// sweepRaceStates are the states the seeded rows start in, each with a
// terminal watchdog write that the sweep makes once its deadline passes.
var sweepRaceStates = []session.State{
	session.StateCreated,
	session.StateReady,
	session.StateStarting,
	session.StateRunning,
	session.StateAwaitingClientAction,
}

// terminalCounter is a terminal hook shared by both watchdogs. It counts the
// terminal hook per session.
type terminalCounter struct {
	mu    sync.Mutex
	hooks map[string]int
}

func (*terminalCounter) BeginTerminal(string) func() { return func() {} }

func (c *terminalCounter) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks[sess.ID]++
}

func (c *terminalCounter) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hooks[id]
}

// sweepRaceConfig keeps the per-state budgets at their defaults, sets a short
// idle cap, and keeps the age cap and the suspended controls out of the way.
func sweepRaceConfig() watchdog.Config {
	const off = 100 * 24 * 3600
	return watchdog.Config{
		MaxIdleSeconds:             600,
		MaxSessionAgeSeconds:       off,
		MaxSuspendedPodHoldSeconds: off,
		MaxSuspendedSessionSeconds: off,
	}
}

// diagnosis: a failure means two replicas' watchdog sweeps ended one session
// twice or ran its terminal hook twice, or ended a row that a concurrent write
// put back inside its deadline: the terminal mutator is not re-checking the
// state and the state-entry deadline on the locked row (§10.1.1 Primary:).
func TestTwoWatchdogsEndEachSessionOnce_spec_10_1_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for round := 0; round < 20; round++ {
		runSweepRaceRound(t, rng, now, round)
	}
}

// runSweepRaceRound seeds a session set past every deadline, races two
// watchdog passes against a writer that restarts some rows' deadlines, and
// checks the per-session invariants.
func runSweepRaceRound(t *testing.T, rng *rand.Rand, now time.Time, round int) {
	t.Helper()
	ctx := context.Background()
	store := memstore.NewWithClock(func() time.Time { return now })
	old := now.Add(-time.Hour)
	const n = 60
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("r%d-s%d", round, i)
		st := sweepRaceStates[rng.Intn(len(sweepRaceStates))]
		if err := store.Create(ctx, sessionstore.Session{
			ID: ids[i], TenantID: "acme", State: st, CreatedAt: old, UpdatedAt: old,
			LastAgentActivityAt: old,
		}); err != nil {
			t.Fatalf("seed %s: %v", ids[i], err)
		}
	}
	// restarted records the rows the writer put back inside their deadline
	// before any watchdog write reached them.
	restarted := sync.Map{}
	victims := rng.Perm(n)[:n/3]
	hooks := &terminalCounter{hooks: map[string]int{}}
	clock := func() time.Time { return now }
	w1 := watchdog.New(store, watchdog.StaticTenants{"acme"}, sweepRaceConfig(), clock).WithTerminalHook(hooks)
	w2 := watchdog.New(store, watchdog.StaticTenants{"acme"}, sweepRaceConfig(), clock).WithTerminalHook(hooks)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, w := range []*watchdog.Watchdog{w1, w2} {
		wg.Add(1)
		go func(w *watchdog.Watchdog) {
			defer wg.Done()
			<-start
			if _, err := w.Tick(ctx, now); err != nil {
				t.Errorf("Tick: %v", err)
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for _, i := range victims {
			restartDeadline(ctx, t, store, ids[i], now, &restarted)
		}
	}()
	close(start)
	wg.Wait()

	for _, id := range ids {
		row, err := store.Get(ctx, "acme", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		got := hooks.count(id)
		terminal := session.IsTerminal(row.State)
		switch {
		case terminal && got != 1:
			t.Errorf("%s is %s with %d terminal hooks, want exactly 1", id, row.State, got)
		case !terminal && got != 0:
			t.Errorf("%s is %s with %d terminal hooks, want 0", id, row.State, got)
		}
		if _, ok := restarted.Load(id); ok && terminal {
			t.Errorf("%s was put back inside its deadline before any sweep write and still ended %s (%s)", id, row.State, row.FailureReason)
		}
	}
}

// restartDeadline puts a non-terminal row back inside its deadline: a running
// row gets an activity stamp, and any other row leaves its state for
// resume_pending and re-enters it, which moves its state-entry instant to
// now. No sweep ends a row in resume_pending inside its window, so once the
// first write applies, the row cannot end. A row a watchdog already ended is
// left alone and not recorded.
func restartDeadline(ctx context.Context, t *testing.T, store sessionstore.Store, id string, now time.Time, restarted *sync.Map) {
	t.Helper()
	var entered session.State
	_, err := store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return sessionstore.ErrSuperseded
		}
		entered = r.State
		if r.State == session.StateRunning {
			r.LastAgentActivityAt = now
			return nil
		}
		r.State = session.StateResumePending
		return nil
	})
	if errors.Is(err, sessionstore.ErrSuperseded) {
		return
	}
	if err != nil {
		t.Errorf("restart %s: %v", id, err)
		return
	}
	if entered != session.StateRunning {
		reenterState(ctx, t, store, id, entered)
	}
	restarted.Store(id, true)
}

// reenterState writes st on a row that is not terminal.
func reenterState(ctx context.Context, t *testing.T, store sessionstore.Store, id string, st session.State) {
	t.Helper()
	if _, err := store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return sessionstore.ErrSuperseded
		}
		r.State = st
		return nil
	}); err != nil {
		t.Errorf("move %s to %s: %v", id, st, err)
	}
}
