// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos scenario for the starting watchdog on a replica that holds no
// binding for the session. The replica serving a /start crashes after its
// launch and before its starting → running commit, which drops its pod
// registry. The surviving replica's watchdog fails the starting row with
// STARTING_TIMEOUT, and its post-terminal hook, holding no binding, reclaims
// the claimed pod by name, so the claim does not stay bound to a failed
// session until the orphan-claim GC.
//
// spec: §6.2 (starting watchdog), §7.2 (Session state machine: starting →
// failed), §4.6 (durable binding)
package tier8_chaos_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// spec: §6.2 (starting watchdog), §7.2 (Session state machine: starting →
// failed), §4.6 (durable binding)
// diagnosis: after the launching replica crashed mid-start, the surviving
// replica's starting watchdog did not fail the row with STARTING_TIMEOUT, or
// its post-terminal hook left the pod's claim in place: a starting row
// failed on a replica without the binding must take the by-name reclaim, so
// starting is missing from the pre-running claim states.
func TestStartingWatchdogOnAReplicaWithoutTheBindingReclaimsByName_spec_6_2(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 1})
	launcher, survivor := h.Replicas[0], h.Replicas[1]
	id := h.CreateSession(launcher)
	if code, body := h.Do(launcher, http.MethodPost, "/v1/sessions/"+id+"/finalize", nil); code != http.StatusOK {
		t.Fatalf("finalize = %d %v", code, body)
	}
	pod := h.Row(id).PodAssignment

	held, gate := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(gate) })
	defer release()
	h.Trace.ArmMatching("Update", "commitStart", lifecycleharness.FaultHold, func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) {
		close(held)
		<-gate
	})
	answer := make(chan int, 1)
	go func() {
		code, _ := h.Do(launcher, http.MethodPost, "/v1/sessions/"+id+"/start", nil)
		answer <- code
	}()
	select {
	case <-held:
	case code := <-answer:
		t.Fatalf("start answered %d before its commit", code)
	}
	h.Crash(launcher)
	if _, ok := survivor.Registry.Get(id); ok {
		t.Fatal("the surviving replica binds the session before the watchdog runs")
	}

	h.Clock.Advance(time.Duration(watchdog.DefaultMaxStartingStateSeconds+1) * time.Second)
	if _, err := survivor.Watchdog.Tick(context.Background(), h.Clock.Now()); err != nil {
		t.Logf("watchdog tick: %v", err)
	}
	row := h.Row(id)
	if row.State != session.StateFailed || row.FailureReason != watchdog.ReasonStartingTimeout {
		t.Fatalf("session = %s (%s), want failed (STARTING_TIMEOUT)", row.State, row.FailureReason)
	}
	h.Settle()
	if _, exists, err := h.Cluster.ClaimPhase(context.Background(), pod); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Errorf("claim of %s still present after the starting watchdog failed the session, want the by-name reclaim to delete it", pod)
	}

	release()
	if code := <-answer; code != http.StatusConflict {
		t.Errorf("the crashed replica's held start answered %d, want 409", code)
	}
	if st := h.Row(id).State; st != session.StateFailed {
		t.Errorf("session after the held start returned = %s, want failed kept", st)
	}
}
