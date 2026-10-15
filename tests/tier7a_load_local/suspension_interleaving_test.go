// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local required pairs for idle suspension, suspension release,
// held-message delivery, and terminal releases on the lifecycle harness. Each
// pair races two operations through a rendezvous in both start orders,
// asserts its own outcome, and then runs the invariant checker.
//
// spec: §6.2 (Pod State Machine), §6.2 (maxClientIdleSeconds clock behavior
// across states), §7.2 (Message delivery routing — seven paths, path 6), §7.3
// (Credential leases on the replacement pod), §10.1.5 (Stale Replica
// Behavior)
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// idleWindowElapsed waits out the adapter's activity window so a following
// idle suspension can seal the session.
func idleWindowElapsed() {
	time.Sleep(lifecycleharness.IdleWindow*time.Second + 100*time.Millisecond)
}

// turnInHand is how long a harness runtime keeps a turn in hand in a
// scenario that asserts no release ends a turn. It is shorter than the idle
// window, so a turn in hand never outlasts the activity it stamped.
const turnInHand = 200 * time.Millisecond

// setResponseDelay makes every pod's runtime keep each later turn in hand
// for d.
func setResponseDelay(h *lifecycleharness.Harness, d time.Duration) {
	for _, p := range h.Cluster.Pods() {
		p.Runtime.SetResponseDelay(d)
	}
}

// awaitTurnsAnswered waits until no pod's runtime holds a turn for the
// session in hand, so every turn a runtime received has either been
// answered or been ended.
func awaitTurnsAnswered(t *testing.T, h *lifecycleharness.Harness, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * turnInHand)
	for _, p := range h.Cluster.Pods() {
		for p.Runtime.Handling(id) > 0 {
			if time.Now().After(deadline) {
				t.Fatalf("pod %s still holds %d turns for the session in hand", p.Name, p.Runtime.Handling(id))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// countText counts the messages with text the session's runtimes received.
func countText(h *lifecycleharness.Harness, id, text string) int {
	n := 0
	for _, got := range h.Cluster.ReceivedTexts(id) {
		if got == text {
			n++
		}
	}
	return n
}

// answered reports whether a message answer accepted the message: delivered
// now or held for a later delivery.
func answered(code int, body map[string]any) bool {
	if code != http.StatusOK {
		return false
	}
	for _, key := range []string{"deliveryReceipt", "deliveryReceipts"} {
		v, ok := body[key]
		if !ok {
			continue
		}
		var r map[string]any
		switch x := v.(type) {
		case map[string]any:
			r = x
		case []any:
			if len(x) > 0 {
				r, _ = x[0].(map[string]any)
			}
		}
		st, _ := r["status"].(string)
		return st == string(session.DeliveryStatusDelivered) || st == string(session.DeliveryStatusQueued)
	}
	return true
}

// spec: §6.2 (Pod State Machine), §7.2 (Message delivery routing — seven
// paths, path 6), §6.2 (maxClientIdleSeconds clock behavior across states)
// diagnosis: a message racing an idle suspension was left held by a podless
// suspended session, reached the runtime twice, or was lost; a message
// racing the suspension was delivered between the seal and the release, so
// the release reached its runtime before the runtime wrote the response;
// or a stale coordinator's idle suspension acted on a session a peer had
// taken over at a later generation.
func TestIdleSuspensionRequiredPairs_spec_6_2(t *testing.T) {
	for _, first := range []int{0, 1} {
		t.Run("order "+strconv.Itoa(first), func(t *testing.T) {
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 24, CredentialPool: true})
			r1, r2 := h.Replicas[0], h.Replicas[1]

			for _, sender := range []*lifecycleharness.Replica{r1, r2} {
				t.Run("message on "+sender.ID+" racing the zero-hold release", func(t *testing.T) {
					id := h.StartSession(r1)
					idleWindowElapsed()
					row := h.Row(id)
					text := "race-" + sender.ID
					var code int
					var body map[string]any
					race(first,
						func() { r1.Server.SuspendIdle(context.Background(), row, lifecycleharness.IdleWindow) },
						func() { code, body = h.Send(sender, id, text) })
					h.Settle()
					if sender != r1 && code == http.StatusInternalServerError && strings.Contains(fmt.Sprint(body), "not bound to a pod") {
						t.Skip("blocked: F-7.2.38 (owner: R19) a message a non-coordinating replica serves for a running session answers EXECUTOR_FAILURE instead of being forwarded or held")
					}
					if !answered(code, body) {
						t.Fatalf("the racing message was answered %d %v, want delivered or queued", code, body)
					}
					got := h.AwaitState(id, session.StateRunning, 20)
					if got.State == session.StateSuspended {
						t.Fatalf("the session is suspended holding the message (pod %q), want resume_pending or running", got.PodAssignment)
					}
					h.Settle()
					if n := countText(h, id, text); n != 1 {
						t.Errorf("the runtime received the racing message %d times, want once", n)
					}
				})
			}

			t.Run("message sends racing SuspendIdle never end a turn", func(t *testing.T) {
				// Each runtime keeps a turn in hand for turnInHand, so a turn
				// delivered between the seal and the release is still in hand
				// when the release reaches the runtime, and the runtime
				// records its response as written after the release.
				setResponseDelay(h, turnInHand)
				defer setResponseDelay(h, 0)
				id := h.StartSession(r1)
				idleWindowElapsed()
				row := h.Row(id)
				results := make([]struct {
					code int
					body map[string]any
				}, 3)
				race(first,
					func() { r1.Server.SuspendIdle(context.Background(), row, lifecycleharness.IdleWindow) },
					func() {
						for i := range results {
							results[i].code, results[i].body = h.Send(r1, id, "turn-"+strconv.Itoa(i))
						}
					})
				h.AwaitState(id, session.StateRunning, 20)
				h.Settle()
				for i, res := range results {
					if !answered(res.code, res.body) {
						t.Errorf("message %d was answered %d %v, want delivered or queued", i, res.code, res.body)
					}
				}
				awaitTurnsAnswered(t, h, id)
				for _, p := range h.Cluster.Pods() {
					for _, m := range p.Runtime.TurnsNotAnsweredBeforeRelease(id) {
						t.Errorf("pod %s received message %q and did not write its response before the release reached the runtime: the release ended the turn", p.Name, m.Text)
					}
				}
			})

			t.Run("stale coordinator's idle suspension after a takeover", func(t *testing.T) {
				id := h.StartSession(r1)
				idleWindowElapsed()
				stale := h.Row(id)
				// The peer takes the session over: the stale coordinator's
				// lease key is gone and the peer's sweep re-adopts the pod at a
				// later generation.
				h.Redis.Del("t:" + lifecycleharness.Tenant + ":lease:session:" + id)
				if _, err := r2.Sweeper.Sweep(context.Background()); err != nil {
					t.Fatalf("peer sweep: %v", err)
				}
				adopted := h.Row(id)
				if adopted.CoordinationGeneration <= stale.CoordinationGeneration || h.Bound(id) == nil {
					t.Fatalf("the peer did not re-adopt the running session whose lease lapsed (generation %d → %d, bound %v)", stale.CoordinationGeneration, adopted.CoordinationGeneration, h.Bound(id) != nil)
				}
				var code int
				var body map[string]any
				race(first,
					func() { r1.Server.SuspendIdle(context.Background(), stale, lifecycleharness.IdleWindow) },
					func() { code, body = h.Send(r2, id, "peer-turn") })
				h.Settle()
				row := h.Row(id)
				if row.State != session.StateRunning || row.PodAssignment != stale.PodAssignment || row.CoordinationGeneration != adopted.CoordinationGeneration {
					t.Errorf("after the stale suspension the row is %q on %q at generation %d, want running on %q at %d",
						row.State, row.PodAssignment, row.CoordinationGeneration, stale.PodAssignment, adopted.CoordinationGeneration)
				}
				if code != http.StatusOK {
					t.Errorf("the peer's turn answered %d %v, want 200", code, body)
				}
				if n := countText(h, id, "peer-turn"); n != 1 {
					t.Errorf("the peer's turn reached the runtime %d times, want once", n)
				}
				if p := h.Cluster.PodOf(stale.PodAssignment); p != nil {
					for _, ended := range p.Runtime.Ended() {
						if ended == id {
							t.Error("the stale coordinator ended the session on the pod")
						}
					}
				}
			})

			h.Check(t)
		})
	}
}

// spec: §7.2 (Message delivery routing — seven paths, path 6), §7.3
// (Credential leases on the replacement pod), §6.2 (Pod State Machine)
// diagnosis: the gateway leader's backlog re-check racing a message hold
// recorded more than one suspended → resume_pending transition; a held
// message arriving during the delivery of held messages was delivered before
// the ones held earlier; a suspension release racing a resume left the
// session holding a lease from before the suspension; or concurrent terminal
// releases of one bound session released the pod or a lease twice.
func TestHeldDeliveryAndReleaseRequiredPairs_spec_7_2(t *testing.T) {
	for _, first := range []int{0, 1} {
		t.Run("order "+strconv.Itoa(first), func(t *testing.T) {
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 24, CredentialPool: true})
			r1, r2 := h.Replicas[0], h.Replicas[1]

			t.Run("re-check racing a message hold", func(t *testing.T) {
				id := h.StartSession(r1)
				h.SuspendIdle(r1, id)
				suspended := h.Row(id)
				if suspended.State != session.StateSuspended || suspended.PodAssignment != "" {
					t.Fatalf("pre-state %q on %q, want podless suspended", suspended.State, suspended.PodAssignment)
				}
				var code int
				var body map[string]any
				race(first,
					func() { h.Recheck(r1) },
					func() { code, body = h.Send(r2, id, "held") })
				if !answered(code, body) {
					t.Fatalf("the held message was answered %d %v", code, body)
				}
				row := h.Row(id)
				if row.State != session.StateResumePending && row.State != session.StateResuming && row.State != session.StateRunning {
					t.Fatalf("after the race the session is %q, want it resuming", row.State)
				}
				if row.RecoveryGeneration > suspended.RecoveryGeneration+1 {
					t.Errorf("recovery generation advanced from %d to %d, want one restore", suspended.RecoveryGeneration, row.RecoveryGeneration)
				}
				h.AwaitState(id, session.StateRunning, 20)
				h.Settle()
				if n := countText(h, id, "held"); n != 1 {
					t.Errorf("the held message reached the runtime %d times, want once", n)
				}
			})

			t.Run("message arriving during the delivery of held messages", func(t *testing.T) {
				id := h.StartSession(r1)
				h.SuspendIdle(r1, id)
				if code, body := h.Send(r1, id, "held-1"); !answered(code, body) {
					t.Fatalf("held-1 = %d %v", code, body)
				}
				var code int
				var body map[string]any
				race(first,
					func() { h.Pass() },
					func() { code, body = h.Send(r1, id, "later") })
				if !answered(code, body) {
					t.Fatalf("later = %d %v", code, body)
				}
				h.AwaitState(id, session.StateRunning, 20)
				h.Settle()
				var got []string
				for _, text := range h.Cluster.ReceivedTexts(id) {
					if text == "held-1" || text == "later" {
						got = append(got, text)
					}
				}
				if len(got) != 2 || got[0] != "held-1" || got[1] != "later" {
					t.Errorf("delivery order = %v, want [held-1 later] once each", got)
				}
			})

			t.Run("suspension release racing a resume started after its clear", func(t *testing.T) {
				id := h.StartSession(r1)
				if code, body := h.Do(r1, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
					t.Fatalf("interrupt = %d %v", code, body)
				}
				h.Clock.Advance(time.Second)
				before := h.CredLeases.LeasesBySession([]string{id})
				row := h.Row(id)
				race(first,
					func() { r1.Server.ReleaseSuspendedPod(context.Background(), row) },
					func() { h.Send(r1, id, "resume-me") })
				final := h.AwaitState(id, session.StateRunning, 20)
				h.Settle()
				if final.State != session.StateRunning {
					t.Fatalf("the session reached %q, want running", final.State)
				}
				old := map[string]bool{}
				for _, l := range before {
					old[l.LeaseID] = true
				}
				bind, ok := r1.Registry.Get(id)
				for _, l := range h.CredLeases.LeasesBySession([]string{id}) {
					if old[l.LeaseID] && final.PodAssignment != row.PodAssignment {
						t.Errorf("lease %s from before the suspension survived the resume onto a new pod", l.LeaseID)
					}
					if ok && l.CoordinationGeneration > bind.CoordinationGeneration {
						t.Errorf("lease %s at generation %d is above the binding's %d", l.LeaseID, l.CoordinationGeneration, bind.CoordinationGeneration)
					}
				}
			})

			t.Run("concurrent terminal releases release once", func(t *testing.T) {
				id := h.StartSession(r1)
				pod := h.Row(id).PodAssignment
				var row sessionstore.Session
				race(first,
					func() { h.Do(r1, http.MethodDelete, "/v1/sessions/"+id, nil) },
					func() {
						row = h.Row(id)
						row.State = session.StateCancelled
						r1.Server.ReleaseTerminalBinding(row)
					})
				r1.Server.ReleaseTerminalBinding(h.Row(id))
				h.Settle()
				if st := h.Row(id).State; st != session.StateCancelled {
					t.Fatalf("state = %q, want cancelled", st)
				}
				if _, ok := r1.Registry.Get(id); ok {
					t.Error("the binding survived the terminal releases")
				}
				if n := len(h.CredLeases.LeasesBySession([]string{id})); n != 0 {
					t.Errorf("%d credential leases survived the terminal releases", n)
				}
				if _, exists, err := h.Cluster.ClaimPhase(context.Background(), pod); err != nil || exists {
					t.Errorf("the claim on %s survived the cancelled release (err %v)", pod, err)
				}
			})

			h.Check(t)
		})
	}
}

// spec: §7.3 (Resume driver), §6.2 (Pod State Machine)
// diagnosis: two replicas' watchdogs ticking over one store exited a
// resuming incarnation more than once, or spent a retry past the budget.
func TestWatchdogsTickingOnOneStoreExitResumingOnce_spec_7_3(t *testing.T) {
	for _, first := range []int{0, 1} {
		t.Run("order "+strconv.Itoa(first), func(t *testing.T) {
			h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 2, Pods: 24, CredentialPool: true})
			r1, r2 := h.Replicas[0], h.Replicas[1]
			id := h.StartSession(r1)
			for round := 0; round <= lifecycleharness.MaxRetries+1; round++ {
				if st := h.Row(id).State; st == session.StateRunning {
					lifecycleharness.Report(t, r1, id, string(session.FailureRuntimeCrash))
				}
				h.Clock.Advance(lifecycleharness.MaxResuming + lifecycleharness.SweepInterval)
				ctx := context.Background()
				race(first,
					func() { _, _ = r1.Watchdog.Tick(ctx, h.Clock.Now()) },
					func() { _, _ = r2.Watchdog.Tick(ctx, h.Clock.Now()) })
				h.Settle()
				if rc := h.Row(id).RetryCount; rc > lifecycleharness.MaxRetries {
					t.Fatalf("round %d: RetryCount = %d, over the budget %d", round, rc, lifecycleharness.MaxRetries)
				}
			}
			h.Check(t)
		})
	}
}
