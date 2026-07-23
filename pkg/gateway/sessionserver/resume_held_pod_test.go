// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.2 path 6 (lines 326-327) — the pod-held branch atomically resumes a
// suspended session (suspended → running) so the caller can deliver; §7.2 path
// 6 (line 330 queued fallback) — a resume that cannot commit fails closed
// without resurrecting a terminal row.

func seedResumeRow(t *testing.T, store sessionstore.Store, id string, st session.State, pod string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice", RuntimeRef: "echo",
		State: st, PodAssignment: pod, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestResumeHeldPodTransitionsSuspendedWithBoundPod pins the §7.2 path-6
// pod-held happy transition: resumeHeldPod on a suspended row that still holds
// a bound pod writes suspended → running, reuses the bound PodAssignment
// without a fresh claim, and emits the resume status-change and session.resumed
// audit that handleResume emits.
//
// spec: 7.2 (path 6 pod-held resume, lines 326-327), 11.7 (session.resumed audit)
//
// diagnosis: a failure means the coordinator did not atomically resume a
// pod-held suspended session, cleared or re-claimed its bound pod, or did not
// surface the resume status-change / lifecycle audit on the reused pod.
func TestResumeHeldPodTransitionsSuspendedWithBoundPod(t *testing.T) {
	store := memstore.New()
	seedResumeRow(t, store, "s-held", session.StateSuspended, "pod-abc")
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{Events: bus, LifecycleAuditSink: sink})

	if err := srv.resumeHeldPod(context.Background(), "acme", "s-held"); err != nil {
		t.Fatalf("resumeHeldPod: %v", err)
	}

	row, _ := store.Get(context.Background(), "acme", "s-held")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running", row.State)
	}
	if row.PodAssignment != "pod-abc" {
		t.Errorf("pod assignment = %q, want pod-abc reused (no fresh claim)", row.PodAssignment)
	}
	if got := sseEventsOfType(bus, "s-held", "status_change"); len(got) != 1 {
		t.Errorf("status_change count = %d, want 1", len(got))
	}
	if len(sink.events) != 1 || sink.events[0].EventType != auditSessionResumed {
		t.Errorf("audit = %+v, want one session.resumed", sink.events)
	}
}

// TestResumeHeldPodTransitionsDevModeShapeWithNoPod pins that the guard does
// not require a bound pod: a suspended row whose PodAssignment == "" (the
// --dev-mode / nil-pod-binder shape the tier-4 target runs, where the gateway
// builds no pod binder yet the session still holds its in-process executor)
// still transitions suspended → running. A guard that gated on a non-empty
// PodAssignment would reject every dev-mode suspended row and make the tier-4
// delivered outcome unreachable.
//
// spec: 7.2 (path 6 pod-held resume, lines 326-327)
//
// diagnosis: a failure means the resume guard began requiring a bound pod,
// wrongly rejecting a pod-unbound (dev-mode) suspended row to the queued
// fallback so the tier-4 dev-mode resume-and-deliver could not reach running.
func TestResumeHeldPodTransitionsDevModeShapeWithNoPod(t *testing.T) {
	store := memstore.New()
	seedResumeRow(t, store, "s-dev", session.StateSuspended, "")
	srv := New(store, Options{})

	if err := srv.resumeHeldPod(context.Background(), "acme", "s-dev"); err != nil {
		t.Fatalf("resumeHeldPod (dev-mode shape): %v", err)
	}

	row, _ := store.Get(context.Background(), "acme", "s-dev")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running for an empty-PodAssignment suspended row", row.State)
	}
}

// TestResumeHeldPodMisrouteReturnsSentinelNoTransition pins the fail-closed
// misroute path: resumeHeldPod on a non-suspended row (a routing bug that fed a
// running row into the resume-and-deliver action) returns
// errResumeHeldPodNotSuspended from inside the store.Update mutator, performs no
// transition and no fresh claim, and emits no resume status-change or audit. The
// caller maps the sentinel to the §7.2 line-330 queued fallback.
//
// spec: 7.2 (path 6 line 330 queued fallback)
//
// diagnosis: a failure means the resume guard stopped fail-closing on a
// misrouted non-suspended row — it silently transitioned, claimed a pod, or
// swallowed the sentinel the queued fallback branches on.
func TestResumeHeldPodMisrouteReturnsSentinelNoTransition(t *testing.T) {
	store := memstore.New()
	seedResumeRow(t, store, "s-run", session.StateRunning, "pod-xyz")
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	srv := New(store, Options{Events: bus, LifecycleAuditSink: sink})

	err := srv.resumeHeldPod(context.Background(), "acme", "s-run")
	if !errors.Is(err, errResumeHeldPodNotSuspended) {
		t.Fatalf("err = %v, want errResumeHeldPodNotSuspended", err)
	}
	row, _ := store.Get(context.Background(), "acme", "s-run")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running unchanged (no transition on a misroute)", row.State)
	}
	if got := sseEventsOfType(bus, "s-run", "status_change"); len(got) != 0 {
		t.Errorf("status_change count = %d, want 0 on a rejected misroute", len(got))
	}
	if len(sink.events) != 0 {
		t.Errorf("audit events = %+v, want none on a rejected misroute", sink.events)
	}
}

// terminalRaceStore models a concurrent terminal transition (a legal
// Suspended → Cancelled DELETE) acquiring the row lock immediately before
// resumeHeldPod's mutator runs. The handler routed ActionResumeAndDeliver
// because it observed the row suspended, but by the time the store.Update
// mutator executes the row is already terminal. Because the guard runs inside
// the mutator (rather than as a separate pre-Update store.Get), it observes the
// terminal state under the same lock and rejects the write. A pre-Update-Get
// guard would have seen suspended and let transitionResume resurrect the
// terminal row to running.
type terminalRaceStore struct {
	sessionstore.Store
	target  string
	flipped bool
}

func (r *terminalRaceStore) Update(
	ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error,
) (sessionstore.Session, error) {
	if id == r.target && !r.flipped {
		r.flipped = true
		if _, err := r.Store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
			row.State = session.StateCancelled
			return nil
		}); err != nil {
			return sessionstore.Session{}, err
		}
	}
	return r.Store.Update(ctx, tenantID, id, mutate)
}

// TestResumeHeldPodRejectsConcurrentTerminalTransition pins the atomicity of
// the guard: when a row is suspended at routing time but terminal by the time
// the store.Update mutator runs, the in-mutator guard rejects the write so no
// illegal terminal → running state is persisted. This is the fail-closed path
// the handler's resume-failure queued fallback drives.
//
// spec: 7.2 (path 6 line 330 queued fallback — the message is not silently
// dropped and a terminal row is not resurrected)
//
// diagnosis: a failure means the resume guard is not atomic with the write — a
// concurrent terminal transition landing between routing and the mutator
// resurrected a cancelled/expired/failed session to running.
func TestResumeHeldPodRejectsConcurrentTerminalTransition(t *testing.T) {
	inner := memstore.New()
	seedResumeRow(t, inner, "s-race", session.StateSuspended, "pod-race")
	store := &terminalRaceStore{Store: inner, target: "s-race"}
	bus := sessionevents.NewBus(64)
	srv := New(store, Options{Events: bus})

	err := srv.resumeHeldPod(context.Background(), "acme", "s-race")
	if !errors.Is(err, errResumeHeldPodNotSuspended) {
		t.Fatalf("err = %v, want errResumeHeldPodNotSuspended (terminal row must be rejected)", err)
	}
	row, _ := inner.Get(context.Background(), "acme", "s-race")
	if row.State != session.StateCancelled {
		t.Errorf("state = %q, want cancelled preserved (no terminal → running resurrection)", row.State)
	}
	if got := sseEventsOfType(bus, "s-race", "status_change"); len(got) != 0 {
		t.Errorf("status_change count = %d, want 0 when the resume was rejected", len(got))
	}
}
