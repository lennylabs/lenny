// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local interleaving coverage for the session lifecycle on the
// lifecycle harness. The randomized run issues lifecycle operations from
// concurrent goroutines on two replicas, with the operation choice and the
// start order taken from a seed, and runs the invariant checker after each
// run. The required pairs each race two operations through a rendezvous, in
// both start orders, and assert their own outcome before the checker runs.
//
// spec: §7.3 (Resume driver), §7.2 (Interactive Session Model), §6.2 (Pod
// State Machine), §10.1.5 (Stale Replica Behavior), §7.3 (Credential leases
// on the replacement pod)
package tier7a_load_local_test

import (
	"context"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// lifecycleSeed reads the run's seed from LENNY_LIFECYCLE_SEED, or derives
// one from the clock; a failing run logs it, and the same seed replays the
// run's operation choices and start orders.
func lifecycleSeed(t *testing.T) int64 {
	if s := os.Getenv("LENNY_LIFECYCLE_SEED"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			t.Fatalf("LENNY_LIFECYCLE_SEED: %v", err)
		}
		return v
	}
	return time.Now().UnixNano()
}

// race starts a and b together through a rendezvous; first selects which
// goroutine yields before it runs.
func race(first int, a, b func()) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	run := func(f func(), yield bool) {
		defer wg.Done()
		<-start
		if yield {
			runtime.Gosched()
			time.Sleep(time.Millisecond)
		}
		f()
	}
	wg.Add(2)
	go run(a, first == 1)
	go run(b, first == 0)
	close(start)
	wg.Wait()
}

func lifecycleReport(t *testing.T, r *lifecycleharness.Replica, id, reason string) {
	t.Helper()
	if _, err := r.Server.ReportSessionFailure(context.Background(), sessionserver.FailureReport{
		TenantID: lifecycleharness.Tenant, SessionID: id, Reason: reason,
	}); err != nil {
		t.Errorf("ReportSessionFailure: %v", err)
	}
}

// spec: §7.3 (Resume driver), §7.2 (Interactive Session Model), §6.2 (Pod
// State Machine)
// diagnosis: a failure means two concurrent operations on one session broke
// a lifecycle invariant: a pod or claim leaked, a lease survived the session,
// two replicas bound it, a queue outlived it, or a retry was spent twice.
func TestLifecycleRandomInterleavings_spec_7_3(t *testing.T) {
	seed := lifecycleSeed(t)
	t.Logf("seed %d (replay with LENNY_LIFECYCLE_SEED=%d)", seed, seed)
	rng := rand.New(rand.NewSource(seed))
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 80, CredentialPool: true})
	type op func(r *lifecycleharness.Replica, id string)
	ops := []op{
		func(r *lifecycleharness.Replica, id string) { h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil) },
		func(r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		},
		func(r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
		},
		func(r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil)
		},
		func(r *lifecycleharness.Replica, id string) {
			lifecycleReport(t, r, id, string(session.FailureRuntimeCrash))
		},
		func(r *lifecycleharness.Replica, id string) {
			r.Server.DriveResume(context.Background(), h.Row(id))
		},
		func(r *lifecycleharness.Replica, id string) {
			r.Server.ReleaseTerminalBinding(h.Row(id))
		},
	}
	for run := 0; run < 8; run++ {
		id := h.StartSession(h.Replicas[0])
		var wg sync.WaitGroup
		for g := 0; g < 3; g++ {
			o := ops[rng.Intn(len(ops))]
			r := h.Replicas[rng.Intn(len(h.Replicas))]
			delay := time.Duration(rng.Intn(3)) * time.Millisecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(delay)
				o(r, id)
			}()
		}
		wg.Wait()
	}
	h.Check(t)
}

// spec: §7.3 (Resume driver), §7.3 (Credential leases on the replacement
// pod), §7.2 (Interactive Session Model), §10.1.5 (Stale Replica Behavior)
// diagnosis: a failure means two drivers on one resume_pending row wrote two
// entries or two commits, or a terminal writer racing a driver left a claim.
func TestLifecycleRequiredPairs_spec_7_3(t *testing.T) {
	for _, first := range []int{0, 1} {
		t.Run("order "+strconv.Itoa(first), func(t *testing.T) {
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 24, CredentialPool: true})
			r1, r2 := h.Replicas[0], h.Replicas[1]

			t.Run("two drivers on one resume_pending row", func(t *testing.T) {
				id := h.StartSession(r1)
				lifecycleReport(t, r1, id, string(session.FailureRuntimeCrash))
				pending := h.Row(id)
				race(first,
					func() { r1.Server.DriveResume(context.Background(), pending) },
					func() { r1.Server.DriveResume(context.Background(), pending) })
				h.Settle()
				got := h.Row(id)
				if got.CoordinationGeneration > pending.CoordinationGeneration+1 || got.RecoveryGeneration > pending.RecoveryGeneration+1 {
					t.Errorf("generations advanced from %d/%d to %d/%d, want at most one entry and one commit",
						pending.CoordinationGeneration, pending.RecoveryGeneration, got.CoordinationGeneration, got.RecoveryGeneration)
				}
			})

			t.Run("POST /resume racing DELETE", func(t *testing.T) {
				id := h.StartSession(r1)
				for i := 0; i <= lifecycleharness.MaxRetries; i++ {
					lifecycleReport(t, r1, id, string(session.FailureRuntimeCrash))
					for p := 0; p < 10 && h.Row(id).State == session.StateResumePending; p++ {
						h.Pass()
					}
				}
				race(first,
					func() { h.Do(r2, http.MethodPost, "/v1/sessions/"+id+"/resume", nil) },
					func() { h.Do(r1, http.MethodDelete, "/v1/sessions/"+id, nil) })
				h.Settle()
				if st := h.Row(id).State; st != session.StateCancelled {
					t.Errorf("state = %q, want cancelled", st)
				}
			})

			t.Run("terminal writer racing the driver", func(t *testing.T) {
				id := h.StartSession(r1)
				lifecycleReport(t, r1, id, string(session.FailureRuntimeCrash))
				pending := h.Row(id)
				race(first,
					func() { r1.Server.DriveResume(context.Background(), pending) },
					func() { h.Do(r2, http.MethodDelete, "/v1/sessions/"+id, nil) })
				h.Settle()
				if st := h.Row(id).State; st != session.StateCancelled {
					t.Errorf("state = %q, want cancelled", st)
				}
			})

			t.Run("concurrent terminal releases", func(t *testing.T) {
				id := h.StartSession(r1)
				race(first,
					func() { h.Do(r1, http.MethodDelete, "/v1/sessions/"+id, nil) },
					func() {
						row := h.Row(id)
						row.State = session.StateCancelled
						r1.Server.ReleaseTerminalBinding(row)
						r1.Server.ReleaseTerminalBinding(row)
					})
				h.Settle()
				if _, ok := r1.Registry.Get(id); ok {
					t.Error("the binding survived the terminal releases")
				}
			})

			t.Run("report racing a same-replica resume", func(t *testing.T) {
				id := h.StartSession(r1)
				lifecycleReport(t, r1, id, string(session.FailureRuntimeCrash))
				pending := h.Row(id)
				race(first,
					func() { r1.Server.DriveResume(context.Background(), pending) },
					func() { lifecycleReport(t, r1, id, string(session.FailureRuntimeCrash)) })
				h.RunToQuiescence()
				row := h.Row(id)
				leases := h.CredLeases.LeasesBySession([]string{id})
				if row.State == session.StateRunning {
					bind, ok := r1.Registry.Get(id)
					if !ok {
						t.Fatal("a running session has no binding")
					}
					for _, l := range leases {
						if l.CoordinationGeneration > bind.CoordinationGeneration {
							t.Errorf("lease %s at generation %d is above the binding's %d", l.LeaseID, l.CoordinationGeneration, bind.CoordinationGeneration)
						}
					}
				}
			})

			h.Check(t)
		})
	}
}
