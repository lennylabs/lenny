// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// waitReleaseIdle waits until no terminal binding release runs for the
// session on this replica.
func waitReleaseIdle(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.terminalClaims.size() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("terminal claim record = %d entries after 5s", srv.terminalClaims.size())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// bindAt seeds a terminal row at gen and publishes a binding stamped stamp.
func bindAt(t *testing.T, f *funnelFixture, id string, st session.State, gen, stamp int64) (sessionstore.Session, *podsession.BindResult) {
	t.Helper()
	if err := f.store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner,
		ID:     id, TenantID: "acme", RuntimeRef: "echo", State: st,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	row, _ := f.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		r.CoordinationGeneration = gen
		return nil
	})
	bind := &podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: "pod-x", CoordinationGeneration: stamp}
	f.registry.Put(bind)
	return row, bind
}

// TestReleaseTerminalBindingReleasesOnlyItsOwnBinding_spec_7_2 asserts that
// ReleaseTerminalBinding releases a bound terminal session with the
// disposition of its state, evicts a binding stamped below the row's
// generation or at 0 without a release, does nothing for an unbound session,
// a non-terminal row, or an open terminal claim, and starts one release for
// two calls.
//
// spec: §7.2 (Interactive Session Model), §10.1.5 (Stale Replica Behavior)
func TestReleaseTerminalBindingReleasesOnlyItsOwnBinding_spec_7_2(t *testing.T) {
	for _, tc := range []struct {
		state session.State
		want  executor.Disposition
	}{
		{session.StateCompleted, executor.DispositionCompleted},
		{session.StateFailed, executor.DispositionFailed},
		{session.StateCancelled, executor.DispositionCancelled},
		{session.StateExpired, executor.DispositionExpired},
	} {
		t.Run("releases "+string(tc.state), func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			row, _ := bindAt(t, f, "s", tc.state, 3, 3)
			f.srv.ReleaseTerminalBinding(row)
			f.srv.ReleaseTerminalBinding(row)
			waitReleaseIdle(t, f.srv)
			if calls := f.exec.callList(); len(calls) != 1 || calls[0] != tc.want {
				t.Errorf("releases = %v, want one %q", calls, tc.want)
			}
			if _, ok := f.registry.Get("s"); ok {
				t.Error("the binding survived its release")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		stamp int64
	}{{"stamped below the row", 2}, {"unstamped", 0}} {
		t.Run("evicts a binding "+tc.name, func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			row, _ := bindAt(t, f, "s", session.StateCancelled, 3, tc.stamp)
			f.srv.ReleaseTerminalBinding(row)
			waitReleaseIdle(t, f.srv)
			if calls := f.exec.callList(); len(calls) != 0 {
				t.Errorf("releases = %v, want none: a peer owns the pod", calls)
			}
			if _, ok := f.registry.Get("s"); ok {
				t.Error("the stale binding was not evicted")
			}
		})
	}
	t.Run("non-terminal row", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, _ := bindAt(t, f, "s", session.StateRunning, 3, 3)
		f.srv.ReleaseTerminalBinding(row)
		waitReleaseIdle(t, f.srv)
		if _, ok := f.registry.Get("s"); !ok || len(f.exec.callList()) != 0 {
			t.Error("a running session's binding was released")
		}
	})
	t.Run("unbound session", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		f.srv.ReleaseTerminalBinding(sessionstore.Session{UserID: testOwner, ID: "none", TenantID: "acme", State: session.StateCancelled})
		waitReleaseIdle(t, f.srv)
		if len(f.exec.callList()) != 0 {
			t.Error("an unbound session was released")
		}
	})
	t.Run("open terminal claim defers", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
		end := f.srv.BeginTerminal("s")
		f.srv.ReleaseTerminalBinding(row)
		time.Sleep(20 * time.Millisecond)
		if len(f.exec.callList()) != 0 {
			t.Fatal("a release ran while the writer's claim was open")
		}
		end()
		end() // idempotent
		f.srv.ReleaseTerminalBinding(row)
		waitReleaseIdle(t, f.srv)
		if calls := f.exec.callList(); len(calls) != 1 {
			t.Errorf("releases after the claim ended = %v, want one", calls)
		}
	})
	t.Run("waits for the slot-accounting lock", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
		unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "s")
		if err != nil {
			t.Fatalf("lock: %v", err)
		}
		f.srv.ReleaseTerminalBinding(row)
		time.Sleep(20 * time.Millisecond)
		if len(f.exec.callList()) != 0 {
			t.Fatal("the release ran while the slot accounting held the lock")
		}
		unlock()
		waitReleaseIdle(t, f.srv)
		if calls := f.exec.callList(); len(calls) != 1 {
			t.Errorf("releases after the unlock = %v, want one", calls)
		}
	})
}

// TestPostTerminalHookRunsItsStepsInOrder_spec_7_2 asserts that the
// post-terminal hook cancels a same-replica attempt, drains, emits one
// status_change with the row's state and one session_complete, runs the
// writer's signals, and then releases the pod with the state's disposition,
// and that the terminal events are emitted when signals is nil.
//
// spec: §7.1 (Normal Flow), §7.2 (Interactive Session Model)
func TestPostTerminalHookRunsItsStepsInOrder_spec_7_2(t *testing.T) {
	for _, withSignals := range []bool{true, false} {
		t.Run(map[bool]string{true: "with signals", false: "nil signals"}[withSignals], func(t *testing.T) {
			f := newFunnelFixture(t, funnelOpts{})
			row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
			cancelled := false
			if !f.srv.resumeAttempts.begin("s", func() { cancelled = true; f.log.add("cancel") }) {
				t.Fatal("begin refused")
			}
			defer f.srv.resumeAttempts.end("s")
			var signals func(context.Context, sessionstore.Session)
			if withSignals {
				signals = func(context.Context, sessionstore.Session) { f.log.add("signals") }
			}
			got := f.srv.AfterTerminalWrite(context.Background(), session.StateRunning, row, signals)
			if !cancelled || got.State != session.StateCancelled {
				t.Errorf("cancelled %v, returned row %q; want the attempt cancelled and the row returned", cancelled, got.State)
			}
			events := f.log.list()
			want := []string{"cancel"}
			if withSignals {
				want = append(want, "signals")
			}
			want = append(want, "release:cancelled")
			if len(events) != len(want) {
				t.Fatalf("steps = %v, want %v", events, want)
			}
			for i := range want {
				if events[i] != want[i] {
					t.Errorf("steps = %v, want %v", events, want)
					break
				}
			}
		})
	}
}
