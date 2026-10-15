// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// interruptSuspended interrupts a running session on r and fails the test
// unless it is suspended holding its pod.
func interruptSuspended(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
	t.Helper()
	if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
		t.Fatalf("interrupt = %d %v", code, body)
	}
	if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment == "" {
		t.Fatalf("after the interrupt the row is %q on %q, want suspended holding its pod", row.State, row.PodAssignment)
	}
	// The stepped clock moves on, so a later message is accepted after the
	// suspension, as on a wall clock.
	h.Clock.Advance(time.Second)
}

// receivedCount counts the messages with text the session's runtimes
// received.
func receivedCount(h *lifecycleharness.Harness, id, text string) int {
	n := 0
	for _, got := range h.Cluster.ReceivedTexts(id) {
		if got == text {
			n++
		}
	}
	return n
}

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: a message to a held-pod suspended session was lost, delivered
// twice, or delivered without the session leaving suspended: a failed
// running write did not leave the message in the shared DLQ for the
// coordinating replica's next suspended sweep, a DELETE did not drain it, or
// a message served by a replica that does not hold the session was not
// answered queued and delivered once after the holder's next tick.
func TestHeldPodSuspendedMessageWaitsInTheDLQ_spec_7_2(t *testing.T) {
	t.Run("running write fails then succeeds", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r := h.Replicas[0]
		id := h.StartSession(r)
		interruptSuspended(t, h, r, id)
		h.Trace.ArmMatching("Update", "resumeHeldPod", lifecycleharness.FaultStoreError, nil)
		code, body := h.Send(r, id, "held-pod")
		h.Trace.Start()
		if code >= http.StatusInternalServerError && code != http.StatusServiceUnavailable {
			t.Fatalf("message with the running write failing = %d %v", code, body)
		}
		if st := h.Row(id).State; st != session.StateSuspended {
			t.Fatalf("after the failed running write the session is %q, want suspended", st)
		}
		if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 1 {
			t.Fatalf("shared DLQ holds %d (%v), want the message", n, err)
		}
		if n := receivedCount(h, id, "held-pod"); n != 0 {
			t.Fatalf("the runtime received the message %d times before the resume", n)
		}
		if row := h.AwaitState(id, session.StateRunning, 3); row.State != session.StateRunning {
			t.Fatalf("the coordinator's suspended sweep left the session %q, want running", row.State)
		}
		h.Settle()
		if n := receivedCount(h, id, "held-pod"); n != 1 {
			t.Errorf("the runtime received the message %d times, want once", n)
		}
		if n, _ := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); n != 0 {
			t.Errorf("DLQ still holds %d entries after the delivery", n)
		}
	})

	t.Run("running write fails then DELETE", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r := h.Replicas[0]
		id := h.StartSession(r)
		interruptSuspended(t, h, r, id)
		h.Trace.ArmMatching("Update", "resumeHeldPod", lifecycleharness.FaultStoreError, nil)
		h.Send(r, id, "never-delivered")
		h.Trace.Start()
		if code, body := h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil); code >= http.StatusBadRequest {
			t.Fatalf("DELETE = %d %v", code, body)
		}
		h.Settle()
		if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
			t.Errorf("DLQ after the DELETE = %d (%v), want drained", n, err)
		}
		if n := receivedCount(h, id, "never-delivered"); n != 0 {
			t.Errorf("the runtime received the message of a deleted session %d times", n)
		}
	})

	t.Run("served by the non-holder", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		holder, peer := h.Replicas[0], h.Replicas[1]
		id := h.StartSession(holder)
		interruptSuspended(t, h, holder, id)
		code, body := h.Send(peer, id, "via-peer")
		if code != http.StatusOK || receiptStatus(body) != string(session.DeliveryStatusQueued) {
			t.Fatalf("message served by the non-holder = %d %v, want 200 queued", code, body)
		}
		if n := receivedCount(h, id, "via-peer"); n != 0 {
			t.Fatalf("the runtime received the message before the holder's tick")
		}
		h.Pass()
		h.Settle()
		if n := receivedCount(h, id, "via-peer"); n != 1 {
			t.Errorf("after the holder's tick the runtime received the message %d times, want once", n)
		}
		if st := h.Row(id).State; st != session.StateRunning {
			t.Errorf("after the holder's tick the session is %q, want running", st)
		}
	})
}

// spec: 9.2 (Elicitation Chain), 6.2 (Pod State Machine)
// diagnosis: an elicitation the runtime raised before an interrupt was still
// answerable after the hold release ended the pod, on REST or on MCP, or its
// answer reached the runtime the session resumed on. The answer would then
// reach a runtime that never asked.
func TestHoldReleaseExpiresTheRaisedElicitation_spec_9_2(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 8})
	r := h.Replicas[0]
	id := h.StartSession(r)
	ctx := context.Background()
	if err := h.Interactions.Put(ctx, interactionstore.Interaction{
		ID: "raised", Kind: interactionstore.KindElicitation, SessionID: id, TenantID: lifecycleharness.Tenant,
		UserID: "alice@acme.com", Phase: interactionstore.PhasePending,
		Detail: map[string]any{interactionstore.DetailOriginPod: id},
	}); err != nil {
		t.Fatalf("raise the elicitation: %v", err)
	}
	interruptSuspended(t, h, r, id)
	r.Server.ReleaseSuspendedPod(ctx, h.Row(id))
	h.Settle()
	if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment != "" {
		t.Fatalf("after the hold release the row is %q on %q, want podless suspended", row.State, row.PodAssignment)
	}
	if _, err := h.Interactions.Get(ctx, lifecycleharness.Tenant, id, "alice@acme.com", "raised"); !errors.Is(err, interactionstore.ErrNotFound) {
		t.Errorf("the raised elicitation after the hold release: err = %v, want it expired", err)
	}

	code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/elicitations/raised/respond", map[string]any{"response": map[string]any{"ok": true}})
	if code != http.StatusNotFound || restErrorCode(body) != "ELICITATION_NOT_FOUND" {
		t.Errorf("REST respond after the hold release = %d %v, want 404 ELICITATION_NOT_FOUND", code, body)
	}
	res := h.MCPCall(r, "lenny/respond_to_elicitation", fmt.Sprintf(`{"sessionId":%q,"elicitationId":"raised","response":{"ok":true}}`, id))
	if !strings.Contains(fmt.Sprint(res), "ELICITATION_NOT_FOUND") {
		t.Errorf("MCP respond after the hold release = %v, want ELICITATION_NOT_FOUND", res)
	}

	if code, body := h.Send(r, id, "after-release"); code != http.StatusOK {
		t.Fatalf("message to the podless session = %d %v", code, body)
	}
	row := h.AwaitState(id, session.StateRunning, recoveryPasses)
	if row.State != session.StateRunning {
		t.Fatalf("the session reached %q, want running", row.State)
	}
	if pod := h.Cluster.PodOf(row.PodAssignment); pod != nil {
		for _, f := range pod.Runtime.FrameTypes(id) {
			if strings.Contains(f, "elicitation") {
				t.Errorf("the resumed runtime received a %s frame", f)
			}
		}
	}
}
