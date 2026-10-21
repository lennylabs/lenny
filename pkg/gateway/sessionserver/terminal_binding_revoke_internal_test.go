// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// failingExecutor unbinds the session like PodExecutor.Release and then
// reports the binder's error, so the binding is gone and the release failed.
type failingExecutor struct{ *funnelExecutor }

func (f failingExecutor) Release(ctx context.Context, sessionID string, d executor.Disposition) error {
	_ = f.funnelExecutor.Release(ctx, sessionID, d)
	return errors.New("binder: delete claim: apiserver unavailable")
}

// TestReleaseRevokedBindingOutcomes_spec_11_4 asserts each result
// ReleaseRevokedBinding returns to the full_revoke accounting: (true, nil)
// for a release whose executor call returned nil, (false, nil) for a
// non-terminal row, a refused release, no binding, and a stale binding it
// evicts with no binder call, and (false, err) for a primary-read error and
// an executor release error.
//
// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestReleaseRevokedBindingOutcomes_spec_11_4(t *testing.T) {
	ctx := context.Background()
	t.Run("released", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		bindAt(t, f, "s", session.StateCancelled, 3, 3)
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if !released || err != nil {
			t.Fatalf("ReleaseRevokedBinding = (%v, %v), want (true, nil)", released, err)
		}
		if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionCancelled {
			t.Errorf("releases = %v, want one cancelled", calls)
		}
		if f.srv.terminalClaims.size() != 0 {
			t.Error("the release mark survived the call")
		}
	})
	t.Run("non-terminal row", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		bindAt(t, f, "s", session.StateRunning, 3, 3)
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if released || err != nil || len(f.exec.callList()) != 0 {
			t.Errorf("ReleaseRevokedBinding = (%v, %v) with releases %v, want (false, nil) and none", released, err, f.exec.callList())
		}
		if _, ok := f.registry.Get("s"); !ok {
			t.Error("a running session's binding was removed")
		}
	})
	t.Run("refused while a terminal claim is open", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		bindAt(t, f, "s", session.StateCancelled, 3, 3)
		end := f.srv.BeginTerminal("s")
		defer end()
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if released || err != nil || len(f.exec.callList()) != 0 {
			t.Errorf("ReleaseRevokedBinding = (%v, %v), want (false, nil) with no release", released, err)
		}
	})
	t.Run("no binding", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		bindAt(t, f, "s", session.StateCancelled, 3, 3)
		f.registry.Remove("s")
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if released || err != nil || len(f.exec.callList()) != 0 {
			t.Errorf("ReleaseRevokedBinding = (%v, %v), want (false, nil) with no release", released, err)
		}
	})
	t.Run("stale binding evicted", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		bindAt(t, f, "s", session.StateCancelled, 3, 2)
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if released || err != nil || len(f.exec.callList()) != 0 {
			t.Errorf("ReleaseRevokedBinding = (%v, %v), want (false, nil) with no binder call", released, err)
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("the stale binding was not evicted")
		}
	})
	t.Run("primary read error", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "missing")
		if released || err == nil {
			t.Errorf("ReleaseRevokedBinding = (%v, %v), want (false, error)", released, err)
		}
	})
	t.Run("executor release error", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		f.srv.executor = failingExecutor{f.exec}
		bindAt(t, f, "s", session.StateCancelled, 3, 3)
		released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
		if released || err == nil || !strings.Contains(err.Error(), "apiserver unavailable") {
			t.Errorf("ReleaseRevokedBinding = (%v, %v), want (false, the executor error)", released, err)
		}
		if _, ok := f.registry.Get("s"); ok {
			t.Error("the executor left the binding registered")
		}
	})
}

// TestReleaseRevokedBindingLockWaitExpiry_spec_11_4 asserts that a
// full_revoke release whose call context expires while the failure funnel
// holds the session's slot-accounting lock returns an error, leaves the
// binding registered, and leaves no release mark, so the coordination
// Sweeper's later terminal binding release releases it exactly once.
//
// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestReleaseRevokedBindingLockWaitExpiry_spec_11_4(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	row, _ := bindAt(t, f, "s", session.StateCancelled, 3, 3)
	unlock, err := f.srv.slotAccountLocks.Lock(context.Background(), "s")
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	released, err := f.srv.ReleaseRevokedBinding(ctx, "acme", "s")
	if released || err == nil {
		t.Fatalf("ReleaseRevokedBinding = (%v, %v), want (false, error) on a lock-wait expiry", released, err)
	}
	if _, ok := f.registry.Get("s"); !ok {
		t.Fatal("the binding was removed although the release never ran")
	}
	if f.srv.terminalClaims.size() != 0 {
		t.Fatal("the release mark survived the expired call")
	}
	unlock()
	f.srv.ReleaseTerminalBinding(row)
	waitReleaseIdle(t, f.srv)
	if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionCancelled {
		t.Errorf("releases after the unlock = %v, want one cancelled", calls)
	}
}

// revokeReadyBound seeds a ready session whose pod is named on the row and
// whose binding this replica holds, a /start between registerBinding and
// commitStart, then cancels the row as the full_revoke cancel write does
// under a terminal claim. It returns the cancelled row and the claim's end.
func revokeReadyBound(t *testing.T, f *funnelFixture) (sessionstore.Session, func()) {
	t.Helper()
	bindAt(t, f, "s", session.StateReady, 3, 3)
	end := f.srv.BeginTerminal("s")
	row, err := f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
		r.PodAssignment = "pod-x"
		from := r.State
		r.State = session.StateCancelled
		sessionstore.MarkTerminated(r, from, time.Now())
		return nil
	})
	if err != nil {
		t.Fatalf("cancel write: %v", err)
	}
	return row, end
}

// deletes returns the claim deletes the fixture's binder issued.
func (f *funnelFixture) deletes() []string {
	var out []string
	for _, c := range f.clientCalls.list() {
		if strings.HasPrefix(c, "delete:") {
			out = append(out, c)
		}
	}
	return out
}

// TestFullRevokeClaimFencesTheSweeperRelease_spec_11_4 asserts the
// full_revoke claim fence on a ready session bound on the handling replica
// of an exclusive pool. A coordination Sweeper release made between the
// cancel write and the pre-running reclaim starts no release, the reclaim
// skips the locally bound session, and the full_revoke binding release then
// releases the binding once with the cancelled disposition, with no by-name
// claim delete after it. Without the fence the Sweeper's release would run
// first and the reclaim would then delete the claim by name. The same
// Sweeper call made once the claim ended starts the release.
//
// spec: §11.4 (User Invalidation), §10.1.5 (Stale Replica Behavior)
func TestFullRevokeClaimFencesTheSweeperRelease_spec_11_4(t *testing.T) {
	t.Run("between the cancel write and the reclaim", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, end := revokeReadyBound(t, f)
		f.srv.ReleaseTerminalBinding(row)
		time.Sleep(20 * time.Millisecond)
		if calls := f.exec.callList(); len(calls) != 0 {
			t.Fatalf("releases = %v; the Sweeper released while the full_revoke claim was open", calls)
		}
		f.srv.ReclaimRevokedPreRunning(context.Background(), session.StateReady, row)
		end()
		released, err := f.srv.ReleaseRevokedBinding(context.Background(), "acme", "s")
		if !released || err != nil {
			t.Fatalf("ReleaseRevokedBinding = (%v, %v), want (true, nil)", released, err)
		}
		f.srv.ReleaseTerminalBinding(row)
		waitReleaseIdle(t, f.srv)
		if calls := f.exec.callList(); len(calls) != 1 || calls[0] != executor.DispositionCancelled {
			t.Errorf("releases = %v, want exactly one cancelled", calls)
		}
		if d := f.deletes(); len(d) != 0 {
			t.Errorf("claim deletes = %v, want none after the binding release", d)
		}
	})
	t.Run("after the claim ended", func(t *testing.T) {
		f := newFunnelFixture(t, funnelOpts{})
		row, end := revokeReadyBound(t, f)
		end()
		f.srv.ReleaseTerminalBinding(row)
		waitReleaseIdle(t, f.srv)
		if calls := f.exec.callList(); len(calls) != 1 {
			t.Errorf("releases = %v, want the Sweeper's release once the claim ended", calls)
		}
	})
}

// TestReclaimRevokedPreRunningReclaimsAnUnboundSession_spec_11_4 asserts
// the full_revoke pre-running reclaim deletes the claim of a cancelled
// ready session whose binding no replica-local registry entry holds, and
// reclaims nothing for a session that had started.
//
// spec: §11.4 (User Invalidation), §4.6.3 (CRD Field Ownership and Write
// Boundaries)
func TestReclaimRevokedPreRunningReclaimsAnUnboundSession_spec_11_4(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{})
	row, end := revokeReadyBound(t, f)
	defer end()
	f.registry.Remove("s")
	f.srv.ReclaimRevokedPreRunning(context.Background(), session.StateRunning, row)
	if d := f.deletes(); len(d) != 0 {
		t.Fatalf("claim deletes = %v, want none for a session that had started", d)
	}
	f.srv.ReclaimRevokedPreRunning(context.Background(), session.StateReady, row)
	if d := f.deletes(); len(d) != 1 || d[0] != "delete:claim-pod-x" {
		t.Errorf("claim deletes = %v, want one by-name delete of claim-pod-x", d)
	}
}
