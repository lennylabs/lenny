// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for F-12.4.38: the slot reservation record that the
// §12.4 Redis-outage capacity gate writes under its per-pod advisory lock,
// and that the Redis fast path writes before its script. Each round runs
// three gateway replicas (three slotcounter.Counters over one miniredis and
// one Postgres store with the production migrations) through randomized
// session lifecycles on a few concurrent-workspace pods: admissions that
// bind and later release, admissions cancelled before their row is
// persisted, crashes between each pair of effects, reservations whose
// context is cancelled mid-call, and a leader reclaiming abandoned records
// while the run proceeds. An invariant checker holds every pod's occupancy
// at or under maxConcurrentSessions at every admission, and the end-state
// checks hold the record accounting exact.
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	slotResPods     = 3
	slotResCap      = 3
	slotResReplicas = 3
	slotResWorkers  = 12
	slotResOps      = 30
	slotResRounds   = 3
)

// slotResMode is when the round's Redis is reachable.
type slotResMode int

const (
	redisDown     slotResMode = iota // an outage for the whole round: the §12.4 gate alone
	redisFailsMid                    // Redis serves the first part, then an outage starts
	redisUp                          // Redis for the whole round: the fast path alone
)

func (m slotResMode) String() string {
	return [...]string{"redis down", "redis fails mid-round", "redis up"}[m]
}

// slotResOutcome is how one session's lifecycle ends.
type slotResOutcome int

const (
	outcomeBindRelease   slotResOutcome = iota // bind, run, release, terminal row
	outcomeCancel                              // admitted, the create rolls back
	outcomeCrashRecorded                       // a record written, the replica dies before the script
	outcomeCrashAdmitted                       // admitted, the replica dies before the row insert
	outcomeCrashBound                          // bound, the replica dies before the release
	outcomeCancelledCtx                        // the reservation's context ends mid-call
	slotResOutcomes
)

// slotResPod is the invariant state of one pod: held is the number of
// sessions the run treats as occupying a slot. It rises after an admission
// returns and falls before a release starts, so it never exceeds the
// occupancy either gate counts, and a value above the cap is an overrun.
type slotResPod struct {
	name string
	held atomic.Int32
	peak atomic.Int32
}

func (p *slotResPod) admit() int32 {
	n := p.held.Add(1)
	for {
		cur := p.peak.Load()
		if n <= cur || p.peak.CompareAndSwap(cur, n) {
			return n
		}
	}
}

// slotResRound is one round's fixture and its accounting.
type slotResRound struct {
	t        *testing.T
	store    *sessionpg.Store
	pg       *containers.Postgres
	mr       *miniredis.Miniredis
	counters [slotResReplicas]*slotcounter.Counter
	pods     [slotResPods]*slotResPod
	tenant   string

	mu sync.Mutex
	// inflight maps a session whose reservation started and whose row is
	// not yet persisted or abandoned to its start, so the reclaimer's
	// cutoff precedes every create in progress, as claimOrphanTimeout does.
	inflight map[string]time.Time
	// live is the set of sessions bound and not released, per pod; crashed
	// holds the sessions abandoned after their row named the pod.
	live map[string]map[string]bool
	// orphans are the records abandoned with no row: they over-count until
	// the reclaimer deletes them.
	orphans map[string]map[string]bool
}

// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity),
// §4.6.1 (Orphaned SandboxClaim detection).
// diagnosis: a pod's occupancy exceeded maxConcurrentSessions while the
// replicas admitted through the §12.4 gate, through the Redis fast path, or
// across the switch from one to the other; or the reservation records did
// not match the sessions holding the pod at the end of a round (a record a
// release, a refusal, or the reclaimer should have removed, or a missing
// record that under-counts).
func TestSlotReservationRandomizedInterleavings_spec_12_4(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	store := sessionpg.New(pg.Pool)
	for _, mode := range []slotResMode{redisDown, redisFailsMid, redisUp} {
		for round := 0; round < slotResRounds; round++ {
			t.Run(fmt.Sprintf("%s round %d", mode, round), func(t *testing.T) {
				r := newSlotResRound(t, pg, store)
				r.run(mode, int64(round)*7919+int64(mode))
				r.check(mode)
			})
		}
	}
}

func newSlotResRound(t *testing.T, pg *containers.Postgres, store *sessionpg.Store) *slotResRound {
	t.Helper()
	tenant := "t-" + uuid.NewString()[:8]
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	r := &slotResRound{
		t: t, store: store, pg: pg, mr: miniredis.RunT(t), tenant: tenant,
		inflight: map[string]time.Time{},
		live:     map[string]map[string]bool{},
		orphans:  map[string]map[string]bool{},
	}
	for i := range r.counters {
		cl := redis.NewClient(&redis.Options{Addr: r.mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
		t.Cleanup(func() { _ = cl.Close() })
		r.counters[i] = slotcounter.New(cl,
			slotcounter.WithSlotSource(store), slotcounter.WithFallbackSource(store),
			slotcounter.WithFallbackMaxWindow(time.Hour))
	}
	for i := range r.pods {
		name := fmt.Sprintf("pod-%s-%d", uuid.NewString()[:8], i)
		r.pods[i] = &slotResPod{name: name}
		r.live[name] = map[string]bool{}
		r.orphans[name] = map[string]bool{}
	}
	return r
}

// run drives the workers and the reclaimer, and for redisFailsMid stops
// Redis part-way through.
func (r *slotResRound) run(mode slotResMode, seed int64) {
	if mode == redisDown {
		r.mr.Close()
	}
	ctx, stop := context.WithCancel(context.Background())
	var aux sync.WaitGroup
	aux.Add(1)
	go func() { defer aux.Done(); r.reclaimLoop(ctx) }()
	var done atomic.Int32
	if mode == redisFailsMid {
		aux.Add(1)
		go func() {
			defer aux.Done()
			for done.Load() < slotResWorkers*slotResOps/3 && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			r.mr.Close()
		}()
	}
	var wg sync.WaitGroup
	for w := 0; w < slotResWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed*1000 + int64(w)))
			for i := 0; i < slotResOps; i++ {
				r.lifecycle(rng, r.counters[rng.Intn(slotResReplicas)], r.pods[rng.Intn(slotResPods)])
				done.Add(1)
			}
		}(w)
	}
	wg.Wait()
	stop()
	aux.Wait()
}

// reclaimLoop is the leader's reclaimer: it reclaims with a cutoff that
// precedes every reservation still in progress.
func (r *slotResRound) reclaimLoop(ctx context.Context) {
	for ctx.Err() == nil {
		cutoff := time.Now().Add(-50 * time.Millisecond)
		r.mu.Lock()
		for _, at := range r.inflight {
			if at.Before(cutoff) {
				cutoff = at.Add(-time.Millisecond)
			}
		}
		r.mu.Unlock()
		if _, err := r.store.ReclaimSlotReservations(context.Background(), cutoff); err != nil {
			r.t.Errorf("ReclaimSlotReservations: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *slotResRound) startInflight(id string) {
	r.mu.Lock()
	r.inflight[id] = time.Now()
	r.mu.Unlock()
}

func (r *slotResRound) endInflight(id string) {
	r.mu.Lock()
	delete(r.inflight, id)
	r.mu.Unlock()
}

func (r *slotResRound) mark(set map[string]map[string]bool, pod, id string, on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if on {
		set[pod][id] = true
	} else {
		delete(set[pod], id)
	}
}

// lifecycle runs one session through a random outcome.
func (r *slotResRound) lifecycle(rng *rand.Rand, c *slotcounter.Counter, pod *slotResPod) {
	ctx := context.Background()
	id := uuid.NewString()
	outcome := slotResOutcome(rng.Intn(int(slotResOutcomes)))
	r.startInflight(id)
	defer r.endInflight(id)
	if outcome == outcomeCrashRecorded {
		// The replica died after writing the record and before the script.
		if _, err := r.store.RecordSlotReservation(ctx, pod.name, id); err != nil {
			r.t.Errorf("RecordSlotReservation: %v", err)
			return
		}
		r.mark(r.orphans, pod.name, id, true)
		return
	}
	rctx := ctx
	if outcome == outcomeCancelledCtx {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, time.Duration(rng.Intn(3000))*time.Microsecond)
		defer cancel()
	}
	if _, _, err := c.Reserve(rctx, pod.name, id, slotResCap); err != nil {
		if !errors.Is(err, slotcounter.ErrSlotsExhausted) && outcome != outcomeCancelledCtx {
			r.t.Errorf("Reserve %s on %s: %v", id, pod.name, err)
		}
		// A reservation whose context ended may have committed its admission
		// before the cancellation was observed; that record over-counts and
		// the reclaimer removes it, so it is an orphan here.
		r.mark(r.orphans, pod.name, id, true)
		return
	}
	if n := pod.admit(); n > slotResCap {
		r.t.Errorf("pod %s: occupancy %d exceeds maxConcurrentSessions %d after admitting %s", pod.name, n, slotResCap, id)
	}
	time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
	switch outcome {
	case outcomeCancel, outcomeCancelledCtx:
		pod.held.Add(-1)
		_, _, _, _ = c.Release(ctx, pod.name, id, false)
		return
	case outcomeCrashAdmitted:
		// The session never runs, so it leaves the model; its record keeps
		// counting, which only over-counts, until the reclaimer finds no row.
		pod.held.Add(-1)
		r.mark(r.orphans, pod.name, id, true)
		return
	}
	if err := r.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: r.tenant, RuntimeRef: "echo", State: session.StateRunning,
		PodAssignment: pod.name, ScrubPolicy: "best-effort-per-slot",
	}); err != nil {
		r.t.Errorf("persist the session row: %v", err)
		return
	}
	r.endInflight(id)
	r.mark(r.live, pod.name, id, true)
	if outcome == outcomeCrashBound {
		return // bound and never released: it holds its slot for the round.
	}
	time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
	pod.held.Add(-1)
	r.mark(r.live, pod.name, id, false)
	if _, err := r.store.Update(ctx, r.tenant, id, func(s *sessionstore.Session) error {
		s.State = session.StateCompleted
		s.TerminatedAt = time.Now()
		return nil
	}); err != nil {
		r.t.Errorf("terminal write: %v", err)
	}
	_, _, _, _ = c.Release(ctx, pod.name, id, false)
}

// check runs the end-of-round invariants: no pod overran its cap, and
// after a final reclaim the records are exactly the bound sessions'
// records plus nothing else, while the gate admits exactly the free slots.
func (r *slotResRound) check(mode slotResMode) {
	t := r.t
	ctx := context.Background()
	for _, p := range r.pods {
		if got := p.peak.Load(); got > slotResCap {
			t.Errorf("pod %s: peak occupancy %d exceeds %d", p.name, got, slotResCap)
		}
	}
	// No create is in progress, so a cutoff of now is past every orphan.
	if _, err := r.store.ReclaimSlotReservations(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("final reclaim: %v", err)
	}
	for _, p := range r.pods {
		recs := slotResRecords(t, r.pg, p.name)
		for id := range recs {
			if r.orphans[p.name][id] {
				t.Errorf("pod %s: orphan record %s survived the final reclaim", p.name, id)
			} else if !r.live[p.name][id] {
				t.Errorf("pod %s: record %s belongs to no bound session (a release or refusal left it)", p.name, id)
			}
		}
		// A bound session whose admission ran on the gate or the fast path
		// with a FallbackSource wired always has a record until release.
		for id := range r.live[p.name] {
			if !recs[id] {
				t.Errorf("pod %s: bound session %s has no record", p.name, id)
			}
		}
		// The gate admits exactly the free slots now.
		free := slotResCap - len(r.live[p.name])
		admitted := 0
		for i := 0; i < slotResCap+1; i++ {
			if _, ok, err := r.store.ReserveSlotUnderLock(ctx, p.name, uuid.NewString(), slotResCap); err != nil {
				t.Fatalf("probe: %v", err)
			} else if ok {
				admitted++
			}
		}
		if admitted != free {
			t.Errorf("pod %s (%s): the gate admitted %d probes, want the %d free slots", p.name, mode, admitted, free)
		}
	}
}

func slotResRecords(t *testing.T, pg *containers.Postgres, pod string) map[string]bool {
	t.Helper()
	rows, err := pg.Pool.Query(context.Background(),
		`SELECT session_id::text FROM slot_reservations WHERE pod_id = $1`, pod)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan record: %v", err)
		}
		out[id] = true
	}
	return out
}
