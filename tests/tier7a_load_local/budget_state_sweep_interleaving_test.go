// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the per-replica budget state sweep. Each
// round races proxied-request settlements (Record), terminal writes, and
// sweep passes over a shared session store on two replicas, and checks that
// no pass ever forgets a session whose row is still non-terminal.
//
// spec: §11.2, §10.1.1
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const (
	sweepInterleavingRounds   = 40
	sweepInterleavingSessions = 64
	sweepInterleavingOps      = 400
	// sweepInterleavingNonUUID is how many of the sessions carry an
	// identifier that is not a UUID, which the in-memory store accepts.
	// These rows stay live for the whole round, so no pass may forget them.
	sweepInterleavingNonUUID = 8
)

type sweepNoopTerminator struct{}

func (sweepNoopTerminator) TerminateSession(string, string) {}

// sweepClock is a monotonic fake clock every goroutine of a round shares.
type sweepClock struct{ ns atomic.Int64 }

func (c *sweepClock) Now() time.Time { return time.Unix(0, c.ns.Add(int64(time.Millisecond))).UTC() }

// spec: §11.2 (Budgets and Quotas), §10.1.1
// diagnosis: the budget state sweep forgot the budget state of a session
// whose row was still non-terminal, or left a live session untracked after
// a Record. A live session that loses its enforcer counter mid-flight
// restarts its §11.2 budget from zero on that replica; the sweep must only
// forget a session the store reports terminal or absent.
func TestBudgetStateSweepNeverForgetsLiveSession_spec_11_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	for round := 0; round < sweepInterleavingRounds; round++ {
		rng := rand.New(rand.NewSource(seed + int64(round)))
		t.Run(fmt.Sprintf("round-%d", round), func(t *testing.T) {
			runSweepInterleavingRound(t, rng)
		})
	}
}

func runSweepInterleavingRound(t *testing.T, rng *rand.Rand) {
	ctx := context.Background()
	store := memstore.New()
	clock := &sweepClock{}
	clock.ns.Store(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC).UnixNano())

	ids := make([]string, sweepInterleavingSessions)
	for i := range ids {
		ids[i] = uuid.NewString()
		if i < sweepInterleavingNonUUID {
			ids[i] = fmt.Sprintf("live-%d", i)
		}
		if err := store.Create(ctx, sessionstore.Session{ID: ids[i], TenantID: "acme", State: session.StateRunning}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	var violations atomic.Int64
	liveForgotten := func(id string) bool {
		row, err := store.GetByID(ctx, id)
		return err == nil && !session.IsTerminal(row.State)
	}
	type replica struct {
		enf   *sessionbudget.Enforcer
		sweep *budgetstate.Sweep
	}
	replicas := make([]replica, 2)
	for i := range replicas {
		enf := sessionbudget.New(sweepNoopTerminator{}, nil, nil)
		replicas[i] = replica{enf: enf, sweep: budgetstate.New(budgetstate.Config{
			Sessions: store,
			Tracked:  enf.TrackedSessions,
			Forget: func(id string) {
				if liveForgotten(id) {
					violations.Add(1)
					t.Errorf("sweep forgot non-terminal session %s", id)
				}
				enf.Forget(id)
			},
			Interval:  time.Minute,
			BatchSize: 1 + rng.Intn(16),
			Now:       clock.Now,
		})}
	}

	// Pre-generate each worker's script from the round's rng, so the
	// interleaving varies with the seed while the goroutines themselves
	// share no rng.
	type op struct {
		kind    int // 0 Record, 1 terminal write, 2 sweep pass
		replica int
		session int
	}
	const workers = 4
	scripts := make([][]op, workers)
	for w := range scripts {
		for i := 0; i < sweepInterleavingOps; i++ {
			k := rng.Intn(10)
			kind := 0
			switch {
			case k == 0:
				kind = 1
			case k < 3:
				kind = 2
			}
			target := rng.Intn(len(ids))
			if kind == 1 {
				// Terminal writes reach only the UUID-identified sessions.
				target = sweepInterleavingNonUUID + rng.Intn(len(ids)-sweepInterleavingNonUUID)
			}
			scripts[w] = append(scripts[w], op{kind, rng.Intn(len(replicas)), target})
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(script []op) {
			defer wg.Done()
			for _, o := range script {
				r := replicas[o.replica]
				id := ids[o.session]
				switch o.kind {
				case 0:
					r.enf.Record(ctx, ctx, "acme", id, 1<<40, 1)
				case 1:
					endSessionAt(t, store, id, clock.Now())
					// The terminal pipeline forgets on the replica that runs it.
					r.enf.Forget(id)
				case 2:
					r.sweep.Pass(ctx)
				}
			}
		}(scripts[w])
	}
	wg.Wait()
	if violations.Load() > 0 {
		return
	}

	// Every live session a replica recorded after the races is still
	// tracked there, and a final pass leaves only live sessions tracked.
	for _, r := range replicas {
		for _, id := range ids {
			if !liveForgotten(id) {
				continue
			}
			r.enf.Record(ctx, ctx, "acme", id, 1<<40, 1)
		}
		r.sweep.Pass(ctx)
		for _, id := range r.enf.TrackedSessions() {
			if !liveForgotten(id) {
				t.Fatalf("final pass left ended session %s tracked", id)
			}
		}
		live := 0
		for _, id := range ids {
			if liveForgotten(id) {
				live++
			}
		}
		if got := len(r.enf.TrackedSessions()); got != live {
			t.Fatalf("replica tracks %d sessions after the final pass, want the %d live ones", got, live)
		}
	}
}

// endSessionAt writes the terminal transition the way every terminal writer
// does, stamping terminated_at. A session already terminal is left as is.
func endSessionAt(t *testing.T, store *memstore.Store, id string, at time.Time) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		if session.IsTerminal(r.State) {
			return nil
		}
		from := r.State
		r.State = session.StateCancelled
		sessionstore.MarkTerminated(r, from, at)
		return nil
	}); err != nil {
		t.Errorf("terminal write of %s: %v", id, err)
	}
}
