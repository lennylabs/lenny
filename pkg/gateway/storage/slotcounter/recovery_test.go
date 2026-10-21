// SPDX-License-Identifier: MIT

package slotcounter_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// The tests in this file pin the add-only recovery rebuild that runs when a
// Redis outage ends. Each outage is a miniredis Close followed by a Restart,
// which keeps the data, so the cases state an outage through which Redis
// keeps its sets.

// outageEnv is one miniredis shared by the replicas of a recovery case, with
// a Postgres fallback and a row source.
type outageEnv struct {
	t   *testing.T
	mr  *miniredis.Miniredis
	cl  *redis.Client
	src *fakeSource
	fb  *fakeFallback
}

func newOutageEnv(t *testing.T) *outageEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	return &outageEnv{t: t, mr: mr, cl: fastClient(t, mr), src: newFakeSource(), fb: newFakeFallback()}
}

// fastClient returns a client with retries disabled so a refused dial
// surfaces at once.
func fastClient(t *testing.T, mr *miniredis.Miniredis) *redis.Client {
	t.Helper()
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// replica returns a Counter on the shared miniredis with the env's source
// and fallback.
func (e *outageEnv) replica(opts ...slotcounter.Option) *slotcounter.Counter {
	all := append([]slotcounter.Option{
		slotcounter.WithSlotSource(e.src),
		slotcounter.WithFallbackSource(e.fb),
	}, opts...)
	return slotcounter.New(fastClient(e.t, e.mr), all...)
}

func (e *outageEnv) down() { e.mr.Close() }

func (e *outageEnv) up() {
	e.t.Helper()
	if err := e.mr.Restart(); err != nil {
		e.t.Fatalf("restart miniredis: %v", err)
	}
}

// seedPod writes members and sets the pod's rehydrated flag directly.
func (e *outageEnv) seedPod(pod string, ids ...string) {
	e.t.Helper()
	ctx := context.Background()
	if len(ids) > 0 {
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		if err := e.cl.SAdd(ctx, "lenny:pod:{"+pod+"}:active_slots", args...).Err(); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.cl.Set(ctx, "lenny:pod:{"+pod+"}:rehydrated", "1", 0).Err(); err != nil {
		e.t.Fatal(err)
	}
}

func mustReserve(t *testing.T, c *slotcounter.Counter, pod, id string, cap int32) int32 {
	t.Helper()
	n, _, err := c.Reserve(context.Background(), pod, id, cap)
	if err != nil {
		t.Fatalf("Reserve %s on %s: %v", id, pod, err)
	}
	return n
}

// TestRecoveryFallbackAdmissionCountedByRebuild_spec_12_4 pins that a
// fallback admission during the outage stamps the
// clock and holds no member; the admitting replica's rebuild after Redis
// returns adds the member, clears the clock, and the next reservation at the
// cap is refused.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration atomicity)
func TestRecoveryFallbackAdmissionCountedByRebuild_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "sib")
	e.src.set("sib", "P", false)

	e.down()
	mustReserve(t, a, "P", "S", 2)
	if a.OutageSinceForTest().IsZero() {
		t.Fatal("a fallback admission must stamp the outage clock")
	}
	e.up()
	wantMembers(t, e.cl, "P", "sib")

	// S's row names P only after the rebuild.
	if _, _, err := a.Reserve(context.Background(), "P", "T", 2); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Fatalf("reservation at the cap after the rebuild = %v, want ErrSlotsExhausted", err)
	}
	wantMembers(t, e.cl, "P", "S", "sib")
	if !a.OutageSinceForTest().IsZero() || a.LedgerLenForTest() != 0 {
		t.Fatalf("after a successful rebuild the clock and ledger must clear (clock %v, ledger %d)",
			a.OutageSinceForTest(), a.LedgerLenForTest())
	}
}

// TestRecoveryUncountedAdmissionReleaseIsNoop_spec_12_4 pins
// : the release of a fallback-admitted session before any
// rebuild, with the pod's flag set and the admitting Counter discarded,
// removes nothing and reports remaining above zero only for real members.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity)
func TestRecoveryUncountedAdmissionReleaseIsNoop_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "sib")
	e.down()
	mustReserve(t, a, "P", "S", 4)
	e.up()
	// A is discarded; B never observed the outage.
	b := slotcounter.New(fastClient(t, e.mr), slotcounter.WithSlotSource(e.src))
	removed, remain, _, err := b.Release(context.Background(), "P", "S", false)
	if err != nil || removed || remain != 1 {
		t.Fatalf("Release = (removed %v, remaining %d, %v), want (false, 1, nil): no disposition", removed, remain, err)
	}
	wantMembers(t, e.cl, "P", "sib")
}

// TestRecoveryProbeTickRebuildsIdleReplica_spec_12_4 pins: a replica
// with no slot traffic stamps its clock on an unreachable probe and rebuilds
// on the first reachable probe after Redis returns, deleting the flag of a
// pod a live row names.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryProbeTickRebuildsIdleReplica_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "sib")
	e.src.set("sib", "P", false)
	ctx := context.Background()

	e.down()
	a.ProbeTickForTest(ctx)
	if a.OutageSinceForTest().IsZero() {
		t.Fatal("an unreachable probe tick must stamp the outage clock")
	}
	e.up()
	a.ProbeTickForTest(ctx)
	if flagSet(t, e.cl, "P") {
		t.Fatal("the probe's rebuild must delete the flag of a listed pod")
	}
	if !a.OutageSinceForTest().IsZero() {
		t.Fatal("the probe's successful rebuild must clear the clock")
	}
	wantMembers(t, e.cl, "P", "sib")
}

// TestRecoveryCleanReleaseRefusedStampsClock_spec_12_4 pins
// clean-release ledger rule: a clean Release refused while Redis is
// unreachable records no entry and stamps the clock, and the next call runs
// one full rebuild with one listing.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryCleanReleaseRefusedStampsClock_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S", "sib")
	e.src.set("sib", "P", false)
	e.src.set("S", "P", true)
	ctx := context.Background()

	e.down()
	if _, _, _, err := a.Release(ctx, "P", "S", false); err == nil {
		t.Fatal("a release while Redis is unreachable must fail")
	}
	if a.LedgerLenForTest() != 0 || a.OutageSinceForTest().IsZero() {
		t.Fatalf("clean refused release: ledger %d (want 0), clock zero %v (want stamped)",
			a.LedgerLenForTest(), a.OutageSinceForTest().IsZero())
	}
	e.up()
	removed, remain, reh, err := a.Release(ctx, "P", "S", false)
	if err != nil || !removed || remain != 1 || !reh {
		t.Fatalf("repeat Release = (removed %v, remaining %d, rehydrated %v, %v), want (true, 1, true, nil)", removed, remain, reh, err)
	}
	if n := e.src.listCount(); n != 1 {
		t.Fatalf("listings = %d, want 1", n)
	}
	wantMembers(t, e.cl, "P", "sib")
}

// TestRecoveryCleanReleaseOfOnlyMemberAfterReturn_spec_12_4 pins the
// only-member case: a clean release refused during the outage and repeated
// after Redis returns removes the member with remaining 0 from its own
// script, because no live row and no ledger entry names the pod, so the
// rebuild leaves the set and flag unchanged.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Slot assignment atomicity)
func TestRecoveryCleanReleaseOfOnlyMemberAfterReturn_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S")
	e.src.set("S", "P", true)
	ctx := context.Background()
	e.down()
	_, _, _, _ = a.Release(ctx, "P", "S", false)
	e.up()
	removed, remain, reh, err := a.Release(ctx, "P", "S", false)
	if err != nil || !removed || remain != 0 || reh {
		t.Fatalf("Release = (removed %v, remaining %d, rehydrated %v, %v), want (true, 0, false, nil)", removed, remain, reh, err)
	}
	if !flagSet(t, e.cl, "P") {
		t.Fatal("an unlisted pod's flag must stay")
	}
}

// TestRecoveryOutageDuringRehydration_spec_12_4 pins: Redis fails
// before a rehydration's seed, the reservation takes the fallback, and the
// pod's next reservation after the outage rehydrates and adds the rows'
// identifiers to the members already present, including the ledger's.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration atomicity)
func TestRecoveryOutageDuringRehydration_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	ctx := context.Background()
	if err := e.cl.SAdd(ctx, "lenny:pod:{P}:active_slots", "leaked:old").Err(); err != nil {
		t.Fatal(err)
	}
	e.src.set("row", "P", false)
	e.down()
	mustReserve(t, a, "P", "S", 8)
	e.up()
	mustReserve(t, a, "P", "T", 8)
	wantMembers(t, e.cl, "P", "S", "T", "leaked:old", "row")
}

// TestRecoveryOutageResumesDuringRebuild_spec_12_4 pins: a rebuild
// script that fails part-way keeps the clock set and the entries of the pods
// whose script did not complete, and the next rebuild adds them.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryOutageResumesDuringRebuild_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	hook := &failKeysHook{}
	cl := fastClient(t, e.mr)
	cl.AddHook(hook)
	a := slotcounter.New(cl, slotcounter.WithSlotSource(e.src), slotcounter.WithFallbackSource(e.fb))
	e.seedPod("PA")
	e.seedPod("PB")
	e.down()
	mustReserve(t, a, "PA", "SA", 4)
	mustReserve(t, a, "PB", "SB", 4)
	e.up()
	hook.set("{PB}")
	ctx := context.Background()
	a.ProbeTickForTest(ctx)
	wantMembers(t, e.cl, "PA", "SA")
	wantMembers(t, e.cl, "PB")
	if a.OutageSinceForTest().IsZero() || a.LedgerLenForTest() != 1 {
		t.Fatalf("after a part-way failure: clock zero %v (want set), ledger %d (want 1)",
			a.OutageSinceForTest().IsZero(), a.LedgerLenForTest())
	}
	hook.set("")
	a.ProbeTickForTest(ctx)
	wantMembers(t, e.cl, "PB", "SB")
	if !a.OutageSinceForTest().IsZero() || a.LedgerLenForTest() != 0 {
		t.Fatal("the repeated rebuild must clear the clock and the ledger")
	}
}

// TestRecoveryReplicasConvergeOnUnion_spec_12_4 pins: two replicas
// that observed the outage each add their own ledger's entries and the slot
// holders, so the set converges on the union.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryReplicasConvergeOnUnion_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a, b := e.replica(), e.replica()
	e.seedPod("P", "sib")
	e.src.set("sib", "P", false)
	e.down()
	mustReserve(t, a, "P", "SA", 8)
	mustReserve(t, b, "P", "SB", 8)
	e.up()
	ctx := context.Background()
	a.ProbeTickForTest(ctx)
	b.ProbeTickForTest(ctx)
	mustReserve(t, a, "P", "SC", 8)
	wantMembers(t, e.cl, "P", "SA", "SB", "SC", "sib")
}

// TestRecoveryAdmissionReleasedElsewhereStaysCounted_spec_12_4 pins that
// an admission recorded by A, then the session's
// terminal write and a release on B before A's rebuild, leaves the member
// after A's rebuild.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryAdmissionReleasedElsewhereStaysCounted_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "sib")
	e.src.set("sib", "P", false)
	e.down()
	mustReserve(t, a, "P", "S", 8)
	e.up()
	e.src.set("S", "P", true)
	b := slotcounter.New(fastClient(t, e.mr), slotcounter.WithSlotSource(e.src))
	if removed, _, _, err := b.Release(context.Background(), "P", "S", false); err != nil || removed {
		t.Fatalf("B's release = (removed %v, %v), want (false, nil)", removed, err)
	}
	a.ProbeTickForTest(context.Background())
	wantMembers(t, e.cl, "P", "S", "sib")
}

// TestRecoveryAddOnlyKeepsEveryMember_spec_12_4 pins the add-only rule:
// on a pod a live sibling names, the rebuild deletes the
// flag and keeps every member, including one whose row is terminal, one whose
// row is non-terminal and names no pod, one with no row, and leaked:S.
//
// spec: §12.4 (Redis failure behavior), §6.2 (leaked slot semantics)
func TestRecoveryAddOnlyKeepsEveryMember_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "sib", "ended", "funnel", "norow", "leaked:S")
	e.src.set("sib", "P", false)
	e.src.set("ended", "P", true)
	e.src.set("funnel", "", false)
	e.src.set("S", "P", true)
	e.down()
	a.ProbeTickForTest(context.Background())
	e.up()
	a.ProbeTickForTest(context.Background())
	if flagSet(t, e.cl, "P") {
		t.Fatal("the rebuild must delete the listed pod's flag")
	}
	wantMembers(t, e.cl, "P", "ended", "funnel", "leaked:S", "norow", "sib")
	// The next reservation rehydrates and keeps the members too.
	mustReserve(t, a, "P", "new", 16)
	wantMembers(t, e.cl, "P", "ended", "funnel", "leaked:S", "new", "norow", "sib")
}

// TestRecoveryReReservationKeptAfterFailedFunnelRelease_spec_12_4 pins that
// A's failure-funnel release of S fails while Redis
// is unreachable, B re-reserves S on P while S's row is resume_pending and
// names no pod, once by a fallback admission and once by a fast-path
// reservation, and A's rebuild leaves S a member of P.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryReReservationKeptAfterFailedFunnelRelease_spec_12_4(t *testing.T) {
	for _, fastPath := range []bool{false, true} {
		name := "fallback admission"
		if fastPath {
			name = "fast-path reservation"
		}
		t.Run(name, func(t *testing.T) {
			e := newOutageEnv(t)
			a, b := e.replica(), e.replica()
			e.seedPod("P", "S", "sib")
			e.src.set("sib", "P", false)
			e.src.set("S", "", false)
			ctx := context.Background()
			e.down()
			if _, _, _, err := a.Release(ctx, "P", "S", false); err == nil {
				t.Fatal("A's release must fail during the outage")
			}
			if !fastPath {
				mustReserve(t, b, "P", "S", 8)
				e.up()
			} else {
				b.ProbeTickForTest(ctx)
				e.up()
				mustReserve(t, b, "P", "S", 8)
			}
			a.ProbeTickForTest(ctx)
			mustReserve(t, a, "P", "other", 8)
			wantMembers(t, e.cl, "P", "S", "other", "sib")
		})
	}
}

// TestRecoveryTerminalReleaseFailedOnDiscardedReplica_spec_12_4 pins that
// A's terminal release of S fails during the outage
// and A is discarded; B's rebuild with a live sibling keeps S, the sibling,
// and a leaked: member, and the sibling's release then removes only its own
// member and runs no disposition.
//
// spec: §12.4 (Redis failure behavior), §6.2 (leaked slot semantics)
func TestRecoveryTerminalReleaseFailedOnDiscardedReplica_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a, b := e.replica(), e.replica()
	e.seedPod("P", "S", "sib", "leaked:X")
	e.src.set("sib", "P", false)
	e.src.set("S", "P", true)
	ctx := context.Background()
	e.down()
	_, _, _, _ = a.Release(ctx, "P", "S", true)
	b.ProbeTickForTest(ctx)
	e.up()
	b.ProbeTickForTest(ctx)
	e.src.set("sib", "P", true)
	removed, remain, _, err := b.Release(ctx, "P", "sib", false)
	if err != nil || !removed || remain != 2 {
		t.Fatalf("sibling release = (removed %v, remaining %d, %v), want (true, 2, nil): no disposition", removed, remain, err)
	}
	wantMembers(t, e.cl, "P", "S", "leaked:X")
}

// TestRecoveryLeakedFailedReleaseKeepsBoth_spec_12_4 pins with the
// ledger: a leaked failed release of S in A's ledger, with S's row terminal
// at the rebuild, leaves both S and leaked:S.
//
// spec: §12.4 (Redis failure behavior), §6.2 (leaked slot semantics)
func TestRecoveryLeakedFailedReleaseKeepsBoth_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S", "sib")
	e.src.set("sib", "P", false)
	e.src.set("S", "P", true)
	ctx := context.Background()
	e.down()
	if _, _, _, err := a.Release(ctx, "P", "S", true); err == nil {
		t.Fatal("the leaked release must fail during the outage")
	}
	e.up()
	a.ProbeTickForTest(ctx)
	wantMembers(t, e.cl, "P", "S", "leaked:S", "sib")
}

// TestRecoveryDuplicateReleaseAfterFlagDelete_spec_12_4 pins: a
// session whose release emptied the set before the outage, released again
// after the rebuild deleted the flag, seeds its own member, removes it, and
// reports removed with remaining 0, which runs the disposition again.
//
// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration atomicity)
func TestRecoveryDuplicateReleaseAfterFlagDelete_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S")
	ctx := context.Background()
	if removed, remain, _, err := a.Release(ctx, "P", "S", false); err != nil || !removed || remain != 0 {
		t.Fatalf("first release = (%v, %d, %v)", removed, remain, err)
	}
	// A row that is about to end still names P at the listing.
	e.src.set("S", "P", false)
	e.down()
	a.ProbeTickForTest(ctx)
	e.up()
	a.ProbeTickForTest(ctx)
	if flagSet(t, e.cl, "P") {
		t.Fatal("precondition: the rebuild deletes P's flag")
	}
	e.src.set("S", "P", true)
	removed, remain, reh, err := a.Release(ctx, "P", "S", false)
	if err != nil || !removed || remain != 0 || !reh {
		t.Fatalf("duplicate release = (removed %v, remaining %d, rehydrated %v, %v), want (true, 0, true, nil)", removed, remain, reh, err)
	}
}

// TestRecoveryRebuildSourceFailure_spec_12_4 pins that a rebuild whose
// listing fails keeps the clock set, makes Reserve return ErrFailClosed, and
// makes Release return the source error.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryRebuildSourceFailure_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S")
	ctx := context.Background()
	e.down()
	a.ProbeTickForTest(ctx)
	e.up()
	srcErr := errors.New("postgres down")
	e.src.mu.Lock()
	e.src.listErr = srcErr
	e.src.mu.Unlock()
	if _, _, err := a.Reserve(ctx, "P", "T", 8); !errors.Is(err, slotcounter.ErrFailClosed) {
		t.Fatalf("Reserve = %v, want ErrFailClosed", err)
	}
	if _, _, _, err := a.Release(ctx, "P", "S", false); !errors.Is(err, srcErr) {
		t.Fatalf("Release = %v, want the source error", err)
	}
	if a.OutageSinceForTest().IsZero() {
		t.Fatal("a failed rebuild must keep the clock set")
	}
	wantMembers(t, e.cl, "P", "S")
}

// TestRecoveryRebuildIsSingleFlight_spec_12_4 pins that concurrent callers
// run one rebuild: one listing for many concurrent reservations. Run under
// -race.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryRebuildIsSingleFlight_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P")
	e.down()
	a.ProbeTickForTest(context.Background())
	e.up()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, _ = a.Reserve(context.Background(), "Q", string(rune('a'+i)), 64)
		}(i)
	}
	wg.Wait()
	if n := e.src.listCount(); n != 1 {
		t.Fatalf("listings = %d, want 1 (single-flight rebuild)", n)
	}
}

// TestRecoveryWithoutSourceAddsLedgerOnly_spec_12_4 pins the no-source
// rebuild: it adds the ledger's admissions and leaked marks, removes no
// member, and deletes no flag.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryWithoutSourceAddsLedgerOnly_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := slotcounter.New(fastClient(t, e.mr), slotcounter.WithFallbackSource(e.fb))
	e.seedPod("P", "keep", "L")
	ctx := context.Background()
	e.down()
	mustReserve(t, a, "P", "S", 8)
	_, _, _, _ = a.Release(ctx, "P", "L", true)
	e.up()
	a.ProbeTickForTest(ctx)
	wantMembers(t, e.cl, "P", "L", "S", "keep", "leaked:L")
	if !flagSet(t, e.cl, "P") {
		t.Fatal("a rebuild with no source must delete no flag")
	}
}

// TestRecoveryLedgerOrdering_spec_12_4 pins the ledger's sequence rule: an
// admission recorded from inside the listing during a rebuild, for a fresh
// pair and for a pair that rebuild already read, leaves the clock set and is
// added by the next rebuild.
//
// spec: §12.4 (Redis failure behavior)
func TestRecoveryLedgerOrdering_spec_12_4(t *testing.T) {
	for _, reread := range []bool{false, true} {
		name := "fresh pair"
		if reread {
			name = "pair already read"
		}
		t.Run(name, func(t *testing.T) {
			e := newOutageEnv(t)
			a := e.replica()
			e.seedPod("P")
			e.down()
			mustReserve(t, a, "P", "S", 8)
			e.up()
			late := "L"
			if reread {
				late = "S"
			}
			once := sync.Once{}
			e.src.mu.Lock()
			e.src.onList = func() { once.Do(func() { a.RecordAdmissionForTest("P", late) }) }
			e.src.mu.Unlock()
			ctx := context.Background()
			a.ProbeTickForTest(ctx)
			if a.OutageSinceForTest().IsZero() || a.LedgerLenForTest() != 1 {
				t.Fatalf("after the first rebuild: clock zero %v (want set), ledger %d (want 1)",
					a.OutageSinceForTest().IsZero(), a.LedgerLenForTest())
			}
			a.ProbeTickForTest(ctx)
			if reread {
				wantMembers(t, e.cl, "P", "S")
			} else {
				wantMembers(t, e.cl, "P", "L", "S")
			}
			if !a.OutageSinceForTest().IsZero() {
				t.Fatal("the second rebuild must clear the clock")
			}
		})
	}
}

// TestRecoveryLeakedReleaseFailsWithClockZero_spec_12_4 pins the leaked-mark
// rule: a leaked Release that fails on its context deadline or on a source
// error in its rehydration, with the clock zero, records a leaked mark and
// leaves the clock zero; the next call's rebuild adds leaked:S, keeps S,
// lists nothing, and deletes no flag; and a Reserve on another pod while the
// source fails succeeds on the fast path. With no further call, a reachable
// probe tick runs the same rebuild.
//
// spec: §12.4 (Redis failure behavior), §6.2 (leaked slot semantics)
func TestRecoveryLeakedReleaseFailsWithClockZero_spec_12_4(t *testing.T) {
	for _, tc := range []struct {
		name    string
		byProbe bool
		srcErr  bool
	}{
		{"deadline then call", false, false},
		{"source error then call", false, true},
		{"deadline then probe", true, false},
		{"source error then probe", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newOutageEnv(t)
			a := e.replica()
			e.seedPod("Q", "q1")
			ctx := context.Background()
			if tc.srcErr {
				if err := e.cl.SAdd(ctx, "lenny:pod:{P}:active_slots", "S").Err(); err != nil {
					t.Fatal(err)
				}
				e.src.setErr(errors.New("postgres down"))
				if _, _, _, err := a.Release(ctx, "P", "S", true); err == nil {
					t.Fatal("the leaked release must fail on the source error")
				}
			} else {
				e.seedPod("P", "S")
				dead, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
				cancel()
				if _, _, _, err := a.Release(dead, "P", "S", true); err == nil {
					t.Fatal("the leaked release must fail on its deadline")
				}
			}
			if !a.OutageSinceForTest().IsZero() || a.LedgerLenForTest() != 1 {
				t.Fatalf("after the failure: clock zero %v (want zero), ledger %d (want 1)",
					a.OutageSinceForTest().IsZero(), a.LedgerLenForTest())
			}
			flagBefore := flagSet(t, e.cl, "P")
			if tc.byProbe {
				a.ProbeTickForTest(ctx)
			} else {
				mustReserve(t, a, "Q", "q2", 8)
			}
			wantMembers(t, e.cl, "P", "S", "leaked:S")
			if n := e.src.listCount(); n != 0 {
				t.Fatalf("listings = %d, want 0 with the clock zero", n)
			}
			if flagSet(t, e.cl, "P") != flagBefore {
				t.Fatal("a rebuild with the clock zero must delete no flag")
			}
		})
	}
}

// TestRecoveryLeakedFailureWithClockSet_spec_12_4 pins that with the clock
// set the same leaked failure records the mark and the next rebuild adds
// leaked:S.
//
// spec: §12.4 (Redis failure behavior), §6.2 (leaked slot semantics)
func TestRecoveryLeakedFailureWithClockSet_spec_12_4(t *testing.T) {
	e := newOutageEnv(t)
	a := e.replica()
	e.seedPod("P", "S")
	ctx := context.Background()
	e.down()
	a.ProbeTickForTest(ctx)
	e.up()
	dead, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	cancel()
	if _, _, _, err := a.Release(dead, "P", "S", true); err == nil {
		t.Fatal("the leaked release must fail on its deadline")
	}
	a.ProbeTickForTest(ctx)
	wantMembers(t, e.cl, "P", "S", "leaked:S")
}

// failKeysHook fails every command whose first key contains a configured
// fragment with a connection-refused error, so a test can fail one pod's
// rebuild script while the others succeed.
type failKeysHook struct {
	mu   sync.Mutex
	frag string
}

func (h *failKeysHook) set(frag string) {
	h.mu.Lock()
	h.frag = frag
	h.mu.Unlock()
}

func (h *failKeysHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *failKeysHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.mu.Lock()
		frag := h.frag
		h.mu.Unlock()
		if frag != "" && cmdTouches(cmd, frag) {
			err := errors.New("dial tcp: connect: connection refused")
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (h *failKeysHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

// cmdTouches reports whether any argument of cmd contains frag.
func cmdTouches(cmd redis.Cmder, frag string) bool {
	for _, arg := range cmd.Args() {
		if s, ok := arg.(string); ok && strings.Contains(s, frag) {
			return true
		}
	}
	return false
}
