// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// spec: 29.3 (Interactive message send) Forwarding rules 5 and 6, 28.5.4 (Inter-replica, CH-HOLDERFORWARD)
// diagnosis: a forwarded request reached a replica that does not hold the
// session's REG-COORDLEASE after the row left the request's forward states
// between the serving replica's read and the listener's re-read, and the
// listener applied it locally, or held a message in the DLQ for a session
// that is no longer running, rather than refusing it with 409
// TARGET_NOT_READY carrying the re-read state. Check that forwardTarget in
// pkg/gateway/sessionserver/holder_forward.go applies the rule-5 branch
// ahead of forwardGated, and that HoldsUndelivered gates the DLQ hold.
func TestForwardedRequestWhoseRowLeftTheForwardStatesIsRefusedAtANonHolder_spec_29_3(t *testing.T) {
	cases := []struct {
		name string
		send func(h *lifecycleharness.Harness, serving *lifecycleharness.Replica, id string) (int, map[string]any)
		// precondition marks a route whose §15.1 precondition check runs
		// before its forward decision, so the re-read suspended row is
		// refused as INVALID_STATE_TRANSITION ahead of rule 5 (F-29.3.5).
		precondition bool
	}{
		{name: "message without inReplyTo", send: func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) (int, map[string]any) {
			return sendBatch(h, r, id, userMessage("reread", ""))
		}},
		{name: "interrupt", precondition: true, send: func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) (int, map[string]any) {
			return h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil)
		}},
		{name: "terminate", send: func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) (int, map[string]any) {
			return h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			leases := &staleLeases{}
			h, former, current := newForwardCompose(t, lifecycleharness.Options{
				Leases: func(inner leasestore.LeaseStore) leasestore.LeaseStore {
					leases.LeaseStore = inner
					return leases
				},
			})
			id := h.StartSession(former)
			pod := h.Row(id).PodAssignment
			h.TakeOver(id, former)
			// The row leaves the forward states after the serving replica
			// read it as running and before the listener re-reads it. A
			// suspended row without a pod is outside every kind's forward
			// states, so a state gate applied first would run the request
			// locally at the former holder.
			var forwards atomic.Int64
			var moveErr atomic.Pointer[error]
			former.SetForwardFault(func(*http.Request) bool {
				if forwards.Add(1) == 1 {
					// The hook runs on the listener's goroutine, so it
					// records a failure rather than failing the test.
					if _, err := h.Store.Update(context.Background(), lifecycleharness.Tenant, id, func(r *sessionstore.Session) error {
						r.State = session.StateSuspended
						r.PodAssignment = ""
						return nil
					}); err != nil {
						moveErr.Store(&err)
					}
				}
				return false
			})
			t.Cleanup(func() { former.SetForwardFault(nil) })
			shutdowns := h.Cluster.PodOf(pod).UnaryCalls("Shutdown")
			interrupts := h.Cluster.PodOf(pod).UnaryCalls("Interrupt")
			holder := former.ID
			leases.stale.Store(&holder)

			code, body := tc.send(h, current, id)

			if err := moveErr.Load(); err != nil {
				t.Fatalf("move the row to suspended: %v", *err)
			}
			if n := forwards.Load(); n != 1 {
				t.Fatalf("the former holder's listener received %d forwards, want the one stale forward", n)
			}
			if tc.precondition {
				requirePreconditionRefusal(t, code, body, session.StateSuspended)
			} else {
				requireForwardRefusal(t, code, body, session.StateSuspended)
			}
			after := h.Row(id)
			if after.State != session.StateSuspended || !after.TerminatedAt.IsZero() {
				t.Errorf("row = %q (terminated %v), want suspended unwritten", after.State, after.TerminatedAt)
			}
			requireEmptyDLQ(t, h, id)
			if n := receivedCount(h, id, "reread"); n != 0 {
				t.Errorf("the runtime received the message %d times, want none", n)
			}
			if n := h.Cluster.PodOf(pod).UnaryCalls("Shutdown") - shutdowns; n != 0 {
				t.Errorf("the pod received %d Shutdowns, want none", n)
			}
			if n := h.Cluster.PodOf(pod).UnaryCalls("Interrupt") - interrupts; n != 0 {
				t.Errorf("the pod received %d Interrupts, want none", n)
			}
		})
	}
}

// requirePreconditionRefusal fails unless the answer is 409
// INVALID_STATE_TRANSITION carrying the session's current state.
func requirePreconditionRefusal(t *testing.T, code int, body map[string]any, state session.State) {
	t.Helper()
	if code != http.StatusConflict || restErrorCode(body) != "INVALID_STATE_TRANSITION" {
		t.Fatalf("answer = %d %v, want 409 INVALID_STATE_TRANSITION", code, body)
	}
	details, _ := body["error"].(map[string]any)["details"].(map[string]any)
	if got, _ := details["currentState"].(string); got != string(state) {
		t.Errorf("currentState = %q, want %q", got, state)
	}
}
