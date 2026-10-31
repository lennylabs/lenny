// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the post-terminal hook's
// release step against the failure funnel.
//
// A failure report and a terminal write can land on one session together:
// a runtime crash is reported while a client terminates the session. The
// funnel holds the session's slot-accounting lock from before its recovery
// edge write through its slot accounting and its failed-disposition release,
// and the hook's release step takes the same lock. These cases drive the two
// actors with randomized start offsets, over both recovery edges and both
// pool kinds, under the race detector, and assert the invariants a race
// would break: the binding is released exactly once, with the failed
// disposition whenever the funnel's edge committed and with the terminal
// disposition otherwise; on a concurrent pool a drain the funnel's
// accounting requests is stamped before that release; and no goroutine
// outlives the race.
//
// spec: §5.2 (whole-pod replacement trigger), §7.3 (Resume flow after pod
// failure), §7.2 (Interactive Session Model).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// hookRaceRound is one randomized interleaving: the recovery edge the
// report selects, the pool kind, and each actor's start offset.
type hookRaceRound struct {
	retries         int64
	edge            session.State
	slot            bool
	reportDelay     time.Duration
	terminateDelay  time.Duration
	reporterYields  int
	terminateYields int
}

func newHookRaceRound(rng *rand.Rand) hookRaceRound {
	r := hookRaceRound{
		slot:            rng.Intn(2) == 0,
		reportDelay:     time.Duration(rng.Intn(300)) * time.Microsecond,
		terminateDelay:  time.Duration(rng.Intn(300)) * time.Microsecond,
		reporterYields:  rng.Intn(4),
		terminateYields: rng.Intn(4),
	}
	if rng.Intn(2) == 0 {
		r.retries, r.edge = 0, session.StateResumePending
	} else {
		r.retries, r.edge = 2, session.StateAwaitingClientAction
	}
	return r
}

// actorStart waits for start, then the round's offset, then yields n times.
func actorStart(start <-chan struct{}, delay time.Duration, yields int) {
	<-start
	time.Sleep(delay)
	for i := 0; i < yields; i++ {
		runtime.Gosched()
	}
}

// spec: §5.2 (whole-pod replacement trigger), §7.3 (Resume flow after pod
// failure), §7.2 (Interactive Session Model)
// diagnosis: a failure report and POST /terminate race on one bound session
// with randomized start offsets. The binding is released once; when the
// report's recovery edge committed, the release carries the failed
// disposition and, on a concurrent pool, follows the drain the report's
// accounting stamped; otherwise the terminal write's hook releases with the
// terminal disposition. A failure means the hook's release step ran while
// the funnel still held the session's slot-accounting lock, so the slot was
// released before its drain or with the wrong disposition, or the two paths
// released it twice or deadlocked.
func TestHookReleaseRacesFailureFunnel_spec_5_2(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	baseline := runtime.NumGoroutine()
	committed, refused := 0, 0
	for round := 0; round < 200; round++ {
		rr := newHookRaceRound(rng)
		if hookRaceOnce(t, round, rr) {
			committed++
		} else {
			refused++
		}
	}
	t.Logf("funnel edge committed first in %d rounds, terminal write first in %d", committed, refused)
	waitGoroutines(t, baseline, 5)
}

// hookRaceOnce runs one round and reports whether the funnel's edge
// committed.
func hookRaceOnce(t *testing.T, round int, rr hookRaceRound) bool {
	t.Helper()
	f := newFunnelRaceFixture(t, 4)
	id := fmt.Sprintf("sess-hook-%d", round)
	pod := fmt.Sprintf("pod-hook-%d", round)
	if rr.slot {
		// A sibling slot's earlier failure puts the pod one short of its
		// threshold of 2, so this report's accounting requests the drain.
		f.seedSlot(t, "sibling-"+id, pod, 2)
		if _, err := f.srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
			TenantID: "acme", SessionID: "sibling-" + id, Reason: "runtime_crash",
		}); err != nil {
			t.Fatalf("round %d: sibling report: %v", round, err)
		}
	}
	f.seedBound(t, id, pod, rr.retries, rr.slot)
	// spec: §10.2 (Session ownership) — the requests run as the seeded rows' owner.
	h := ownerprincipal.Wrap(f.srv.Handler(), ownerprincipal.Alice)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var disp sessionserver.FailureDisposition
	var code int
	wg.Add(2)
	go func() {
		defer wg.Done()
		actorStart(start, rr.reportDelay, rr.reporterYields)
		d, err := f.srv.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
			TenantID: "acme", SessionID: id, Reason: "runtime_crash",
		})
		if err != nil {
			t.Errorf("round %d: ReportSessionFailure: %v", round, err)
		}
		disp = d
	}()
	go func() {
		defer wg.Done()
		actorStart(start, rr.terminateDelay, rr.terminateYields)
		req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		code = rec.Code
	}()
	close(start)
	wg.Wait()

	if code != http.StatusOK {
		t.Fatalf("round %d: terminate answered %d, want 200", round, code)
	}
	funnelFirst := disp.To == rr.edge
	assertHookRaceEvents(t, round, f, id, pod, rr.slot, funnelFirst)
	return funnelFirst
}

// assertHookRaceEvents checks the one-release, disposition, and drain-order
// invariants of one round.
func assertHookRaceEvents(t *testing.T, round int, f *funnelRaceFixture, id, pod string, slot, funnelFirst bool) {
	t.Helper()
	events := f.events.list()
	if got := f.events.count("release:" + id); got != 1 {
		t.Fatalf("round %d: releases of %s = %d, want 1; events=%v", round, id, got, events)
	}
	wantDisp := "disposition:" + id + ":completed"
	if funnelFirst {
		wantDisp = "disposition:" + id + ":failed"
	}
	if f.events.count(wantDisp) != 1 {
		t.Fatalf("round %d (funnel first %v): events = %v, want %s", round, funnelFirst, events, wantDisp)
	}
	if !slot || !funnelFirst {
		return
	}
	drain, release := -1, -1
	for i, ev := range events {
		if ev == "drain:"+pod && drain < 0 {
			drain = i
		}
		if ev == "release:"+id && release < 0 {
			release = i
		}
	}
	if drain < 0 || drain > release {
		t.Fatalf("round %d: events = %v, want drain:%s before release:%s", round, events, pod, id)
	}
}

// seedBound inserts a running session bound to pod, to a slot when slot is
// true and to the whole pod otherwise. The binding carries the row's
// coordination_generation, as every production publish site stamps it, so
// the hook's stale-binding step leaves it to the release step.
func (f *funnelRaceFixture) seedBound(t *testing.T, id, pod string, retries int64, slot bool) {
	t.Helper()
	if err := f.store.Create(context.Background(), sessionstore.Session{
		UserID: ownerprincipal.Alice,
		ID:     id, TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning, RetryCount: retries,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	row, err := f.store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if row.CoordinationGeneration == 0 {
		t.Fatalf("seed %s: row generation is 0, so every binding would read as stale", id)
	}
	bind := &podsession.BindResult{
		SessionID: id, TenantID: "acme", SandboxName: pod,
		CoordinationGeneration: row.CoordinationGeneration,
	}
	if slot {
		bind.SlotID = id
	}
	f.registry.Put(bind)
}
