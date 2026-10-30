// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// newComposeWith builds a harness over the compose Postgres session store
// with the given options.
func newComposeWith(t *testing.T, opts lifecycleharness.Options) *lifecycleharness.Harness {
	t.Helper()
	opts.NewStore, _ = lifecycleharness.SharedPostgresStore(t, &harnessPostgres)
	opts.CredentialPool = true
	opts.ControlPlane = &harnessControlPlane
	return lifecycleharness.New(t, opts)
}

// receiptStatus reads the delivery receipt status of a message answer.
func receiptStatus(body map[string]any) string {
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

// idleSuspended suspends a running session for idleness on r and fails
// the test unless the row is a podless Idle suspension.
func idleSuspended(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) sessionstore.Session {
	t.Helper()
	h.SuspendIdle(r, id)
	row := h.Row(id)
	if row.State != session.StateSuspended || row.PodAssignment != "" || row.SuspendedReason != sessionstore.SuspendedReasonIdle {
		t.Fatalf("after the idle suspension the row is %q on %q (reason %q), want a podless Idle suspension",
			row.State, row.PodAssignment, row.SuspendedReason)
	}
	return row
}

// requireOrder fails unless the session's runtimes received each of want,
// once, in that order.
func requireOrder(t *testing.T, h *lifecycleharness.Harness, id string, want ...string) {
	t.Helper()
	var got []string
	for _, text := range h.Cluster.ReceivedTexts(id) {
		for _, w := range want {
			if text == w {
				got = append(got, text)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the runtime received %v of %v, want each once in that order (all received: %v)", got, want, h.Cluster.ReceivedTexts(id))
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 29.3
// (Interactive message send)
// diagnosis: a podless suspended session still depended on a coordinating
// replica: its coordination lease survived the idle suspension, the event
// list differed between replicas, or a terminate served by another replica
// acquired a lease, failed, or left the session's DLQ entries behind.
func TestPodlessSuspendedSessionNeedsNoCoordinator_spec_10_1_1(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8, Events: true})
	r1, r2 := h.Replicas[0], h.Replicas[1]
	id := h.StartSession(r1)
	if code, body := h.Send(r1, id, "before-suspension"); code != http.StatusOK {
		t.Fatalf("message = %d %v", code, body)
	}
	idleSuspended(t, h, r1, id)
	// The coordinator stops renewing the lease of a podless suspended
	// session, which lapses on its expiry.
	for passed := time.Duration(0); passed <= lifecycleharness.LeaseTTL; passed += lifecycleharness.SweepInterval {
		h.Pass()
	}
	if got := leaseHolder(t, h, id); got != "" {
		t.Errorf("the podless suspended session's lease is held by %q, want no lease", got)
	}

	code1, items1 := h.Events(r1, id)
	code2, items2 := h.Events(r2, id)
	if code1 != http.StatusOK || code2 != http.StatusOK || len(items1) == 0 {
		t.Fatalf("GET /events = %d (%d items) and %d, want 200 with the session's events on both", code1, len(items1), code2)
	}
	if !reflect.DeepEqual(items1, items2) {
		t.Errorf("GET /events differs between replicas:\n%s %v\n%s %v", r1.ID, items1, r2.ID, items2)
	}

	ctx := context.Background()
	if _, err := h.Messaging.EnqueueDLQ(ctx, lifecycleharness.Tenant, id, sessioninbox.Message{
		MessageID: "msg_held", Payload: heldPayload("held"), EnqueuedAt: h.Clock.Now(),
	}, 0); err != nil {
		t.Fatalf("hold a message: %v", err)
	}
	code, body := h.Do(r2, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
	if code != http.StatusOK || body["state"] != string(session.StateCompleted) {
		t.Fatalf("POST /terminate on %s = %d %v, want 200 completed", r2.ID, code, body)
	}
	h.Settle()
	if n, err := h.DLQ.Len(ctx, lifecycleharness.Tenant, id); err != nil || n != 0 {
		t.Errorf("DLQ after the terminate = %d (%v), want drained", n, err)
	}
	if got := leaseHolder(t, h, id); got != "" {
		t.Errorf("the terminate acquired a lease for %q, want none", got)
	}
}

// spec: 7.2 (Message delivery routing — seven paths, path 6), 7.2
// (Interactive Session Model)
// diagnosis: a held message reached the runtime out of order, more than
// once, or not at all, without a client call: a message to a podless
// suspended session was not delivered after the restore and before a later
// message; a message to a ready session was not held and delivered after
// POST /start; or a durable inbox entry present at resume_pending got a TTL
// or was delivered after a DLQ entry enqueued later.
func TestHeldMessagesReachTheRuntimeInOrder_spec_7_2(t *testing.T) {
	t.Run("podless suspended", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
		r1 := h.Replicas[0]
		id := h.StartSession(r1)
		idleSuspended(t, h, r1, id)
		code, body := h.Send(r1, id, "held")
		if code != http.StatusOK {
			t.Fatalf("message to the suspended session = %d %v", code, body)
		}
		if row := h.AwaitState(id, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
		if code, body := h.Send(r1, id, "later"); code != http.StatusOK {
			t.Fatalf("later message = %d %v", code, body)
		}
		requireOrder(t, h, id, "held", "later")
	})

	t.Run("ready", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4})
		r := h.Replicas[0]
		code, body := h.Do(r, http.MethodPost, "/v1/sessions", map[string]any{
			"runtimeRef": lifecycleharness.RuntimeRef, "userId": "alice@acme.com",
		})
		id, _ := body["id"].(string)
		if code != http.StatusCreated || id == "" {
			t.Fatalf("create = %d %v", code, body)
		}
		h.Track(id)
		if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/finalize", nil); code != http.StatusOK || body["state"] != "ready" {
			t.Fatalf("finalize = %d %v", code, body)
		}
		// spec: §7.2 — a pre-running target refuses an external client's
		// message and holds an inter-session message in its DLQ.
		if code, body := h.Send(r, id, "external"); code != http.StatusConflict {
			t.Fatalf("external message to the ready session = %d %v, want 409 TARGET_NOT_READY", code, body)
		}
		res := h.MCPCall(r, "lenny/send_message", fmt.Sprintf(`{"to":%q,"message":"before-start"}`, id))
		if !strings.Contains(fmt.Sprint(res), string(session.DeliveryStatusQueued)) {
			t.Fatalf("inter-session message to the ready session = %v, want a queued receipt", res)
		}
		if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 1 {
			t.Fatalf("shared DLQ of the ready session holds %d (%v), want the message", n, err)
		}
		if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/start", nil); code != http.StatusOK {
			t.Fatalf("start = %d %v", code, body)
		}
		h.Settle()
		if code, body := h.Send(r, id, "after-start"); code != http.StatusOK {
			t.Fatalf("message after start = %d %v", code, body)
		}
		requireOrder(t, h, id, "before-start", "after-start")
	})

	t.Run("durable inbox entry at resume_pending", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 8, DurableInbox: true})
		r := h.Replicas[0]
		id := h.StartSession(r)
		ctx := context.Background()
		// The entry stands for a message the session's inbox buffered
		// while it was running, serialized as the gateway buffers one.
		if _, err := h.Messaging.EnqueueInbox(ctx, lifecycleharness.Tenant, id, sessioninbox.Message{
			MessageID: "msg_inbox", Payload: heldPayload("inbox-entry"), EnqueuedAt: h.Clock.Now(),
		}); err != nil {
			t.Fatalf("buffer an inbox entry: %v", err)
		}
		// The failure report holds the restore: no spare pod is claimable
		// until the DLQ entry below is enqueued.
		for _, p := range h.Cluster.Pods() {
			if p.Name != h.Row(id).PodAssignment {
				_ = h.Cluster.RemoveSandbox(ctx, p)
			}
		}
		lifecycleharness.Report(t, r, id, string(session.FailureRuntimeCrash))
		if st := h.Row(id).State; st != session.StateResumePending {
			t.Fatalf("after the crash with no spare pod the session is %q, want resume_pending", st)
		}
		inboxKey := sessioninbox.InboxKey(lifecycleharness.Tenant, id)
		if !h.Redis.Exists(inboxKey) {
			t.Fatalf("the durable inbox entry left the inbox at resume_pending")
		}
		if ttl := h.Redis.TTL(inboxKey); ttl != 0 {
			t.Errorf("the durable inbox key carries a TTL of %s at resume_pending, want none", ttl)
		}
		if code, body := h.Send(r, id, "dlq-later"); code != http.StatusOK {
			t.Fatalf("message at resume_pending = %d %v", code, body)
		}
		h.Cluster.AddIdlePod(t)
		if row := h.AwaitState(id, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the session reached %q, want running", row.State)
		}
		requireOrder(t, h, id, "inbox-entry", "dlq-later")
	})
}

// heldPayload is a held message carrying text, serialized as the gateway
// serializes a message it holds.
func heldPayload(text string) []byte {
	return []byte(`{"role":"user","content":"` + text + `"}`)
}

// spec: 7.2 (Message delivery routing — seven paths, path 6)
// diagnosis: the gateway leader's backlog re-check did not move a podless
// suspended session that holds a stranded DLQ entry to resume_pending, or
// the resume driver did not deliver the entry after the restore.
func TestBacklogRecheckResumesAStrandedPodlessSession_spec_7_2(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 8})
	r1 := h.Replicas[0]
	id := h.StartSession(r1)
	idleSuspended(t, h, r1, id)
	if _, err := h.Messaging.EnqueueDLQ(context.Background(), lifecycleharness.Tenant, id, sessioninbox.Message{
		MessageID: "msg_stranded", Payload: heldPayload("stranded"), EnqueuedAt: h.Clock.Now(),
	}, 0); err != nil {
		t.Fatalf("strand a message: %v", err)
	}
	if n := h.Recheck(r1); n != 1 {
		t.Fatalf("the re-check moved %d rows, want 1", n)
	}
	if row := h.AwaitState(id, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
		t.Fatalf("the session reached %q, want running", row.State)
	}
	requireOrder(t, h, id, "stranded")
	if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
		t.Errorf("DLQ after the delivery = %d (%v), want empty", n, err)
	}
}

// spec: 7.2 (Interactive Session Model), 10.1.1 (Stateless Replicas and
// Per-Session Coordination)
// diagnosis: a terminal write served by a replica other than the
// coordinator did not land on the serving replica, the coordinator kept its
// binding beyond two Sweeper intervals, the serving replica returned the
// session's credential leases (only the coordinator's release may), or a
// pod claim or credential lease outlived the session.
func TestTerminalWriteOnAPeerReleasesThroughTheCoordinator_spec_7_2(t *testing.T) {
	writers := []struct {
		name  string
		state session.State
		run   func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string)
	}{
		{"DELETE", session.StateCancelled, func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodDelete, "/v1/sessions/"+id, nil)
		}},
		{"terminate", session.StateCompleted, func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		}},
		{"lenny/cancel_session", session.StateCancelled, func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
			h.CancelSessionMCP(r, id)
		}},
	}
	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 32})
			coordinator, peer := h.Replicas[0], h.Replicas[1]
			for _, pre := range lifecycleharness.PreStates() {
				t.Run(pre.Name, func(t *testing.T) {
					id := pre.Reach(t, h, coordinator)
					h.Settle()
					before := h.CredLeases.LeasesBySession([]string{id})
					pod := h.Row(id).PodAssignment
					w.run(h, peer, id)
					h.Settle()
					row := h.Row(id)
					if row.State != w.state {
						t.Fatalf("state after the peer's write = %q, want %q", row.State, w.state)
					}
					if got := h.CredLeases.LeasesBySession([]string{id}); len(before) > 0 && len(got) != len(before) {
						t.Errorf("the serving replica returned %d of %d credential leases; only the coordinator's release returns them", len(before)-len(got), len(before))
					}
					for i := 0; i < 2; i++ {
						h.Pass()
					}
					if _, ok := coordinator.Registry.Get(id); ok {
						t.Error("the coordinator still binds the terminal session after two Sweeper intervals")
					}
					if n := len(h.CredLeases.LeasesBySession([]string{id})); n != 0 {
						t.Errorf("%d credential leases remain", n)
					}
					claims, err := h.Cluster.BoundClaims(context.Background())
					if err != nil {
						t.Fatalf("list claims: %v", err)
					}
					for _, c := range claims {
						if pod != "" && c == pod {
							t.Errorf("the claim on %s survived the terminal write", pod)
						}
					}
				})
			}
		})
	}
}
