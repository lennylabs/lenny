// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the watchdog sweeps running on every
// gateway replica. Two watchdogs sweep one session set at the same instant
// while a third goroutine lands activity stamps and recovery round trips on
// the same rows. Each terminal write re-checks its state and deadline on the
// locked row, so every session ends with at most one terminal write and
// exactly one terminal hook per terminal write, and a row a concurrent write
// put back inside its deadline is not ended. A second variant adds orphan
// cleanup over children of a root that ended past the cascade window, so
// three sweepers race for the same children.
//
// spec: §10.1.1 (Primary:), §6.2 (Pod State Machine), §8.10 (Delegation Tree
// Recovery)
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
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
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

// terminalCounter is a terminal hook shared by every sweeper. It counts the
// terminal hook per session and tracks the open terminal claims, which the
// sweepers hold until their dispatched hooks return.
type terminalCounter struct {
	mu     sync.Mutex
	hooks  map[string]int
	claims sync.WaitGroup
}

func (c *terminalCounter) BeginTerminal(string) func() {
	c.claims.Add(1)
	var once sync.Once
	return func() { once.Do(c.claims.Done) }
}

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

// spec: §10.1.1 (Primary:), §7.2 (Session state machine)
//
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
		runSweepRaceRound(t, rng, now, round, false)
	}
}

// spec: §10.1.1 (Primary:), §8.10 (Delegation Tree Recovery)
//
// diagnosis: a failure means two replicas' watchdogs and orphan cleanup,
// racing over children of a root past its cascade window, made more than one
// terminal write or ran more than one terminal hook for a session, or left a
// terminal claim open after its dispatched hook: the watchdog's locked-row
// re-check or orphan cleanup's non-terminal guard is not holding (§10.1.1
// Primary:).
func TestTwoWatchdogsAndOrphanCleanupEndEachSessionOnce_spec_10_1_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for round := 0; round < 20; round++ {
		runSweepRaceRound(t, rng, now, round, true)
	}
}

// sweepRaceRoot is the ended root whose children orphan cleanup expires.
const sweepRaceRoot = "root"

// terminalWriteCounter counts, per session, the Updates that applied a
// transition from a non-terminal to a terminal state.
type terminalWriteCounter struct {
	sessionstore.Store
	mu     sync.Mutex
	writes map[string]int
}

func (c *terminalWriteCounter) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	var ended bool
	row, err := c.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		was := session.IsTerminal(r.State)
		if err := mut(r); err != nil {
			return err
		}
		ended = !was && session.IsTerminal(r.State)
		return nil
	})
	if err == nil && ended {
		c.mu.Lock()
		c.writes[id]++
		c.mu.Unlock()
	}
	return row, err
}

func (c *terminalWriteCounter) count(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes[id]
}

// runSweepRaceRound seeds a session set past every deadline, races two
// watchdog passes (and, with orphans, an orphan-cleanup pass over children
// of an ended root) in a random start order against a writer that restarts
// some rows' deadlines, waits for every dispatched terminal hook, and checks
// the per-session invariants.
func runSweepRaceRound(t *testing.T, rng *rand.Rand, now time.Time, round int, orphans bool) {
	t.Helper()
	ctx := context.Background()
	mem := memstore.NewWithClock(func() time.Time { return now })
	store := &terminalWriteCounter{Store: mem, writes: map[string]int{}}
	old := now.Add(-time.Hour)
	if orphans {
		// The root is seeded live and ended after its children exist, because
		// the store's creation fence refuses a delegated child under a
		// terminal parent.
		if err := mem.Create(ctx, sessionstore.Session{
			ID: sweepRaceRoot, TenantID: "acme", State: session.StateRunning,
			CascadeOnFailure: session.CascadeDetach,
			CreatedAt:        old.Add(-2 * time.Hour), UpdatedAt: old,
		}); err != nil {
			t.Fatalf("seed root: %v", err)
		}
	}
	const n = 60
	ids := make([]string, n)
	child := make([]bool, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("r%d-s%d", round, i)
		st := sweepRaceStates[rng.Intn(len(sweepRaceStates))]
		row := sessionstore.Session{
			ID: ids[i], TenantID: "acme", State: st, CreatedAt: old, UpdatedAt: old,
			LastAgentActivityAt: old,
		}
		if orphans && rng.Intn(2) == 0 {
			row.ParentSessionID, row.DelegationDepth, child[i] = sweepRaceRoot, 1, true
		}
		if err := mem.Create(ctx, row); err != nil {
			t.Fatalf("seed %s: %v", ids[i], err)
		}
	}
	if orphans {
		if _, err := mem.Update(ctx, "acme", sweepRaceRoot, func(r *sessionstore.Session) error {
			r.State, r.TerminatedAt = session.StateCompleted, old.Add(-time.Hour)
			return nil
		}); err != nil {
			t.Fatalf("end root: %v", err)
		}
	}
	// restarted records the rows the writer put back inside their deadline
	// before any watchdog write reached them. Orphan cleanup ends a child in
	// any non-terminal state, so only rows that are not children are victims.
	restarted := sync.Map{}
	var victims []int
	for _, i := range rng.Perm(n)[:n/3] {
		if !child[i] {
			victims = append(victims, i)
		}
	}
	hooks := &terminalCounter{hooks: map[string]int{}}
	clock := func() time.Time { return now }
	passes := []func(){
		func() {
			sweepRacePass(t, watchdog.New(store, watchdog.StaticTenants{"acme"}, sweepRaceConfig(), clock).WithTerminalHook(hooks), now)
		},
		func() {
			sweepRacePass(t, watchdog.New(store, watchdog.StaticTenants{"acme"}, sweepRaceConfig(), clock).WithTerminalHook(hooks), now)
		},
		func() {
			for _, i := range victims {
				restartDeadline(ctx, t, store, ids[i], now, &restarted)
			}
		},
	}
	if orphans {
		sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{Terminal: hooks, Clock: clock})
		passes = append(passes, func() {
			if _, err := sw.Tick(ctx, now); err != nil {
				t.Errorf("orphan cleanup Tick: %v", err)
			}
		})
	}
	rng.Shuffle(len(passes), func(i, j int) { passes[i], passes[j] = passes[j], passes[i] })

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, pass := range passes {
		wg.Add(1)
		go func(pass func()) {
			defer wg.Done()
			<-start
			pass()
		}(pass)
	}
	close(start)
	wg.Wait()
	// The sweepers hand each terminal claim to the goroutine that runs the
	// row's hooks; the claims close once every hook returned.
	hooks.claims.Wait()

	for i, id := range ids {
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
		if w := store.count(id); terminal && w != 1 || !terminal && w != 0 {
			t.Errorf("%s is %s after %d terminal writes, want %d", id, row.State, w, map[bool]int{true: 1, false: 0}[terminal])
		}
		if child[i] && !terminal {
			t.Errorf("orphaned child %s is still %s after orphan cleanup's pass", id, row.State)
		}
		if _, ok := restarted.Load(id); ok && terminal {
			t.Errorf("%s was put back inside its deadline before any sweep write and still ended %s (%s)", id, row.State, row.FailureReason)
		}
	}
}

// sweepRacePass runs one watchdog pass and reports its error.
func sweepRacePass(t *testing.T, w *watchdog.Watchdog, now time.Time) {
	if _, err := w.Tick(context.Background(), now); err != nil {
		t.Errorf("watchdog Tick: %v", err)
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
