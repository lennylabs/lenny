// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for POST /v1/sessions/{id}/start moving a
// session through starting. The start writes ready → starting before its
// launch, so the ready watchdog cannot fail a launch that outlasts the
// ready window; the starting watchdog bounds the launch instead and, when it
// fires while the launch is held, its terminal write stands and the start
// answers 409 with failed. A launch that fails inside the binder fails the
// session. Each case settles the harness and requires that the session holds
// no binding, claim, or lease, except the running session, whose binding and
// lease stay.
//
// spec: §6.2 (starting watchdog), §7.2 (Session state machine: ready →
// starting, starting → failed), §15.1 (/start)
package tier4_integration_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// heldLaunch is a /start whose starting → running commit is held after the
// launch.
type heldLaunch struct {
	release func()
	answer  chan startReply
}

// startReply is a /start answer's status and body.
type startReply struct {
	code int
	body map[string]any
}

// finalizeOn finalizes a created session on r.
func finalizeOn(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
	t.Helper()
	if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/finalize", nil); code != http.StatusOK {
		t.Fatalf("finalize %s = %d %v", id, code, body)
	}
}

// startHeldAtCommit starts id on r with its commit held, and returns once
// the launch ran and the row is starting.
func startHeldAtCommit(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) *heldLaunch {
	t.Helper()
	held, gate := make(chan struct{}), make(chan struct{})
	h.Trace.ArmMatching("Update", "commitStart", lifecycleharness.FaultHold, func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
		close(held)
		<-gate
	})
	hl := &heldLaunch{release: sync.OnceFunc(func() { close(gate) }), answer: make(chan startReply, 1)}
	go func() {
		code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
		hl.answer <- startReply{code: code, body: body}
	}()
	select {
	case <-held:
	case a := <-hl.answer:
		t.Fatalf("start of %s answered %d before its commit", id, a.code)
	}
	if st := h.Row(id).State; st != session.StateStarting {
		t.Fatalf("held start's session = %s, want starting", st)
	}
	return hl
}

// tickWatchdog runs one watchdog pass on r at the harness clock.
func tickWatchdog(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) {
	t.Helper()
	if _, err := r.Watchdog.Tick(context.Background(), h.Clock.Now()); err != nil {
		t.Logf("watchdog tick: %v", err)
	}
}

// requireReleased requires that the session holds no binding on any replica,
// its pod's claim is not bound, and no replica holds its lease.
func requireReleased(t *testing.T, h *lifecycleharness.Harness, id, pod string) {
	t.Helper()
	h.Settle()
	for _, r := range h.Replicas {
		if _, ok := r.Registry.Get(id); ok {
			t.Errorf("%s still binds %s", r.ID, id)
		}
	}
	phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if exists && phase == "bound" {
		t.Errorf("claim of %s is still bound after the session failed", pod)
	}
	if l, err := h.Leases.Get(context.Background(), lifecycleharness.Tenant, id); err == nil && l.Holder != "" {
		t.Errorf("lease of %s still held by %s", id, l.Holder)
	}
}

func replyState(b map[string]any) string {
	e, _ := b["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	s, _ := d["currentState"].(string)
	return s
}

// spec: §6.2 (starting watchdog), §7.2 (Session state machine: ready →
// starting), §15.1 (/start)
// diagnosis: a /start whose launch began late in the ready window was failed
// by the ready watchdog (READY_TIMEOUT) while it launched, or the session did
// not end running. The start must write starting before the launch, so the
// ready watchdog no longer lists the row and the starting bound runs from
// the starting write.
func TestStartLaunchOutlastingTheReadyWindowEndsRunning_spec_6_2(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: 1})
	r := h.Replicas[0]
	id := h.CreateSession(r)
	finalizeOn(t, h, r, id)
	ready := time.Duration(watchdog.DefaultMaxReadyStateSeconds) * time.Second
	starting := time.Duration(watchdog.DefaultMaxStartingStateSeconds) * time.Second
	h.Clock.Advance(ready - 10*time.Second)
	hl := startHeldAtCommit(t, h, r, id)
	// The ready window has elapsed since the finalize, and the starting
	// window has not since the starting write.
	h.Clock.Advance(starting / 2)
	tickWatchdog(t, h, r)
	if st := h.Row(id).State; st != session.StateStarting {
		t.Fatalf("session after the watchdog pass = %s, want starting", st)
	}
	hl.release()
	if a := <-hl.answer; a.code != http.StatusOK {
		t.Fatalf("start = %d %v, want 200", a.code, a.body)
	}
	h.Settle()
	if st := h.Row(id).State; st != session.StateRunning {
		t.Fatalf("session = %s, want running", st)
	}
	if _, ok := r.Registry.Get(id); !ok {
		t.Error("the running session holds no binding")
	}
	h.Check(t)
}

// spec: §6.2 (starting watchdog), §7.2 (Session state machine: starting →
// failed), §15.1 (/start)
// diagnosis: the starting watchdog fired while a /start was held after its
// launch, and the start overwrote the watchdog's failed row with running,
// answered other than 409 failed, or left the pod's binding, claim, or lease
// behind. The commit must re-check starting on the locked row, and the
// terminal binding release must release the binding the launch published.
func TestStartingWatchdogFiresWhileTheLaunchIsHeld_spec_6_2(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts lifecycleharness.Options
	}{
		{"exclusive pool", lifecycleharness.Options{Replicas: 1, Pods: 1}},
		{"concurrent-workspace pool", lifecycleharness.Options{Replicas: 1, Pods: 1, MaxConcurrentSessions: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := lifecycleharness.New(t, tc.opts)
			r := h.Replicas[0]
			id := h.CreateSession(r)
			finalizeOn(t, h, r, id)
			pod := h.Row(id).PodAssignment
			hl := startHeldAtCommit(t, h, r, id)
			h.Clock.Advance(time.Duration(watchdog.DefaultMaxStartingStateSeconds+1) * time.Second)
			tickWatchdog(t, h, r)
			row := h.Row(id)
			if row.State != session.StateFailed || row.FailureReason != watchdog.ReasonStartingTimeout {
				t.Fatalf("session = %s (%s), want failed (STARTING_TIMEOUT)", row.State, row.FailureReason)
			}
			hl.release()
			a := <-hl.answer
			if a.code != http.StatusConflict || replyState(a.body) != string(session.StateFailed) {
				t.Fatalf("start = %d %v, want 409 with failed", a.code, a.body)
			}
			if got := h.Row(id); got.State != session.StateFailed || got.FailureReason != watchdog.ReasonStartingTimeout {
				t.Errorf("session after the start = %s (%s), want failed (STARTING_TIMEOUT) kept", got.State, got.FailureReason)
			}
			requireReleased(t, h, id, pod)
			h.Check(t)
		})
	}
}

// spec: §7.2 (Session state machine: starting → failed), §15.1 (/start)
// diagnosis: a /start whose launch failed inside the binder left the
// session ready (so a retry would reconnect to a pod the binder already
// released), answered other than 503 STARTING_FAILED, or left a binding,
// claim, or lease behind. Every exit after the starting write must fail the
// session with STARTING_FAILED and release the lease.
func TestStartLaunchFailureInsideTheBinderFailsTheSession_spec_7_2(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: 1})
	r := h.Replicas[0]
	id := h.CreateSession(r)
	finalizeOn(t, h, r, id)
	pod := h.Row(id).PodAssignment
	h.Cluster.PodOf(pod).Runtime.FailNextStart(errors.New("runtime refused to start"))
	code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
	if code < 400 {
		t.Fatalf("start = %d %v, want a failure", code, body)
	}
	row := h.Row(id)
	if row.State != session.StateFailed || row.FailureReason != "STARTING_FAILED" {
		t.Fatalf("session = %s (%s), want failed (STARTING_FAILED)", row.State, row.FailureReason)
	}
	again, againBody := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
	if again != http.StatusConflict || replyState(againBody) != string(session.StateFailed) {
		t.Errorf("repeated start = %d %v, want 409 with failed", again, againBody)
	}
	requireReleased(t, h, id, pod)
	h.Check(t)
}
