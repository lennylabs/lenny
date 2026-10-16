// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// suspendWithPendingInputWait interrupts a running session on r and leaves
// a lenny/request_input wait registered on the replica, as an interrupt of a
// runtime blocked in lenny/request_input does: the interrupt neither cancels
// nor forgets the session's waits.
func suspendWithPendingInputWait(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica) string {
	t.Helper()
	id := h.StartSession(r)
	if _, err := r.InputWaits.Register(id, "req_blocked", nil); err != nil {
		t.Fatalf("register the input wait: %v", err)
	}
	interruptSuspended(t, h, r, id)
	if len(r.InputWaits.PendingForSession(id)) == 0 {
		t.Fatalf("the interrupt cleared the input wait; the case no longer reproduces the interrupted request_input")
	}
	return id
}

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: a message to a suspended session whose runtime was interrupted
// while blocked in lenny/request_input was routed to the inbox (path 3)
// instead of resuming the session (path 6): the classification tested the
// pending input wait before the suspended state, so the message waited in
// the inbox, the session stayed suspended, and the runtime never received it.
func TestSuspendedWithPendingInputWaitResumesOnMessage_spec_7_2(t *testing.T) {
	t.Run("REST", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 8})
		r := h.Replicas[0]
		id := suspendWithPendingInputWait(t, h, r)
		if code, body := h.Send(r, id, "after-interrupt"); code != http.StatusOK {
			t.Fatalf("message to the suspended session = %d %v", code, body)
		}
		if n, err := h.Inbox.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
			t.Fatalf("inbox holds %d (%v), want nothing: path 6 never enqueues to the inbox", n, err)
		}
		h.Settle()
		if st := h.Row(id).State; st == session.StateSuspended {
			t.Fatalf("the session stayed %q after the message, want it resumed", st)
		}
		if n := receivedCount(h, id, "after-interrupt"); n != 1 {
			t.Errorf("the runtime received the message %d times, want once", n)
		}
	})

	t.Run("MCP", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 8})
		r := h.Replicas[0]
		id := suspendWithPendingInputWait(t, h, r)
		res := h.MCPCall(r, "lenny/send_message", fmt.Sprintf(`{"to":%q,"message":"after-interrupt"}`, id))
		if !strings.Contains(fmt.Sprint(res), string(session.DeliveryStatusQueued)) {
			t.Fatalf("lenny/send_message to the suspended session = %v, want a queued receipt", res)
		}
		if n, err := h.Inbox.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
			t.Fatalf("inbox holds %d (%v), want nothing: path 6 never enqueues to the inbox", n, err)
		}
		h.Pass()
		h.Settle()
		if st := h.Row(id).State; st == session.StateSuspended {
			t.Fatalf("the session stayed %q after the message, want it resumed", st)
		}
		if n := receivedCount(h, id, "after-interrupt"); n != 1 {
			t.Errorf("the runtime received the message %d times, want once", n)
		}
	})
}
