// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos scenarios for the off-holder forward on CH-HOLDERFORWARD and
// for the drain's binding release on CH-BINDRELEASE, on the lifecycle
// harness with two replicas: the coordinator crashes after it read a
// forwarded message; Redis fails and the REG-COORDLEASE lease moves to the
// Postgres fallback, from which the serving replica resolves the forward
// target; and the drain's binding-release fan-out reaches only the replica
// that ran the drain, so the erasure job's store deletions wait on the §7.1
// post-terminal window until the peer releases the binding or the window cap
// passes. Each scenario asserts its own outcome and runs no invariant
// checker.
//
// spec: §29.3 (Interactive message send) Forwarding, §10.1.1 (Stateless
// Replicas and Per-Session Coordination), §28.5.7 (Gateway-to-store), §12.8
// (Compliance Interfaces: Lifecycle fence and drain)
package tier8_chaos_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// forwardChaosUser is the user every harness session is created for.
const forwardChaosUser = "alice@acme.com"

// forwardReceipt reads the status of the first delivery receipt of a message
// answer.
func forwardReceipt(body map[string]any) string {
	if r, ok := body["deliveryReceipt"].(map[string]any); ok {
		s, _ := r["status"].(string)
		return s
	}
	if rs, ok := body["deliveryReceipts"].([]any); ok && len(rs) > 0 {
		if r, ok := rs[0].(map[string]any); ok {
			s, _ := r["status"].(string)
			return s
		}
	}
	return ""
}

// timesReceived counts how many times the session's runtime received text.
func timesReceived(h *lifecycleharness.Harness, id, text string) int {
	n := 0
	for _, got := range h.Cluster.ReceivedTexts(id) {
		if got == text {
			n++
		}
	}
	return n
}

// requireNothingHeld fails when the session's DLQ or inbox holds an entry or
// its runtime received text.
func requireNothingHeld(t *testing.T, h *lifecycleharness.Harness, id, text string) {
	t.Helper()
	if n := dlqLen(t, h, id); n != 0 {
		t.Errorf("DLQ of %s holds %d entries, want none", id, n)
	}
	if n, err := h.Inbox.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
		t.Errorf("inbox of %s holds %d entries (%v), want none", id, n, err)
	}
	if n := timesReceived(h, id, text); n != 0 {
		t.Errorf("the runtime received %q %d times, want never", text, n)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rule 7, 7.2 (Message
// delivery routing — seven paths, path 6)
// diagnosis: the coordinating replica crashed after it read a message
// forwarded to it over CH-HOLDERFORWARD and before it answered, and the
// serving replica treated the forward as not delivered: it held the message
// in the DLQ or the inbox, delivered it itself, or answered other than 409
// TARGET_NOT_READY with the session's current state. An outcome-unknown
// forward may already have been applied, so holding it could deliver it
// twice. Check the outcome classification in
// pkg/gateway/sessionserver/holder_forward.go and forwardMessageBatch in
// pkg/gateway/sessionserver/messages_delivery.go.
func TestCoordinatorCrashAfterReadingAForwardIsRefusedAndNotHeld_spec_29_3(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 4})
	coordinator, peer := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(coordinator)

	var read atomic.Int64
	coordinator.SetForwardFault(func(*http.Request) bool {
		read.Add(1)
		h.Crash(coordinator)
		return true
	})
	const text = "lost-in-the-crash"
	code, body := h.Send(peer, id, text)

	if n := read.Load(); n != 1 {
		t.Fatalf("the coordinator read %d forwarded requests, want 1", n)
	}
	if !coordinator.Crashed() {
		t.Fatal("the coordinator did not crash after it read the forward")
	}
	if code != http.StatusConflict {
		t.Fatalf("message on the serving replica = %d %v, want 409", code, body)
	}
	errBody, _ := body["error"].(map[string]any)
	if got, _ := errBody["code"].(string); got != "TARGET_NOT_READY" {
		t.Errorf("error code = %q, want TARGET_NOT_READY: %v", got, body)
	}
	details, _ := errBody["details"].(map[string]any)
	if got, _ := details["currentState"].(string); got != string(session.StateRunning) {
		t.Errorf("currentState = %q, want %q", got, session.StateRunning)
	}
	requireNothingHeld(t, h, id, text)

	// The surviving replica takes the session over; nothing was held, so
	// nothing is delivered later either.
	h.RunToQuiescence()
	requireNothingHeld(t, h, id, text)
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination) Fallback and
// Replica address, 29.3 (Interactive message send) Forwarding rules 1 and 2
// diagnosis: with Redis failing, the coordinating replica renewed the
// session's REG-COORDLEASE lease in the Postgres fallback, and a message sent
// to the other replica was answered 409 or 500, or delivered other than once:
// the fallback did not store the holder value with the replica address, or
// the serving replica resolved the forward target from somewhere other than
// the lease store it coordinates through. Check the holder value in
// pkg/gateway/storage/leasestore/pgstore and the target resolution in
// pkg/gateway/sessionserver/holder_forward.go.
func TestForwardResolvesTheCoordinatorThroughThePostgresLeaseFallback_spec_10_1_1(t *testing.T) {
	ctx := context.Background()
	newStore, pg := lifecycleharness.PostgresStore(t)
	pgLeases := leasepg.New(pg.Pool)
	var fallbacks atomic.Int64
	h := lifecycleharness.New(t, lifecycleharness.Options{
		Replicas: 2, Pods: 4, NewStore: newStore,
		Leases: func(primary leasestore.LeaseStore) leasestore.LeaseStore {
			return leasestore.NewFailover(primary, pgLeases, func() { fallbacks.Add(1) })
		},
	})
	id := h.StartSession(h.Replicas[0])

	h.Redis.SetError("chaos: redis unavailable")
	t.Cleanup(func() { h.Redis.SetError("") })

	var lease leasestore.Lease
	for pass := 0; ; pass++ {
		if pass == 6 {
			t.Fatalf("no replica holds the lease of %s in the Postgres fallback after %d passes", id, pass)
		}
		h.Pass()
		l, err := pgLeases.Get(ctx, lifecycleharness.Tenant, id)
		if err == nil {
			lease = l
			break
		}
	}
	var holder, serving *lifecycleharness.Replica
	for _, r := range h.Replicas {
		if r.ID == lease.Holder {
			holder = r
		} else {
			serving = r
		}
	}
	if holder == nil {
		t.Fatalf("the Postgres fallback holder value %q names no replica", lease.Holder)
	}
	if fallbacks.Load() == 0 {
		t.Fatal("no lease operation routed to the Postgres fallback while Redis failed")
	}
	if st := h.Row(id).State; st != session.StateRunning {
		t.Fatalf("session %s = %s after the Redis failure, want running", id, st)
	}

	var forwards atomic.Int64
	holder.SetForwardFault(func(*http.Request) bool {
		forwards.Add(1)
		return false
	})
	const text = "via-the-fallback"
	code, body := h.Send(serving, id, text)
	if code != http.StatusOK || forwardReceipt(body) != string(session.DeliveryStatusDelivered) {
		t.Fatalf("message on the serving replica = %d %v, want 200 with the coordinator's delivered receipt", code, body)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
	}
	h.Settle()
	if n := timesReceived(h, id, text); n != 1 {
		t.Errorf("the runtime received the message %d times, want once", n)
	}
}

// replicaDrainTerminator is the drain's forced terminal transition run on
// one replica, as the gateway's session-admin adapter runs it: it opens the
// replica's terminal-writer claim, writes failed with FORCE_TERMINATED under
// the row lock, and runs the replica's terminal hook for the transition it
// made.
type replicaDrainTerminator struct {
	h *lifecycleharness.Harness
	r *lifecycleharness.Replica
}

func (f replicaDrainTerminator) ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	ctx = context.WithoutCancel(ctx)
	defer f.r.Server.BeginTerminal(id)()
	var prev session.State
	transitioned := false
	row, err := f.h.Store.Update(ctx, tenantID, id, func(s *sessionstore.Session) error {
		prev = s.State
		if session.IsTerminal(s.State) {
			return nil
		}
		s.State, s.FailureClass, s.FailureReason = session.StateFailed, session.FailureClassRuntime, "FORCE_TERMINATED"
		sessionstore.MarkTerminated(s, prev, f.h.Clock.Now())
		transitioned = true
		return nil
	})
	if err != nil || !transitioned {
		return row, prev, false, err
	}
	f.r.Server.OnSessionTerminal(ctx, prev, row)
	return row, prev, true, nil
}

// localOnlyRelease is a binding-release fan-out whose publish reaches only
// the replica that ran the drain: it releases that replica's own bindings
// among the named sessions and nothing on any peer, as a CH-BINDRELEASE
// message the peer never received leaves it.
type localOnlyRelease struct{ r *lifecycleharness.Replica }

func (l localOnlyRelease) TerminateUserSessions(ctx context.Context, tenantID, _ string, ids []string) admin.UserTerminationResult {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var res admin.UserTerminationResult
	for _, b := range l.r.Registry.Snapshot() {
		if !want[b.SessionID] {
			continue
		}
		released, err := l.r.Server.ReleaseRevokedBinding(ctx, tenantID, b.SessionID)
		switch {
		case err != nil:
			res.FailedSessions = append(res.FailedSessions, b.SessionID)
		case released:
			res.PodsTerminated++
		}
	}
	return res
}

// lostFanOutErasure is one user-erasure job whose drain ran on replica a
// with a binding-release fan-out that never reached replica b.
type lostFanOutErasure struct {
	h       *lifecycleharness.Harness
	b       *lifecycleharness.Replica
	onB     string
	jobs    erasurejob.Store
	runner  *erasurejob.Runner
	jobID   string
	erasers *atomic.Int64
}

// startLostFanOutErasure starts one session on each of two replicas, starts
// the user's erasure job with the drain on replica a and a release gate of
// admin.ReleasePending on the harness clock, and runs the job until it waits
// in store_deleting.
func startLostFanOutErasure(t *testing.T) *lostFanOutErasure {
	t.Helper()
	ctx := context.Background()
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 4})
	a, b := h.Replicas[0], h.Replicas[1]
	h.StartSession(a)
	onB := h.StartSession(b)

	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{TenantID: lifecycleharness.Tenant, Subject: forwardChaosUser}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	jobs := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
	var erasers atomic.Int64
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "session-objects",
		DeleteByUser: func(context.Context, string, string) (int, error) {
			erasers.Add(1)
			return 0, nil
		},
	}}})
	runner := erasurejob.NewRunner(jobs, orch, h.Clock.Now).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(ctx context.Context, tenantID, userID string) error {
			_, err := admin.DrainSessions(ctx, h.Store, replicaDrainTerminator{h: h, r: a}, localOnlyRelease{r: a}, tenantID, userID)
			return err
		}).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithReleaseGate(func(ctx context.Context, tenantID, userID string) ([]string, error) {
			return admin.ReleasePending(ctx, h.Store, tenantID, userID, h.Clock.Now(), lifecycleharness.PostTerminalCap)
		}).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })
	job, err := runner.Start(ctx, lifecycleharness.Tenant, forwardChaosUser, "carol@acme.com", nil)
	if err != nil {
		t.Fatalf("start the erasure job: %v", err)
	}
	e := &lostFanOutErasure{h: h, b: b, onB: onB, jobs: jobs, runner: runner, jobID: job.ID, erasers: &erasers}
	for i := 0; i < 3; i++ {
		e.run(t)
	}
	if j := e.job(t); j.Phase != erasurejob.PhaseStoreDeleting {
		t.Fatalf("job phase after the drain = %s (%s), want store_deleting", j.Phase, j.Failure)
	}
	if n := erasers.Load(); n != 0 {
		t.Fatalf("the store deletions ran %d times while %s was in its post-terminal window, want none", n, onB)
	}
	if st := h.Row(onB).State; !session.IsTerminal(st) {
		t.Fatalf("session %s = %s after the drain, want terminal", onB, st)
	}
	if _, ok := b.Registry.Get(onB); !ok {
		t.Fatalf("replica b released %s, so the fan-out was not lost", onB)
	}
	return e
}

// run runs the job once and ignores the release gate's wait.
func (e *lostFanOutErasure) run(t *testing.T) {
	t.Helper()
	_ = e.runner.Run(context.Background(), e.jobID)
}

// job reads the job as persisted.
func (e *lostFanOutErasure) job(t *testing.T) erasurejob.Job {
	t.Helper()
	j, err := e.jobs.Get(context.Background(), erasurejob.AllTenants, e.jobID)
	if err != nil {
		t.Fatalf("read the erasure job: %v", err)
	}
	return j
}

// requireCompleted fails unless the job completed after running the store
// deletions once.
func (e *lostFanOutErasure) requireCompleted(t *testing.T) {
	t.Helper()
	e.run(t)
	if j := e.job(t); j.Phase != erasurejob.PhaseCompleted {
		t.Fatalf("job phase = %s (%s), want completed", j.Phase, j.Failure)
	}
	if n := e.erasers.Load(); n != 1 {
		t.Errorf("the store deletions ran %d times, want once", n)
	}
}

// spec: 28.5.7 (Gateway-to-store), 12.8 (Compliance Interfaces: Lifecycle
// fence and drain), 7.1 (Post-terminal window)
// diagnosis: the drain's CH-BINDRELEASE publish reached only the replica
// that ran the drain, so the session bound on the peer kept its binding, and
// the erasure job deleted the user's stores while that session was still in
// its post-terminal window, or never deleted them after the peer released
// the binding or the window cap passed. Check the store_deleting release gate
// in pkg/gateway/storage/erasurejob/runner_phases.go, admin.ReleasePending in
// pkg/gateway/externalapi/admin/drain.go, and the release marker the peer's
// terminal binding release records.
func TestLostBindReleaseFanOutHoldsErasureUntilReleaseOrWindowCap_spec_28_5_7(t *testing.T) {
	t.Run("the peer releases the binding", func(t *testing.T) {
		e := startLostFanOutErasure(t)
		terminatedAt := e.h.Row(e.onB).TerminatedAt
		for pass := 0; ; pass++ {
			if _, ok := e.b.Registry.Get(e.onB); !ok {
				break
			}
			if pass == 6 {
				t.Fatalf("replica b never released the binding of terminal session %s", e.onB)
			}
			e.h.Pass()
		}
		if elapsed := e.h.Clock.Now().Sub(terminatedAt); elapsed >= lifecycleharness.PostTerminalCap {
			t.Fatalf("the release came %v after the terminal write, not before the window cap %v", elapsed, lifecycleharness.PostTerminalCap)
		}
		if row := e.h.Row(e.onB); row.PodReleasedAt.IsZero() {
			t.Fatalf("replica b released %s without recording the release", e.onB)
		}
		e.requireCompleted(t)
	})

	t.Run("the window cap passes", func(t *testing.T) {
		e := startLostFanOutErasure(t)
		terminatedAt := e.h.Row(e.onB).TerminatedAt
		e.h.Clock.Advance(lifecycleharness.PostTerminalCap - e.h.Clock.Now().Sub(terminatedAt) - time.Second)
		e.run(t)
		if j := e.job(t); j.Phase != erasurejob.PhaseStoreDeleting || e.erasers.Load() != 0 {
			t.Fatalf("one second before the window cap the job is %s with %d store deletions, want store_deleting with none",
				j.Phase, e.erasers.Load())
		}
		e.h.Clock.Advance(2 * time.Second)
		if _, ok := e.b.Registry.Get(e.onB); !ok {
			t.Fatalf("replica b released %s, so the job would not be waiting on the window cap", e.onB)
		}
		e.requireCompleted(t)
	})
}
