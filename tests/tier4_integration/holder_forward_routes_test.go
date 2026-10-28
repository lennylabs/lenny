// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration tests for the §29.3 Forwarding rules on two gateway
// replicas. Every case runs on the lifecycle harness, whose replicas forward
// an off-holder request to the session's coordinating replica over their
// CH-HOLDERFORWARD listeners with real gateway certificates: "the coordinator
// unreachable" is the coordinator's SetForwardRefuse, and "a forward whose
// connection fails after it is established" is its SetForwardFault. The
// cross-tenant case runs against one cmd/lenny-gateway subprocess, sending
// straight to its forward listener.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// sessionOwner is the user every harness session is created for.
const sessionOwner = "alice@acme.com"

// newForwardCompose builds a two-replica harness over the compose Postgres
// session store and returns it with its first replica, which the scenario
// makes the coordinator, and its second, the serving replica.
func newForwardCompose(t *testing.T, opts lifecycleharness.Options) (*lifecycleharness.Harness, *lifecycleharness.Replica, *lifecycleharness.Replica) {
	t.Helper()
	opts.Replicas = 2
	if opts.Pods == 0 {
		opts.Pods = 8
	}
	h := newComposeWith(t, opts)
	return h, h.Replicas[0], h.Replicas[1]
}

// setRowState moves the session's row to state through the shared store, as
// the gateway's own writer of that state does on the coordinating replica.
// The binding, the pod, and the lease stay as they are.
func setRowState(t *testing.T, h *lifecycleharness.Harness, id string, state session.State) {
	t.Helper()
	if _, err := h.Store.Update(context.Background(), lifecycleharness.Tenant, id, func(r *sessionstore.Session) error {
		r.State = state
		return nil
	}); err != nil {
		t.Fatalf("move %s to %s: %v", id, state, err)
	}
}

// setParent records parent as the session's parent, so a lenny/send_message
// between the two passes the default direct messagingScope.
func setParent(t *testing.T, h *lifecycleharness.Harness, id, parent string) {
	t.Helper()
	if _, err := h.Store.Update(context.Background(), lifecycleharness.Tenant, id, func(r *sessionstore.Session) error {
		r.ParentSessionID = parent
		return nil
	}); err != nil {
		t.Fatalf("set the parent of %s: %v", id, err)
	}
}

// refuseForwards makes the coordinator unreachable for the rest of the test
// or until the returned function restores it.
func refuseForwards(t *testing.T, coordinator *lifecycleharness.Replica) func() {
	t.Helper()
	coordinator.SetForwardRefuse(true)
	restore := func() { coordinator.SetForwardRefuse(false) }
	t.Cleanup(restore)
	return restore
}

// faultForwards closes every forwarded connection to the coordinator after
// its request was read, so each forward's outcome is unknown.
func faultForwards(t *testing.T, coordinator *lifecycleharness.Replica) {
	t.Helper()
	coordinator.SetForwardFault(func(*http.Request) bool { return true })
	t.Cleanup(func() { coordinator.SetForwardFault(nil) })
}

// undeliveredModes are the two failure classes of a forward, each armed on
// the coordinator.
var undeliveredModes = []struct {
	name string
	arm  func(t *testing.T, coordinator *lifecycleharness.Replica)
}{
	{"coordinator unreachable", func(t *testing.T, c *lifecycleharness.Replica) { refuseForwards(t, c) }},
	{"connection fails after it is established", faultForwards},
}

// sendBatch posts the messages to the session on r.
func sendBatch(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string, msgs ...map[string]any) (int, map[string]any) {
	batch := make([]any, 0, len(msgs))
	for _, m := range msgs {
		batch = append(batch, m)
	}
	return h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/messages", map[string]any{"messages": batch})
}

// userMessage is one user message, carrying inReplyTo when it is set.
func userMessage(text, inReplyTo string) map[string]any {
	m := map[string]any{"role": "user", "content": text}
	if inReplyTo != "" {
		m["inReplyTo"] = inReplyTo
	}
	return m
}

// dlqExpiries returns how far past now each DLQ entry of the session
// expires, from the entries' scores. A score holds whole milliseconds, so
// now is truncated to the millisecond before the subtraction; an untruncated
// now would put an expiry of exactly the TTL up to a millisecond short.
func dlqExpiries(t *testing.T, h *lifecycleharness.Harness, id string, now time.Time) []time.Duration {
	t.Helper()
	now = now.Truncate(time.Millisecond)
	key := sessioninbox.DLQKey(lifecycleharness.Tenant, id)
	if !h.Redis.Exists(key) {
		return nil
	}
	members, err := h.Redis.ZMembers(key)
	if err != nil {
		t.Fatalf("read the DLQ of %s: %v", id, err)
	}
	out := make([]time.Duration, 0, len(members))
	for _, m := range members {
		score, err := h.Redis.ZScore(key, m)
		if err != nil {
			t.Fatalf("read a DLQ score of %s: %v", id, err)
		}
		out = append(out, time.UnixMilli(int64(score)).Sub(now))
	}
	return out
}

// requireRecoveringTTL fails unless the session's DLQ holds want entries,
// each expiring at the dead-letter table's recovering-state TTL, the
// harness's resume window, rather than at the default DLQ TTL.
func requireRecoveringTTL(t *testing.T, h *lifecycleharness.Harness, id string, now time.Time, want int) {
	t.Helper()
	expiries := dlqExpiries(t, h, id, now)
	if len(expiries) != want {
		t.Fatalf("DLQ of %s holds %d entries, want %d", id, len(expiries), want)
	}
	for _, d := range expiries {
		if d < lifecycleharness.MaxResumePending || d > lifecycleharness.MaxResumePending+time.Second {
			t.Errorf("DLQ entry of %s expires %v after acceptance, want the recovering-state TTL %v (the default is %v)",
				id, d, lifecycleharness.MaxResumePending, sessioninbox.DefaultDLQTTL)
		}
	}
}

// requireEmptyDLQ fails when the session's DLQ holds an entry.
func requireEmptyDLQ(t *testing.T, h *lifecycleharness.Harness, id string) {
	t.Helper()
	if n, err := h.DLQ.Len(context.Background(), lifecycleharness.Tenant, id); err != nil || n != 0 {
		t.Errorf("DLQ of %s holds %d entries (%v), want none", id, n, err)
	}
}

// inboxLen is the length of the session's inbox.
func inboxLen(t *testing.T, h *lifecycleharness.Harness, id string) int {
	t.Helper()
	n, err := h.Inbox.Len(context.Background(), lifecycleharness.Tenant, id)
	if err != nil {
		t.Fatalf("read the inbox of %s: %v", id, err)
	}
	return n
}

// requireForwardRefusal fails unless the answer is 409 TARGET_NOT_READY
// carrying the session's current state.
func requireForwardRefusal(t *testing.T, code int, body map[string]any, state session.State) {
	t.Helper()
	if code != http.StatusConflict || restErrorCode(body) != "TARGET_NOT_READY" {
		t.Fatalf("answer = %d %v, want 409 TARGET_NOT_READY", code, body)
	}
	details, _ := body["error"].(map[string]any)["details"].(map[string]any)
	if got, _ := details["currentState"].(string); got != string(state) {
		t.Errorf("currentState = %q, want %q", got, state)
	}
}

// requireToolRefusal fails unless the MCP or platform call answered the tool
// error TARGET_NOT_READY carrying currentState.
func requireToolRefusal(t *testing.T, res map[string]any) {
	t.Helper()
	s := fmt.Sprint(res)
	if !strings.Contains(s, "TARGET_NOT_READY") || !strings.Contains(s, "currentState") {
		t.Fatalf("call answered %v, want the tool error TARGET_NOT_READY with currentState", res)
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 1 and 2, 7.2
// (Interactive Session Model)
// diagnosis: a message to a running session sent to the replica that does
// not coordinate it was answered 500, held in the serving replica's inbox, or
// delivered by the serving replica, rather than forwarded over
// CH-HOLDERFORWARD to the REG-COORDLEASE holder, which delivers it and
// answers the receipt. Check forwardMessageBatch in
// pkg/gateway/sessionserver/messages_delivery.go.
func TestOffHolderMessageToRunningIsDeliveredByTheCoordinator_spec_29_3(t *testing.T) {
	h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
	id := h.StartSession(coordinator)
	forwards := countForwards(coordinator)
	code, body := h.Send(peer, id, "via-peer")
	if code != http.StatusOK || receiptStatus(body) != string(session.DeliveryStatusDelivered) {
		t.Fatalf("message on the serving replica = %d %v, want 200 delivered", code, body)
	}
	if n := forwards.Load(); n != 1 {
		t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
	}
	h.Settle()
	if n := receivedCount(h, id, "via-peer"); n != 1 {
		t.Errorf("the runtime received the message %d times, want once", n)
	}
	if n := inboxLen(t, h, id); n != 0 {
		t.Errorf("inbox holds %d entries, want none", n)
	}
	requireEmptyDLQ(t, h, id)
}

// spec: 29.3 (Interactive message send) Forwarding rules 1 and 6, 7.2
// (Message delivery routing — seven paths, paths 2, 3, and 6)
// diagnosis: a message carrying no inReplyTo to an input_required session,
// sent to the replica that does not coordinate it, was buffered in the
// serving replica's inbox, where the coordinator never reads it; or, with
// the coordinator unreachable, it was refused, held with the default DLQ TTL
// rather than the recovering-state TTL, written anywhere besides the DLQ, or
// never delivered when the coordinator next delivered under path 2. Check
// holdUndeliveredForward in pkg/gateway/sessionserver/messages_delivery.go.
func TestOffHolderMessageToInputRequiredIsBufferedOrHeld_spec_29_3(t *testing.T) {
	t.Run("coordinator buffers under path 3", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		setRowState(t, h, id, session.StateInputRequired)
		forwards := countForwards(coordinator)
		if code, body := h.Send(peer, id, "buffered"); code != http.StatusOK {
			t.Fatalf("message on the serving replica = %d %v, want 200", code, body)
		}
		if n := forwards.Load(); n != 1 {
			t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
		}
		if n := inboxLen(t, h, id); n != 1 {
			t.Errorf("inbox holds %d entries, want the coordinator's path-3 buffer of 1", n)
		}
		requireEmptyDLQ(t, h, id)
		if n := receivedCount(h, id, "buffered"); n != 0 {
			t.Errorf("the runtime received the buffered message %d times, want none while input_required", n)
		}
	})

	t.Run("coordinator unreachable holds in the DLQ", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		setRowState(t, h, id, session.StateInputRequired)
		restore := refuseForwards(t, coordinator)
		now := h.Clock.Now()
		code, body := h.Send(peer, id, "held")
		if code != http.StatusOK || receiptStatus(body) != string(session.DeliveryStatusQueued) {
			t.Fatalf("message on the serving replica = %d %v, want 200 queued", code, body)
		}
		requireRecoveringTTL(t, h, id, now, 1)
		if n := inboxLen(t, h, id); n != 0 {
			t.Errorf("inbox holds %d entries, want the DLQ enqueue as the only write", n)
		}
		if st := h.Row(id).State; st != session.StateInputRequired {
			t.Errorf("state = %q after the hold, want input_required unwritten", st)
		}

		restore()
		setRowState(t, h, id, session.StateRunning)
		if code, body := h.Send(coordinator, id, "next"); code != http.StatusOK {
			t.Fatalf("message on the coordinator = %d %v, want 200", code, body)
		}
		h.Settle()
		texts := h.Cluster.ReceivedTexts(id)
		held, next := -1, -1
		for i, text := range texts {
			switch text {
			case "held":
				held = i
			case "next":
				next = i
			}
		}
		if held < 0 || next < 0 || held > next {
			t.Errorf("runtime received %v, want the held message delivered before the coordinator's next path-2 message", texts)
		}
		requireEmptyDLQ(t, h, id)
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 1 and 6
// diagnosis: an inReplyTo reply sent to the replica that does not coordinate
// the session did not reach the coordinator's pending lenny/request_input
// wait; or, with the coordinator unreachable, the reply, a reply matching no
// outstanding call, or a batch in which one message carries inReplyTo was
// held in the DLQ or answered as queued. A held reply never resolves the
// wait, so the serving replica must fail closed on the presence of
// inReplyTo. Check messageForwardKind in
// pkg/gateway/sessionserver/messages_delivery.go.
func TestOffHolderInReplyToReplyResolvesOrFailsClosed_spec_29_3(t *testing.T) {
	t.Run("forwarded reply resolves the coordinator's wait", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		answer, err := coordinator.InputWaits.Register(id, "req-1", nil)
		if err != nil {
			t.Fatalf("register the input wait: %v", err)
		}
		setRowState(t, h, id, session.StateInputRequired)
		if code, body := sendBatch(h, peer, id, userMessage("yes", "req-1")); code != http.StatusOK {
			t.Fatalf("reply on the serving replica = %d %v, want 200", code, body)
		}
		select {
		case got := <-answer:
			if !strings.Contains(got, "yes") {
				t.Errorf("the wait resolved with %q, want the reply", got)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the coordinator's lenny/request_input wait was not resolved by the forwarded reply")
		}
	})

	t.Run("coordinator unreachable", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		if _, err := coordinator.InputWaits.Register(id, "req-2", nil); err != nil {
			t.Fatalf("register the input wait: %v", err)
		}
		setRowState(t, h, id, session.StateInputRequired)
		refuseForwards(t, coordinator)
		batches := map[string][]map[string]any{
			"matching reply":           {userMessage("yes", "req-2")},
			"reply matching no call":   {userMessage("yes", "req-none")},
			"one reply among messages": {userMessage("first", ""), userMessage("yes", "req-2")},
		}
		for name, batch := range batches {
			code, body := sendBatch(h, peer, id, batch...)
			if code != http.StatusConflict || restErrorCode(body) != "TARGET_NOT_READY" {
				t.Errorf("%s = %d %v, want 409 TARGET_NOT_READY", name, code, body)
			}
			requireEmptyDLQ(t, h, id)
		}
		if !coordinator.InputWaits.Pending(id, "req-2") {
			t.Error("the coordinator's wait left pending; a refused reply must not resolve it")
		}
	})
}

// spec: 29.3 (Interactive message send) Forwarding rules 1 and 6, 7.2
// (Message delivery routing — seven paths, path 6)
// diagnosis: a held-pod resume sent to the replica that does not coordinate
// the session answered 409, or wrote running on the serving replica, rather
// than being forwarded to the coordinator, which resumes the session on its
// pod; or, with the coordinator unreachable, the serving replica wrote a
// state. Check resumeHeldPodRequest in pkg/gateway/sessionserver/resume.go.
func TestOffHolderHeldPodResumeIsWrittenByTheCoordinator_spec_29_3(t *testing.T) {
	t.Run("forwarded", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		interruptSuspended(t, h, coordinator, id)
		forwards := countForwards(coordinator)
		if code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/resume", nil); code != http.StatusOK {
			t.Fatalf("resume on the serving replica = %d %v, want 200", code, body)
		}
		if n := forwards.Load(); n != 1 {
			t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
		}
		if st := h.Row(id).State; st != session.StateRunning {
			t.Errorf("state = %q after the forwarded resume, want running", st)
		}
	})
	t.Run("coordinator unreachable", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		interruptSuspended(t, h, coordinator, id)
		refuseForwards(t, coordinator)
		code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/resume", nil)
		requireForwardRefusal(t, code, body, session.StateSuspended)
		if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment == "" {
			t.Errorf("row = %q on %q, want suspended holding its pod", row.State, row.PodAssignment)
		}
	})
}

// gatedState is one state in which a terminate or a delete is forwarded,
// reached on the coordinating replica.
type gatedState struct {
	name  string
	state session.State
	reach func(t *testing.T, h *lifecycleharness.Harness, coordinator *lifecycleharness.Replica) string
}

func terminateGatedStates() []gatedState {
	forced := func(st session.State) func(*testing.T, *lifecycleharness.Harness, *lifecycleharness.Replica) string {
		return func(t *testing.T, h *lifecycleharness.Harness, c *lifecycleharness.Replica) string {
			id := h.StartSession(c)
			setRowState(t, h, id, st)
			return id
		}
	}
	return []gatedState{
		{"running", session.StateRunning, func(t *testing.T, h *lifecycleharness.Harness, c *lifecycleharness.Replica) string {
			return h.StartSession(c)
		}},
		{"input_required", session.StateInputRequired, forced(session.StateInputRequired)},
		{"resuming", session.StateResuming, forced(session.StateResuming)},
		{"held-pod suspended", session.StateSuspended, func(t *testing.T, h *lifecycleharness.Harness, c *lifecycleharness.Replica) string {
			id := h.StartSession(c)
			interruptSuspended(t, h, c, id)
			return id
		}},
	}
}

// terminalRoutes are the terminate and the delete with the state each
// writes.
var terminalRoutes = []struct {
	name, method, suffix string
	want                 session.State
}{
	{"terminate", http.MethodPost, "/terminate", session.StateCompleted},
	{"delete", http.MethodDelete, "", session.StateCancelled},
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 6, and 7, 15.1
// (REST API)
// diagnosis: a terminate or a delete sent to the replica that does not
// coordinate the session wrote the terminal state there, so the coordinator's
// termination sequence did not run, rather than being forwarded to the
// coordinator; the input_required and resuming cases discriminate a gate that
// looks up only the states a §29.3 row names. With the coordinator
// unreachable, or a forward whose connection fails after it is established,
// the serving replica wrote a state rather than answering 409. Check
// handleTerminalWrite in pkg/gateway/sessionserver/session_transitions.go.
func TestOffHolderTerminateAndDeleteRunOnTheCoordinator_spec_29_3(t *testing.T) {
	for _, gs := range terminateGatedStates() {
		for _, route := range terminalRoutes {
			t.Run(route.name+" in "+gs.name, func(t *testing.T) {
				h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
				id := gs.reach(t, h, coordinator)
				if st := h.Row(id).State; st != gs.state {
					t.Fatalf("pre-state = %q, want %q", st, gs.state)
				}
				forwards := countForwards(coordinator)
				code, body := h.Do(peer, route.method, "/v1/sessions/"+id+route.suffix, nil)
				if code >= http.StatusBadRequest {
					t.Fatalf("%s on the serving replica = %d %v, want the coordinator's success", route.name, code, body)
				}
				if n := forwards.Load(); n != 1 {
					t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
				}
				if st := h.Row(id).State; st != route.want {
					t.Errorf("state = %q, want %q written by the coordinator", st, route.want)
				}
				if _, ok := coordinator.Registry.Get(id); ok && gs.state == session.StateRunning {
					t.Error("the coordinator kept the session's binding; its termination sequence did not run")
				}
			})
		}
	}
	for _, mode := range undeliveredModes {
		for _, route := range terminalRoutes {
			t.Run(route.name+" with the "+mode.name, func(t *testing.T) {
				h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
				id := h.StartSession(coordinator)
				mode.arm(t, coordinator)
				code, body := h.Do(peer, route.method, "/v1/sessions/"+id+route.suffix, nil)
				requireForwardRefusal(t, code, body, session.StateRunning)
				if row := h.Row(id); row.State != session.StateRunning || !row.TerminatedAt.IsZero() {
					t.Errorf("row = %q (terminated %v), want running and unwritten", row.State, row.TerminatedAt)
				}
				if _, ok := coordinator.Registry.Get(id); !ok {
					t.Error("the coordinator lost the session's binding although no terminal state was written")
				}
			})
		}
	}
	t.Run("terminate in created is written locally", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.CreateSession(peer)
		forwards := countForwards(coordinator)
		peerForwards := countForwards(peer)
		code, body := h.Do(coordinator, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		if code >= http.StatusBadRequest || !session.IsTerminal(h.Row(id).State) {
			t.Fatalf("terminate in created = %d %v state %q, want a local terminal write", code, body, h.Row(id).State)
		}
		if n := forwards.Load() + peerForwards.Load(); n != 0 {
			t.Errorf("forward listeners received %d requests, want none for a session with no coordinating replica", n)
		}
	})
}

// staleLeases answers the next lease read with a stale holder value, as a
// replica that read the lease just before a takeover sees it.
type staleLeases struct {
	leasestore.LeaseStore
	stale atomic.Pointer[string]
}

func (s *staleLeases) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	if holder := s.stale.Swap(nil); holder != nil {
		return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: *holder, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	return s.LeaseStore.Get(ctx, tenantID, sessionID)
}

// spec: 29.3 (Interactive message send) Forwarding rule 5, 28.6
// (Exclusivity and concurrency model)
// diagnosis: a forward that reached a replica that no longer holds the
// session's lease was applied there, writing a state or driving the pod, or
// was forwarded again, rather than refused under the never-forward-twice
// rule. Check forwardTarget in pkg/gateway/sessionserver/holder_forward.go.
func TestStaleForwardIsRefusedByTheFormerHolder_spec_29_3(t *testing.T) {
	cases := []struct {
		name string
		send func(h *lifecycleharness.Harness, serving *lifecycleharness.Replica, id string) (int, map[string]any)
	}{
		{"terminate", func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) (int, map[string]any) {
			return h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
		}},
		{"message", func(h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) (int, map[string]any) {
			return h.Send(r, id, "stale")
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
			before := h.Row(id)
			shutdowns := h.Cluster.PodOf(pod).UnaryCalls("Shutdown")
			forwards := countForwards(former)
			holder := former.ID
			leases.stale.Store(&holder)
			code, body := tc.send(h, current, id)
			if n := forwards.Load(); n != 1 {
				t.Fatalf("the former holder's forward listener received %d requests, want the one stale forward", n)
			}
			if code != http.StatusConflict && receiptStatus(body) != string(session.DeliveryStatusQueued) {
				t.Errorf("stale forward answered %d %v, want 409 or a DLQ hold", code, body)
			}
			after := h.Row(id)
			if after.State != before.State || !after.TerminatedAt.IsZero() {
				t.Errorf("row = %q (terminated %v), want %q unwritten", after.State, after.TerminatedAt, before.State)
			}
			if n := receivedCount(h, id, "stale"); n != 0 {
				t.Errorf("the runtime received the stale message %d times, want none", n)
			}
			if n := h.Cluster.PodOf(pod).UnaryCalls("Shutdown") - shutdowns; n != 0 {
				t.Errorf("the pod received %d Shutdowns, want none", n)
			}
		})
	}
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 6, and 7, 7.4
// (Upload Safety), 15.1 (REST API), 7.2 (Session state machine)
// diagnosis: a mid-session upload or an interrupt sent to the replica that
// does not coordinate the session was served there, pushing files to or
// interrupting a pod the serving replica does not hold, or writing
// suspended, rather than being forwarded. An interrupt of a session in
// input_required was forwarded or written rather than refused with 409
// INVALID_STATE_TRANSITION before any forward, because the interrupt row of
// §15.1 admits running alone. With the coordinator unreachable, or a forward
// whose connection fails after it is established, the serving replica wrote
// a state. Check handleUploadToSession and handleInterrupt in
// pkg/gateway/sessionserver and the interrupt entry in pkg/api/v1/session.
func TestOffHolderUploadAndInterruptAreForwarded_spec_29_3(t *testing.T) {
	t.Run("upload-to-session", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{MidSessionUpload: true})
		id := h.StartSession(coordinator)
		forwards := countForwards(coordinator)
		code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/upload-to-session",
			map[string]any{"files": []any{map[string]any{"path": "a.txt", "content": "YQ=="}}})
		if n := forwards.Load(); n != 1 {
			t.Fatalf("the coordinator's forward listener received %d requests (answer %d %v), want 1", n, code, body)
		}
		if restErrorCode(body) == "TARGET_NOT_READY" {
			t.Errorf("upload answered %d %v, want the coordinator's answer", code, body)
		}
	})
	t.Run("interrupt in running", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		forwards := countForwards(coordinator)
		if code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil); code != http.StatusOK {
			t.Fatalf("interrupt on the serving replica = %d %v, want 200", code, body)
		}
		if n := forwards.Load(); n != 1 {
			t.Errorf("the coordinator's forward listener received %d requests, want 1", n)
		}
		if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment == "" {
			t.Errorf("row = %q on %q, want suspended on its pod by the coordinator", row.State, row.PodAssignment)
		}
	})
	t.Run("interrupt in input_required is refused before any forward", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		setRowState(t, h, id, session.StateInputRequired)
		before := h.Row(id)
		forwards := countForwards(coordinator)
		code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil)
		if code != http.StatusConflict || restErrorCode(body) != "INVALID_STATE_TRANSITION" {
			t.Fatalf("interrupt on the serving replica = %d %v, want 409 INVALID_STATE_TRANSITION", code, body)
		}
		if n := forwards.Load(); n != 0 {
			t.Errorf("the coordinator's forward listener received %d requests, want none", n)
		}
		if row := h.Row(id); row.State != session.StateInputRequired || row.PodAssignment != before.PodAssignment {
			t.Errorf("row = %q on %q, want input_required on %q unwritten", row.State, row.PodAssignment, before.PodAssignment)
		}
	})
	for _, mode := range undeliveredModes {
		t.Run("interrupt with the "+mode.name, func(t *testing.T) {
			h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
			id := h.StartSession(coordinator)
			mode.arm(t, coordinator)
			code, body := h.Do(peer, http.MethodPost, "/v1/sessions/"+id+"/interrupt", nil)
			requireForwardRefusal(t, code, body, session.StateRunning)
			if st := h.Row(id).State; st != session.StateRunning {
				t.Errorf("state = %q, want running unwritten", st)
			}
		})
	}
}

// awaitPendingInteraction waits until the interaction is recorded pending.
func awaitPendingInteraction(t *testing.T, h *lifecycleharness.Harness, id, interactionID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		in, err := h.Interactions.Get(context.Background(), lifecycleharness.Tenant, id, sessionOwner, interactionID)
		if err == nil && in.Phase == interactionstore.PhasePending {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("interaction %s of %s was never recorded pending", interactionID, id)
}

// spec: 29.3 (Interactive message send) the interaction resolution rows,
// 7.2 (Interactive Session Model)
// diagnosis: an approve, deny, respond, or dismiss posted to the replica
// that does not coordinate the session did not wake the coordinator's
// pending wait through the shared interaction store, or was forwarded; or a
// second resolution posted to the other replica was accepted and changed the
// blocked call's outcome. Check resolveInteraction in
// pkg/gateway/sessionserver/interactions_resolution.go and the store poll of
// ToolApprovalGate.AwaitApproval.
func TestOffHolderInteractionResolutionWakesTheCoordinator_spec_29_3(t *testing.T) {
	type outcome struct {
		decision executor.ApprovalDecision
		err      error
	}
	for _, verdict := range []struct {
		path     string
		approved bool
		second   string
	}{
		{"approve", true, "deny"},
		{"deny", false, "approve"},
	} {
		t.Run("tool-use "+verdict.path, func(t *testing.T) {
			h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
			id := h.StartSession(coordinator)
			gate := sessionserver.NewToolApprovalGate(h.Store, h.Interactions, nil, toolapproval.NewRegistry(), h.Clock.Now, 0)
			done := make(chan outcome, 1)
			go func() {
				d, err := gate.AwaitApproval(context.Background(), lifecycleharness.Tenant, id, executor.PendingToolCall{ID: "call-1", Name: "shell"})
				done <- outcome{d, err}
			}()
			awaitPendingInteraction(t, h, id, "call-1")
			forwards := countForwards(coordinator)
			path := "/v1/sessions/" + id + "/tool-use/call-1/"
			if code, body := h.DoAs(peer, sessionOwner, http.MethodPost, path+verdict.path, nil); code >= http.StatusBadRequest {
				t.Fatalf("%s on the serving replica = %d %v", verdict.path, code, body)
			}
			select {
			case got := <-done:
				if got.err != nil || got.decision.Approved != verdict.approved {
					t.Fatalf("the coordinator's wait ended with %+v (%v), want approved=%v", got.decision, got.err, verdict.approved)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the coordinator's approval wait was not woken by the resolution on the serving replica")
			}
			code, body := h.DoAs(coordinator, sessionOwner, http.MethodPost, path+verdict.second, nil)
			if code != http.StatusConflict || restErrorCode(body) != "INTERACTION_ALREADY_RESOLVED" {
				t.Errorf("second resolution on the other replica = %d %v, want 409 INTERACTION_ALREADY_RESOLVED", code, body)
			}
			if n := forwards.Load(); n != 0 {
				t.Errorf("the coordinator's forward listener received %d requests, want none", n)
			}
			in, err := h.Interactions.Get(context.Background(), lifecycleharness.Tenant, id, sessionOwner, "call-1")
			wantPhase := interactionstore.PhaseDenied
			if verdict.approved {
				wantPhase = interactionstore.PhaseApproved
			}
			if err != nil || in.Phase != wantPhase {
				t.Errorf("interaction = %+v (%v), want the first resolution %s kept", in, err, wantPhase)
			}
		})
	}
	for _, verdict := range []struct {
		path, second, want string
		body               any
	}{
		{"respond", "dismiss", "from-the-peer", map[string]any{"response": map[string]any{"answer": "from-the-peer"}}},
		{"dismiss", "respond", "dismissed", nil},
	} {
		t.Run("elicitation "+verdict.path, func(t *testing.T) {
			h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
			id := h.StartSession(coordinator)
			done := make(chan map[string]any, 1)
			go func() {
				done <- h.PlatformCall(coordinator, id, "lenny/request_elicitation", `{"message":"ok?","schema":{},"elicitationId":"elic-1"}`)
			}()
			awaitPendingInteraction(t, h, id, "elic-1")
			forwards := countForwards(coordinator)
			path := "/v1/sessions/" + id + "/elicitations/elic-1/"
			if code, body := h.DoAs(peer, sessionOwner, http.MethodPost, path+verdict.path, verdict.body); code >= http.StatusBadRequest {
				t.Fatalf("%s on the serving replica = %d %v", verdict.path, code, body)
			}
			select {
			case res := <-done:
				if !strings.Contains(fmt.Sprint(res), verdict.want) {
					t.Fatalf("the coordinator's elicitation wait returned %v, want %s", res, verdict.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the coordinator's elicitation wait was not woken by the resolution on the serving replica")
			}
			code, body := h.DoAs(coordinator, sessionOwner, http.MethodPost, path+verdict.second, map[string]any{"response": map[string]any{"answer": "second"}})
			if code != http.StatusConflict || restErrorCode(body) != "INTERACTION_ALREADY_RESOLVED" {
				t.Errorf("second resolution on the other replica = %d %v, want 409 INTERACTION_ALREADY_RESOLVED", code, body)
			}
			if n := forwards.Load(); n != 0 {
				t.Errorf("the coordinator's forward listener received %d requests, want none", n)
			}
		})
	}
}

// countingResultInterceptor counts the PreToolResult phases it runs.
type countingResultInterceptor struct{ runs atomic.Int64 }

func (c *countingResultInterceptor) Name() string    { return "count-results" }
func (c *countingResultInterceptor) Priority() int32 { return 50 }
func (c *countingResultInterceptor) Builtin() bool   { return true }
func (c *countingResultInterceptor) FailPolicy() interceptor.FailPolicy {
	return interceptor.FailClosed
}
func (c *countingResultInterceptor) Timeout() time.Duration { return 0 }
func (c *countingResultInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	c.runs.Add(1)
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// mcpSend calls lenny/send_message from an MCP client on r.
func mcpSend(h *lifecycleharness.Harness, r *lifecycleharness.Replica, to, text, inReplyTo string) map[string]any {
	args := fmt.Sprintf(`{"to":%q,"message":%q}`, to, text)
	if inReplyTo != "" {
		args = fmt.Sprintf(`{"to":%q,"message":%q,"inReplyTo":%q}`, to, text, inReplyTo)
	}
	return h.MCPCall(r, "lenny/send_message", args)
}

// platformSend calls lenny/send_message as a platform tool call from the
// sender's pod on r.
func platformSend(h *lifecycleharness.Harness, r *lifecycleharness.Replica, sender, to, text string) map[string]any {
	return h.PlatformCall(r, sender, "lenny/send_message", fmt.Sprintf(`{"to":%q,"message":%q}`, to, text))
}

// requireReceipt fails unless the call's answer carries the receipt status.
func requireReceipt(t *testing.T, res map[string]any, status session.DeliveryStatus) {
	t.Helper()
	if !strings.Contains(fmt.Sprint(res), string(status)) {
		t.Fatalf("call answered %v, want a %s receipt", res, status)
	}
}

// receivedFrom returns the from-object of the message with text the
// session's runtimes received.
func receivedFrom(h *lifecycleharness.Harness, id, text string) (string, string, bool) {
	for _, m := range h.Cluster.Received(id) {
		if m.Text == text {
			return m.FromKind, m.FromID, true
		}
	}
	return "", "", false
}

// spec: 29.3 (Interactive message send) Forwarding rules 1, 2, 3, 4, 6, and
// 7, 15.2 (MCP API), 7.2 (Interactive Session Model), 8.3 (Delegation Policy
// and Lease)
// diagnosis: an off-holder lenny/send_message or lenny/cancel_session was
// served on the serving replica or forwarded as its REST route rather than
// as the tools/call: a REST-route forward loses the sender, so the
// coordinator neither refuses an out-of-scope target with SCOPE_DENIED nor
// stamps from.kind agent. Or an undelivered send was held without the scope
// check or with the default DLQ TTL; a refusal was swallowed by the
// playground_client_closed path; the lifetime cap counted per coordinating
// replica; or PreToolResult ran on both replicas. Check the lenny/send_message
// and lenny/cancel_session handlers in
// pkg/gateway/mcpfabric/mcptools/mcptools_register.go.
func TestOffHolderMCPToolCallsForwardAsToolCalls_spec_29_3(t *testing.T) {
	t.Run("MCP client send and cancel", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		forwards := countForwards(coordinator)
		requireReceipt(t, mcpSend(h, peer, id, "via-mcp", ""), session.DeliveryStatusDelivered)
		h.Settle()
		if n := receivedCount(h, id, "via-mcp"); n != 1 {
			t.Errorf("the runtime received the MCP message %d times, want once", n)
		}
		h.CancelSessionMCP(peer, id)
		if st := h.Row(id).State; st != session.StateCancelled {
			t.Errorf("state = %q after the off-holder lenny/cancel_session, want cancelled by the coordinator", st)
		}
		if n := forwards.Load(); n != 2 {
			t.Errorf("the coordinator's forward listener received %d requests, want the send and the cancel", n)
		}
	})

	t.Run("platform call carries the calling session", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		sender := h.StartSession(peer)
		child := h.StartSession(coordinator)
		stranger := h.StartSession(coordinator)
		setParent(t, h, child, sender)
		res := platformSend(h, peer, sender, stranger, "out-of-scope")
		if !strings.Contains(fmt.Sprint(res), "SCOPE_DENIED") {
			t.Errorf("out-of-scope platform send = %v, want SCOPE_DENIED from the coordinator", res)
		}
		requireReceipt(t, platformSend(h, peer, sender, child, "in-scope"), session.DeliveryStatusDelivered)
		h.Settle()
		kind, from, ok := receivedFrom(h, child, "in-scope")
		if !ok || kind != "agent" || from != sender {
			t.Errorf("delivered from = {%q %q} (received %v), want {agent %s}", kind, from, ok, sender)
		}
	})

	t.Run("coordinator unreachable", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		sender := h.StartSession(peer)
		child := h.StartSession(coordinator)
		stranger := h.StartSession(coordinator)
		setParent(t, h, child, sender)
		refuseForwards(t, coordinator)
		if res := platformSend(h, peer, sender, stranger, "out-of-scope"); !strings.Contains(fmt.Sprint(res), "SCOPE_DENIED") {
			t.Errorf("out-of-scope platform send = %v, want SCOPE_DENIED", res)
		}
		requireEmptyDLQ(t, h, stranger)
		now := h.Clock.Now()
		requireReceipt(t, mcpSend(h, peer, child, "held-mcp", ""), session.DeliveryStatusQueued)
		requireReceipt(t, platformSend(h, peer, sender, child, "held-platform"), session.DeliveryStatusQueued)
		requireRecoveringTTL(t, h, child, now, 2)
		requireToolRefusal(t, mcpSend(h, peer, stranger, "reply", "req-1"))
		requireEmptyDLQ(t, h, stranger)
		for _, reason := range []string{"", "playground_client_closed"} {
			requireToolRefusal(t, h.MCPCall(peer, "lenny/cancel_session", fmt.Sprintf(`{"sessionId":%q,"reason":%q}`, stranger, reason)))
		}
		if row := h.Row(stranger); row.State != session.StateRunning || !row.TerminatedAt.IsZero() {
			t.Errorf("row = %q (terminated %v), want running and unwritten", row.State, row.TerminatedAt)
		}
	})

	t.Run("connection fails after it is established", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{})
		id := h.StartSession(coordinator)
		faultForwards(t, coordinator)
		requireToolRefusal(t, mcpSend(h, peer, id, "unknown-outcome", ""))
		requireEmptyDLQ(t, h, id)
		for _, reason := range []string{"", "playground_client_closed"} {
			requireToolRefusal(t, h.MCPCall(peer, "lenny/cancel_session", fmt.Sprintf(`{"sessionId":%q,"reason":%q}`, id, reason)))
		}
		if row := h.Row(id); row.State != session.StateRunning || !row.TerminatedAt.IsZero() {
			t.Errorf("row = %q (terminated %v), want running and unwritten", row.State, row.TerminatedAt)
		}
	})

	t.Run("lifetime cap spans both replicas", func(t *testing.T) {
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{
			MessagingRateLimit: mcptoolsLifetimeCap(3),
		})
		sender := h.StartSession(peer)
		local := h.StartSession(peer)
		remote := h.StartSession(coordinator)
		setParent(t, h, local, sender)
		setParent(t, h, remote, sender)
		requireReceipt(t, platformSend(h, peer, sender, local, "one"), session.DeliveryStatusDelivered)
		requireReceipt(t, platformSend(h, peer, sender, remote, "two"), session.DeliveryStatusDelivered)
		restore := refuseForwards(t, coordinator)
		requireReceipt(t, platformSend(h, peer, sender, remote, "three"), session.DeliveryStatusQueued)
		restore()
		requireReceipt(t, platformSend(h, peer, sender, local, "four"), session.DeliveryStatusRateLimited)
	})

	t.Run("PreToolResult runs once", func(t *testing.T) {
		counter := &countingResultInterceptor{}
		chain := interceptor.NewChain()
		if err := chain.Register(interceptor.PhasePreToolResult, counter); err != nil {
			t.Fatalf("register the interceptor: %v", err)
		}
		h, coordinator, peer := newForwardCompose(t, lifecycleharness.Options{Interceptors: chain})
		id := h.StartSession(coordinator)
		before := counter.runs.Load()
		requireReceipt(t, mcpSend(h, peer, id, "once", ""), session.DeliveryStatusDelivered)
		if n := counter.runs.Load() - before; n != 1 {
			t.Errorf("PreToolResult ran %d times for one forwarded call, want once on the serving replica", n)
		}
	})
}

// mcptoolsLifetimeCap is the messaging limits with the per-sender lifetime
// cap set to n and every other limit at its spec default.
func mcptoolsLifetimeCap(n int) mcptools.MessagingRateLimit {
	return mcptools.MessagingRateLimit{MaxPerSession: n}
}

// forwardListenerClient is a CH-HOLDERFORWARD client presenting the gateway
// certificate of m.
func forwardListenerClient(t *testing.T, m *gatewaypki.Material) *http.Client {
	t.Helper()
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatalf("load the gateway certificate: %v", err)
	}
	return sessionserver.NewHolderForwardClient(reloader, m.CAPool)
}

// awaitForwardListener waits until the gateway subprocess's forward
// listener accepts connections.
func awaitForwardListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the forward listener on %s never accepted a connection", addr)
}

// spec: 29.3 (Interactive message send) Forwarding rule 3, 15.1 (REST API)
// diagnosis: a request arriving on the real gateway's CH-HOLDERFORWARD
// listener from a peer presenting the gateway certificate, naming an acme
// session and carrying a valid globex credential, was authorized on the
// peer's word or under the wrong tenant and terminated the session, rather
// than authenticated and authorized by the coordinator's own handler, which
// cannot see another tenant's session and answers 404. Check
// NewHolderForwardHandler in pkg/gateway/sessionserver and the forward
// listener wiring in cmd/lenny-gateway/holder_forward_listener.go.
func TestForwardedCrossTenantTerminateIsNotFound_spec_29_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	m := gatewaypki.Mint(t)
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode", "--agent-runtime", "echo",
		"--redis-url=redis://"+rd.Addr+"/0",
		"--adapter-tls-cert="+m.CertFile, "--adapter-tls-key="+m.KeyFile, "--adapter-ca="+m.CAFile)
	do := gateReq(t, gw.BaseURL())
	if code, boot := do(http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}, {"id": "globex", "displayName": "Globex"}},
		"runtimes": []map[string]any{{
			"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
		}},
	}); code != http.StatusOK {
		t.Fatalf("bootstrap: status %d (%v)", code, boot)
	}
	code, created := do(http.MethodPost, "/v1/sessions/start", "", map[string]any{"runtimeRef": "echo", "userId": sessionOwner})
	id, _ := created["id"].(string)
	if code != http.StatusCreated || id == "" {
		t.Fatalf("create-and-start: status %d (%v)", code, created)
	}
	_, before := do(http.MethodGet, "/v1/sessions/"+id, "", nil)

	awaitForwardListener(t, gw.ForwardAddr())
	req, err := http.NewRequest(http.MethodPost, "https://"+gw.ForwardAddr()+"/v1/sessions/"+id+"/terminate", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	req.Header.Set("X-Lenny-User-ID", "bob@globex.com")
	resp, err := forwardListenerClient(t, m).Do(req)
	if err != nil {
		t.Fatalf("forwarded terminate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusNotFound || restErrorCode(body) != "RESOURCE_NOT_FOUND" {
		t.Errorf("forwarded cross-tenant terminate = %d %v, want 404 RESOURCE_NOT_FOUND", resp.StatusCode, body)
	}
	_, after := do(http.MethodGet, "/v1/sessions/"+id, "", nil)
	if after["state"] != before["state"] || after["state"] == nil {
		t.Errorf("acme session state = %v after the cross-tenant terminate, want %v unwritten", after["state"], before["state"])
	}
}
