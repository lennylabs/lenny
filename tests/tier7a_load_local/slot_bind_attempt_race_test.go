// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local ordering coverage for the §4.7.1 bind attempt fence on
// the adapter's slot registry: the start confirmation that rule 8 states and
// the registry critical section that makes the resolve and the stamp one
// step.
//
// A start claims its slot, releases the registry lock, and runs
// Runtime.Start outside it. A §7.1 compensating Shutdown can reclaim the slot
// in that window. The start then confirms, under the lock again, that the
// registry still holds the entry its claim was admitted against before it
// records the pod's shared runtime process as holding the session. The
// cases below park a start inside Runtime.Start, drive the reclaim into the
// window, and assert the start's refusal and the pod-level outcome on a pod
// with and without a co-tenant. Resume holds the slot's per-slot guard for
// the whole call, so a reclaim racing a Resume waits for it instead and
// reclaims the session the Resume started.
//
// spec: §4.7.1 (role and gateway RPC contract), §7.1 (normal flow).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// bindRaceIterations is how many fresh fixtures each arm drives. Each
// iteration forces its ordering through a park, so repetition exercises the
// surrounding lock and guard traffic under -race rather than searching for
// a schedule; the stress budget carries the long sweep.
const bindRaceIterations = 20

// guardBlockedWindow is how long a reclaim that must be blocked on a
// Resume's per-slot guard is watched before the case releases the Resume.
const guardBlockedWindow = 200 * time.Millisecond

// raceBob is the co-tenant a co-tenanted arm starts before the race.
const raceBob = "bob"

// startParked arms rt so the named session's Start parks until unpark is
// closed, and returns the channel that reports it has parked. Only the
// first Start for the session parks.
func startParked(rt *hookRuntime, sessionID string) (parked, unpark chan struct{}) {
	parked, unpark = make(chan struct{}), make(chan struct{})
	var once sync.Once
	rt.mu.Lock()
	rt.onStart = func(id string) {
		if id != sessionID {
			return
		}
		once.Do(func() {
			close(parked)
			<-unpark
		})
	}
	rt.mu.Unlock()
	return parked, unpark
}

// bindRacePod builds a reclaim pod with a counting cleanup-outcome reporter
// and, when coTenant is set, bob bound and started on it first.
func bindRacePod(t *testing.T, coTenant bool) (*adapter.Server, *hookRuntime, *holdScrubReporter) {
	t.Helper()
	// The adapter caches its pod id at construction, and it withholds a
	// cleanup-outcome report when that id is empty.
	t.Setenv("POD_NAME", "pod-bind-attempt-race")
	rt := &hookRuntime{}
	s := reclaimPod(t, rt)
	reporter := newHoldScrubReporter()
	s.SessionScrubReporter = reporter
	if coTenant {
		if err := assignCreds(s, raceBob, "attempt-bob"); err != nil {
			t.Fatalf("assign bob: %v", err)
		}
		if err := startSession(s, raceBob); err != nil {
			t.Fatalf("start bob: %v", err)
		}
	}
	return s, rt, reporter
}

// resumeConversationOnly sends a Resume naming token with no chunks, so the
// call reaches Runtime.Start with no extraction.
func resumeConversationOnly(s *adapter.Server, sessionID, token string) error {
	_, err := s.Resume(context.Background(), &adapterv1.ResumeRequest{
		SessionId:    &adapterv1.SessionId{Value: sessionID},
		CheckpointId: "ckpt-1",
		BindAttempt:  token,
	})
	return err
}

// ranClose reports whether the runtime recorded a Close for sessionID.
func ranClose(rt *hookRuntime, sessionID string) bool {
	for _, iv := range rt.snapshot() {
		if iv.what == "close "+sessionID {
			return true
		}
	}
	return false
}

// tenancies names the pod variants every arm runs on.
var tenancies = []struct {
	name     string
	coTenant bool
}{{"sole", false}, {"co-tenanted", true}}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// diagnosis: a start whose slot the compensating Shutdown reclaimed while
// Runtime.Start ran was recorded as holding the session, or a reclaim racing
// a Resume ran inside it. A recorded start puts a session the registry no
// longer holds into the shared runtime's generation for the life of the pod,
// which empties SoleSessionID on a co-tenanted pod and keeps the pod-wide
// MCP surface and the direct-mode token fold from ever naming the incumbent.
func TestAStartParkedInsideRuntimeStartRacesTheReclaim_spec_4_7_1(t *testing.T) {
	arms := []struct {
		name string
		run  func(t *testing.T, coTenant bool, i int)
	}{
		{"StartSession", startSessionLosesToTheReclaim},
		{"Resume", resumeSerializesTheReclaim},
	}
	for _, arm := range arms {
		for _, ten := range tenancies {
			t.Run(arm.name+"/"+ten.name, func(t *testing.T) {
				for i := range bindRaceIterations {
					arm.run(t, ten.coTenant, i)
				}
			})
		}
	}
}

// startSessionLosesToTheReclaim parks alice's StartSession inside
// Runtime.Start, lets the compensating Shutdown reclaim the slot to
// completion, and then releases the start. StartSession holds no per-slot
// guard, so this is the only ordering a parked start admits.
func startSessionLosesToTheReclaim(t *testing.T, coTenant bool, i int) {
	t.Helper()
	s, rt, reporter := bindRacePod(t, coTenant)
	if err := assignCreds(s, "alice", "attempt-1"); err != nil {
		t.Fatalf("iteration %d: assign alice: %v", i, err)
	}
	parked, unpark := startParked(rt, "alice")
	startDone := make(chan error, 1)
	go func() { startDone <- startSession(s, "alice") }()
	<-parked

	resp, err := fencedReclaim(s, "alice", "attempt-1")
	if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Fatalf("iteration %d: reclaim = (%v, %v), want RECLAIMED", i, resp.GetSlotReclaim(), err)
	}
	close(unpark)
	if err := <-startDone; status.Code(err) != codes.Aborted {
		t.Fatalf("iteration %d: StartSession = %v, want Aborted for a start whose slot was reclaimed", i, err)
	}
	wantSole := ""
	if coTenant {
		wantSole = raceBob
	}
	if got := s.SoleSessionID(); got != wantSole {
		t.Errorf("iteration %d: SoleSessionID = %q, want %q; the refused start was recorded", i, got, wantSole)
	}
	if n := reporter.counts()["alice"]; n != 0 {
		t.Errorf("iteration %d: cleanup-outcome reports for alice = %d, want 0 for a slot that never reached running", i, n)
	}
	if slotTreeExists(s, "alice") {
		t.Errorf("iteration %d: alice's slot tree survived the reclaim", i)
	}
}

// resumeSerializesTheReclaim parks alice's Resume inside Runtime.Start and
// issues the compensating Shutdown. The Shutdown blocks on the per-slot
// guard the Resume holds; once the Resume returns having recorded the
// session, the Shutdown reclaims a slot that reached running, closes the
// runtime for it and files exactly one cleanup-outcome report.
func resumeSerializesTheReclaim(t *testing.T, coTenant bool, i int) {
	t.Helper()
	s, rt, reporter := bindRacePod(t, coTenant)
	parked, unpark := startParked(rt, "alice")
	resumeDone := make(chan error, 1)
	go func() { resumeDone <- resumeConversationOnly(s, "alice", "attempt-1") }()
	<-parked

	reclaimDone := make(chan *adapterv1.ShutdownResponse, 1)
	go func() {
		r, err := fencedReclaim(s, "alice", "attempt-1")
		if err != nil {
			t.Errorf("iteration %d: reclaim: %v", i, err)
		}
		reclaimDone <- r
	}()
	select {
	case <-reclaimDone:
		t.Fatalf("iteration %d: the reclaim returned while the Resume held the slot's guard", i)
	case <-time.After(guardBlockedWindow):
	}
	close(unpark)
	if err := <-resumeDone; err != nil {
		t.Fatalf("iteration %d: Resume = %v, want success; the reclaim cannot land inside it", i, err)
	}
	if r := <-reclaimDone; r.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Fatalf("iteration %d: reclaim = %v, want RECLAIMED", i, r.GetSlotReclaim())
	}
	if !ranClose(rt, "alice") {
		t.Errorf("iteration %d: the reclaim of a started session ran no runtime close", i)
	}
	if n := reporter.counts()["alice"]; n != 1 {
		t.Errorf("iteration %d: cleanup-outcome reports for alice = %d, want exactly 1 for a slot that reached running", i, n)
	}
	if a, b, ok := overlapping(rt.snapshot()); ok {
		t.Errorf("iteration %d: %s overlapped %s", i, a.what, b.what)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// diagnosis: a start parked inside Runtime.Start confirmed against a
// successor's entry. The reclaim removed the start's own entry and a later
// attempt re-created one under the same slot identifier with the same
// session, so a confirmation reading only the session bound on the entry
// records the abandoned start onto the successor, and a rollback keyed on
// the session identifier deletes the successor's staged tree.
func TestAParkedStartRefusesTheSuccessorsEntry_spec_4_7_1(t *testing.T) {
	for _, ten := range tenancies {
		t.Run(ten.name, func(t *testing.T) {
			for i := range bindRaceIterations {
				reverseOrderingIteration(t, ten.coTenant, i)
			}
		})
	}
}

func reverseOrderingIteration(t *testing.T, coTenant bool, i int) {
	t.Helper()
	s, rt, reporter := bindRacePod(t, coTenant)
	if err := assignCreds(s, "alice", "attempt-1"); err != nil {
		t.Fatalf("iteration %d: assign alice: %v", i, err)
	}
	parked, unpark := startParked(rt, "alice")
	startDone := make(chan error, 1)
	go func() { startDone <- startSession(s, "alice") }()
	<-parked

	if _, err := fencedReclaim(s, "alice", "attempt-1"); err != nil {
		t.Fatalf("iteration %d: reclaim: %v", i, err)
	}
	if err := assignCreds(s, "alice", "attempt-2"); err != nil {
		t.Fatalf("iteration %d: the successor's bind was refused after the reclaim returned: %v", i, err)
	}
	close(unpark)
	if err := <-startDone; status.Code(err) != codes.Aborted {
		t.Fatalf("iteration %d: StartSession = %v, want Aborted against the successor's entry", i, err)
	}
	if !slotTreeExists(s, "alice") {
		t.Fatalf("iteration %d: the refused start's rollback removed the successor's tree", i)
	}
	if n := reporter.counts()["alice"]; n != 0 {
		t.Errorf("iteration %d: cleanup-outcome reports for alice = %d, want 0", i, n)
	}
	resp, err := fencedReclaim(s, "alice", "attempt-2")
	if err != nil || resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
		t.Errorf("iteration %d: reclaim naming the successor = (%v, %v), want RECLAIMED; its entry did not survive",
			i, resp.GetSlotReclaim(), err)
	}
}

// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
//
// diagnosis: two bind attempts racing the first resolve of one slot
// identifier were both admitted, or the entry ended up stamped with a
// loser's token. The resolve, the creation and the stamp are one step under
// the registry lock; a resolve that released the lock between the lookup
// and the stamp admits a second attempt onto the entry, and that attempt's
// compensation then destroys the winner's session.
func TestConcurrentBindAttemptsResolveExactlyOneOwner_spec_4_7_1(t *testing.T) {
	const attempts = 8
	for i := range bindRaceIterations {
		s := reclaimPod(t, &hookRuntime{})
		start := newRaceStart(attempts)
		errs := make([]error, attempts)
		var wg sync.WaitGroup
		for a := range attempts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start.arrive()
				errs[a] = assignCreds(s, "alice", fmt.Sprintf("attempt-%d", a))
			}()
		}
		start.release(t)
		wg.Wait()

		winner := -1
		for a, err := range errs {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("iteration %d: attempts %d and %d were both admitted onto one slot", i, winner, a)
				}
				winner = a
				continue
			}
			if status.Code(err) != codes.Aborted || errorCode(err) != adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED {
				t.Errorf("iteration %d: attempt %d = %v, want the superseded refusal", i, a, err)
			}
		}
		if winner < 0 {
			t.Fatalf("iteration %d: no attempt was admitted", i)
		}
		loser := (winner + 1) % attempts
		if resp, err := fencedReclaim(s, "alice", fmt.Sprintf("attempt-%d", loser)); err != nil ||
			resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
			t.Errorf("iteration %d: reclaim naming a loser = (%v, %v), want SUPERSEDED", i, resp.GetSlotReclaim(), err)
		}
		if resp, err := fencedReclaim(s, "alice", fmt.Sprintf("attempt-%d", winner)); err != nil ||
			resp.GetSlotReclaim() != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
			t.Errorf("iteration %d: reclaim naming the winner = (%v, %v), want RECLAIMED; the entry is not stamped with its token",
				i, resp.GetSlotReclaim(), err)
		}
	}
}

// errorCode returns the adapterv1.Error code a status carries in its
// detail, or zero when it carries none.
func errorCode(err error) adapterv1.Error_ErrorCode {
	st, ok := status.FromError(err)
	if !ok {
		return 0
	}
	for _, d := range st.Details() {
		if e, ok := d.(*adapterv1.Error); ok {
			return e.GetCode()
		}
	}
	return 0
}
