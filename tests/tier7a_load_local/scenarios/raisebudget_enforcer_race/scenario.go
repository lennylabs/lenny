// SPDX-License-Identifier: MIT

//go:build load_local

// Package raisebudget_enforcer_race exercises the §11.2/§8.6
// sessionbudget.Enforcer under concurrent Record/Allow/RaiseBudget/
// TerminateSession/Forget, the reconciliation proposal 0023 S3/S4 lands.
//
// The enforcer gains a tri-state §8.6 extension seam, a deny-next-request
// flag decoupled from termination, and RaiseBudget/TerminateSession
// methods that structurally satisfy leasecontrol.SessionReclaimer. The
// per-tree episode fan-out calls RaiseBudget/TerminateSession per joined
// session out-of-band (through the SessionReclaimer), while in-path proxy
// goroutines call Record (which consults the seam and may itself call
// back into the enforcer's reclaimer methods) and the pre-flight gate
// calls Allow. Both the seam and the terminator run OUTSIDE the enforcer
// lock, mirroring the terminate-outside-the-lock pattern, so a concurrent
// Forget driven by the terminal pipeline is never blocked and no deadlock
// forms.
//
// The invariants this scenario asserts:
//   - No deadlock: every iteration returns and the whole run completes
//     within its profile duration. A seam or terminator that ran under the
//     enforcer lock, or a lock held across the out-of-band reclaimer
//     callback, would deadlock a concurrent Forget and stall the run.
//   - No lost deny-flag clear: a deterministic drain phase raises every
//     denied session's budget through RaiseBudget and asserts the
//     pre-flight gate then admits it. A RaiseBudget that failed to clear
//     the deny flag under contention would leave the session denied.
//   - Budget monotonicity: a set of raise-only sessions is raised
//     concurrently and never recorded down; their final budget equals the
//     sum of the deltas applied, so RaiseBudget never loses or reorders a
//     raise.
//   - The whole path is -race clean (run under -race with the load_local
//     tag).
//
// Runnable under `lenny-test stress --test raisebudget_enforcer_race
// --runs N`.
//
// TESTING.md §12.7.a regression scenarios; proposal 0023 S3/S4.
package raisebudget_enforcer_race

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
	"github.com/lennylabs/lenny/tests/testinfra/scenkit"
)

const name = "raisebudget_enforcer_race"

// sessionCount is the number of in-path sessions the load spreads over.
// A moderate count keeps many goroutines contending on the same counters
// (the enforcer map is shared) while leaving room for the out-of-band
// fan-out goroutine to reclaim them concurrently.
const sessionCount = 64

// raiseOnlySessions is the count of sessions used only for the budget
// monotonicity invariant: they are raised concurrently and never recorded
// down, so their final budget must equal the summed deltas exactly.
const raiseOnlySessions = 16

// raiseDelta is the token amount each RaiseBudget applies. It is the
// granted extension delta the episode fan-out would carry per session.
const raiseDelta = int64(1000)

func init() {
	loadgen.Register(name, func() loadgen.Scenario { return &Scenario{counters: scenkit.NewCounters()} })
}

// Scenario drives concurrent enforcer traffic and asserts the no-deadlock,
// deny-clear, and budget-monotonicity invariants.
type Scenario struct {
	counters *scenkit.Counters

	enforcer *sessionbudget.Enforcer
	term     *countingTerminator

	// raiseOnlyApplied counts, per raise-only session, how many RaiseBudget
	// calls the load applied, so Assert can compute the expected final
	// budget. Keyed by session id.
	raiseOnlyApplied sync.Map // string -> *atomic.Int64
}

func (s *Scenario) Name() string { return name }

func (s *Scenario) DefaultProfile() loadgen.Profile {
	return loadgen.Profile{Kind: loadgen.ConstantVU, VUs: 64, Duration: 2 * time.Second}
}

// inPathID is the id of the j-th in-path session.
func inPathID(j int) string { return fmt.Sprintf("s-%d", j%sessionCount) }

// raiseOnlyID is the id of the j-th raise-only session.
func raiseOnlyID(j int) string { return fmt.Sprintf("raise-only-%d", j%raiseOnlySessions) }

func (s *Scenario) Setup(ctx context.Context) error {
	s.term = &countingTerminator{}
	// A Pending-returning seam models the elicitation-mode extension whose
	// in-path wait elapses: Record sets the deny flag but does not
	// terminate. The seam runs outside the enforcer lock; it does not call
	// back into the enforcer here, so the out-of-band reclaimer traffic in
	// Run is the sole source of RaiseBudget/TerminateSession, matching the
	// episode fan-out's independent goroutine.
	seam := func(reqCtx, waitCtx context.Context, _ /*tenantID*/, _ /*sessionID*/ string, _, _ int64) sessionbudget.Outcome {
		return sessionbudget.Pending
	}
	s.enforcer = sessionbudget.New(s.term, seam, nil)

	// Seed the raise-only sessions so RaiseBudget has a counter to raise
	// (RaiseBudget on an unknown session is a no-op). Record them with a
	// large budget and zero usage so they never exhaust.
	for j := 0; j < raiseOnlySessions; j++ {
		id := raiseOnlyID(j)
		s.enforcer.Record(context.Background(), context.Background(), "acme", id, 1_000_000, 0)
		s.raiseOnlyApplied.Store(id, &atomic.Int64{})
	}
	return nil
}

func (s *Scenario) Teardown(ctx context.Context) error { return nil }

// Run drives one mixed iteration. Odd/even virtual users split into
// in-path traffic (Record + Allow, with the seam consulted at exhaustion)
// and out-of-band fan-out traffic (RaiseBudget/TerminateSession, as the
// episode fan-out calls through the SessionReclaimer), plus occasional
// Forget from the terminal pipeline and the raise-only monotonicity load.
func (s *Scenario) Run(ctx context.Context, vu, iter int) error {
	j := vu*7 + iter
	switch vu % 4 {
	case 0:
		// In-path proxy traffic: record usage that exhausts the session so
		// the Pending seam fires and sets the deny flag, then read the gate.
		id := inPathID(j)
		s.enforcer.Record(ctx, ctx, "acme", id, 100, 200) // exhausts
		if s.enforcer.Allow(id) {
			s.counters.Inc("allowed_after_record")
		} else {
			s.counters.Inc("denied_after_record")
		}
	case 1:
		// Out-of-band fan-out: raise a joined session's budget (a grant) and
		// clear its deny flag, as the episode fan-out does through the
		// SessionReclaimer, concurrently with the in-path Record/Allow above.
		id := inPathID(j)
		s.enforcer.RaiseBudget(id, raiseDelta)
		s.counters.Inc("raised")
	case 2:
		// Out-of-band fan-out: terminate a joined session on a terminal
		// outcome (fail closed), and the terminal pipeline forgets it. The
		// terminator runs outside the enforcer lock, so a concurrent Forget
		// is never blocked.
		id := inPathID(j)
		s.enforcer.TerminateSession(id)
		s.enforcer.Forget(id)
		s.counters.Inc("terminated")
	default:
		// Budget monotonicity load: raise a raise-only session, which is
		// never recorded down. Track the applied delta so Assert can verify
		// the final budget equals the summed raises.
		id := raiseOnlyID(j)
		s.enforcer.RaiseBudget(id, raiseDelta)
		if v, ok := s.raiseOnlyApplied.Load(id); ok {
			v.(*atomic.Int64).Add(raiseDelta)
		}
		s.counters.Inc("raise_only")
	}
	return nil
}

func (s *Scenario) Assert(r *loadgen.Result) error {
	s.counters.EmitTo(r)
	r.AddCustom("terminate_calls", float64(s.term.count()))

	if e := s.counters.Get("errors"); e > 0 {
		return fmt.Errorf("enforcer observed %d errors under load", e)
	}
	if r.Iterations < 100 {
		return fmt.Errorf("scenario did not get enough load: %d iterations (want >= 100)", r.Iterations)
	}

	// No lost deny-flag clear. After the concurrent phase, deterministically
	// exhaust every in-path session (Pending seam -> deny), then RaiseBudget
	// it and assert the pre-flight gate admits it. A RaiseBudget that failed
	// to clear the deny flag under contention would leave the session denied
	// here. Forget first so each session starts from a clean counter.
	for j := 0; j < sessionCount; j++ {
		id := inPathID(j)
		s.enforcer.Forget(id)
		s.enforcer.Record(context.Background(), context.Background(), "acme", id, 100, 200) // exhaust -> Pending -> deny
		if s.enforcer.Allow(id) {
			return fmt.Errorf("deny-clear invariant: session %s admitted while denied before RaiseBudget", id)
		}
		s.enforcer.RaiseBudget(id, raiseDelta)
		if !s.enforcer.Allow(id) {
			return fmt.Errorf("lost deny-flag clear: RaiseBudget did not admit session %s after a Pending deny", id)
		}
	}

	// Budget monotonicity. Each raise-only session was seeded at 1_000_000
	// and only ever raised. Its final budget must equal the seed plus the
	// summed applied deltas, and a subsequent record just under that budget
	// must be admitted (proving no raise was lost or reordered). Apply one
	// final raise-and-check per session deterministically to read the budget
	// through the gate: consume exactly the seed plus applied deltas minus a
	// margin and assert Allow stays true, then consume one token past it and
	// assert the gate closes, pinning the exact raised ceiling.
	for j := 0; j < raiseOnlySessions; j++ {
		id := raiseOnlyID(j)
		v, ok := s.raiseOnlyApplied.Load(id)
		if !ok {
			continue
		}
		applied := v.(*atomic.Int64).Load()
		budget := int64(1_000_000) + applied
		// Just under the raised budget: admitted.
		s.enforcer.Record(context.Background(), context.Background(), "acme", id, budget, budget-1)
		if !s.enforcer.Allow(id) {
			return fmt.Errorf("budget monotonicity: session %s denied just under its raised budget %d (applied deltas %d lost)",
				id, budget, applied)
		}
		// One token over the raised budget: the gate must close, confirming
		// the raised ceiling is exactly seed+applied and no raise vanished.
		s.enforcer.Record(context.Background(), context.Background(), "acme", id, budget, 1)
		if s.enforcer.Allow(id) {
			return fmt.Errorf("budget monotonicity: session %s admitted past its raised budget %d", id, budget)
		}
	}
	return nil
}

// countingTerminator records TerminateSession calls so Assert can report
// the terminator fired without blocking the enforcer's fast path.
type countingTerminator struct {
	calls atomic.Int64
}

func (t *countingTerminator) TerminateSession(_ /*sessionID*/, _ /*reason*/ string) {
	t.calls.Add(1)
}

func (t *countingTerminator) count() int64 { return t.calls.Load() }
