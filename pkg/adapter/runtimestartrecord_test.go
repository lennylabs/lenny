// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"
	"testing"
	"time"
)

// claimForRecordTest claims the named slot the way a start's claim does and
// returns the claim, so the test can drive the rule-8 record directly.
func claimForRecordTest(t *testing.T, s *Server, id string) slotClaim {
	t.Helper()
	if s.WorkspaceBase == "" {
		s.WorkspaceBase = t.TempDir()
	}
	claim, err := s.claimSessionSlot(id, slotResolve{allowCreate: true}, false, false)
	if err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return claim
}

// recordedOn reports whether the session carries a rule-8 record.
func recordedOn(s *Server, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runtimeLive[id]
	return ok
}

// spec: 4.7.1 (The registry critical section), 4.7.10 (Runtime supervisor,
// Crash; Runtime supervisor, Liveness), 5.2 (Pod retirement policy, Runtime
// not live)
// diagnosis: the rule-8 record of a supervised start read neither the
// not-live state nor whether the generation's end was already handled, so an
// end handled between Runtime.Start and the record left the session recorded
// on a process that no longer ran, its start succeeded, and its Attach
// stream never ended with runtime_crash.
func TestSupervisedStartRecordRefusesAnEndedGeneration_spec_4_7_10(t *testing.T) {
	ends := []struct {
		event   string
		notLive bool
	}{
		{"exited by signal", true},
		{"exited 0", true},
		{"connection", true},
		{"ended", false},
	}
	for _, lifetime := range []RuntimeLifetime{LifetimeKeep, LifetimeRestart} {
		for _, end := range ends {
			t.Run(string(lifetime)+"/"+end.event, func(t *testing.T) {
				e := newExitsFixture(t, lifetime, LaunchSession)
				claim := claimForRecordTest(t, e.s, "b")
				if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
					t.Fatalf("Start: %v", err)
				}
				e.endBy(t, end.event)
				if end.notLive {
					if live, _ := e.rt.NotLive(); !live {
						t.Fatal("the end did not set the not-live state")
					}
				}
				ok, err := e.s.confirmRuntimeStart("b", claim.entry)
				if ok || !errors.Is(err, errRuntimeNotLive) {
					t.Fatalf("confirmRuntimeStart = %v, %v; want refused with errRuntimeNotLive", ok, err)
				}
				if recordedOn(e.s, "b") {
					t.Fatal("the session was recorded on a generation whose end was handled")
				}
			})
		}
	}
}

// spec: 4.7.10 (Runtime supervisor, Liveness), 5.2 (Pod retirement policy,
// Runtime not live)
// diagnosis: a start that joined a live process before the not-live state
// was set was recorded afterwards, so the pod served a session while the
// state was set.
func TestSupervisedStartRecordRefusesWhileNotLive_spec_4_7_10(t *testing.T) {
	e := newExitsFixture(t, LifetimeKeep, LaunchSession)
	claim := claimForRecordTest(t, e.s, "b")
	if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	e.rt.MarkNotLive(CauseHang)
	if e.s.noteRuntimeStarted("b", claim.entry) {
		t.Fatal("noteRuntimeStarted recorded a session while the not-live state was set")
	}
	if recordedOn(e.s, "b") {
		t.Fatal("the session was recorded while the not-live state was set")
	}
}

// spec: 4.7.1 (The registry critical section), 4.7.10 (Runtime supervisor,
// Crash)
func TestSupervisedStartRecordedBeforeTheEndIsCrashed_spec_4_7_10(t *testing.T) {
	e := newExitsFixture(t, LifetimeKeep, LaunchSession)
	claim := claimForRecordTest(t, e.s, "b")
	if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !e.s.noteRuntimeStarted("b", claim.entry) {
		t.Fatal("a start on the live generation was refused")
	}
	if e.s.noteRuntimeStarted("b", claim.entry) {
		t.Fatal("a second record consumed no outstanding start and was taken")
	}
	e.endBy(t, "exited by signal")
	if !e.crashed("b") {
		t.Fatal("the session recorded before the end did not fail with runtime_crash")
	}
}

// spec: 4.7.10 (Runtime process lifetime)
func TestSupervisedCloseDropsTheOutstandingStart_spec_4_7_10(t *testing.T) {
	e := newExitsFixture(t, LifetimeKeep, LaunchSession)
	claim := claimForRecordTest(t, e.s, "b")
	if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.s.Runtime.Close(context.Background(), "b"); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.s.confirmRuntimeStart("b", claim.entry); ok || !errors.Is(err, errRuntimeNotLive) {
		t.Fatalf("confirmRuntimeStart after Close = %v, %v; want refused", ok, err)
	}
}

// spec: 28.5.3 (CH-MSGSOCK, Session frame writes), 4.7.10 (Runtime
// supervisor, Liveness)
// diagnosis: the open sequence confirmed a start whose generation ended
// after Runtime.Start returned, instead of failing it as a not-live start so
// the caller releases the claim.
func TestOpenRuntimeSessionFailsWhenTheGenerationEndedAfterStart_spec_4_7_10(t *testing.T) {
	e := newExitsFixture(t, LifetimeKeep, LaunchSession)
	claim := claimForRecordTest(t, e.s, "b")
	if err := e.s.Runtime.Start(startCtx(t, 5*time.Second), "b"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	e.endBy(t, "exited by signal")
	confirmed, err := e.s.openRuntimeSession(startCtx(t, 5*time.Second), "b", claim, manifestInputs{}, false)
	if confirmed || !errors.Is(err, errRuntimeNotLive) {
		t.Fatalf("openRuntimeSession = %v, %v; want a not-live failure", confirmed, err)
	}
	if recordedOn(e.s, "b") {
		t.Fatal("the failed start left a rule-8 record")
	}
	e.s.mu.Lock()
	written := claim.entry.sessionStartWritten
	e.s.mu.Unlock()
	if written {
		t.Fatal("the failed start left session_start marked written")
	}
}
