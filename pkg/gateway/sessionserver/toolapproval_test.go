// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

func fixedNow() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) }

func seedSession(t *testing.T, store *memstore.Store) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_1", TenantID: "acme", UserID: "alice",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// TestToolApprovalGateProducer_spec_7_2_9 asserts the §7.2 producer half:
// AwaitApproval records a KindToolUse interaction (directed at the
// session's owning user) and publishes a `tool_use_requested(tool_call_id,
// tool, args)` SSE event before blocking. F-7.2.9.
func TestToolApprovalGateProducer_spec_7_2_9(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	inter := interactionstore.NewMemory()
	bus := sessionevents.NewBus(64)
	waits := toolapproval.NewRegistry()

	sub, err := bus.SubscribeForTenant("acme", "sess_1", 0, 8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	gate := sessionserver.NewToolApprovalGate(store, inter, bus, waits, fixedNow, 0)

	done := make(chan executor.ApprovalDecision, 1)
	go func() {
		d, _ := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{
			ID: "tc-1", Name: "lenny/deploy", Arguments: json.RawMessage(`{"target":"prod"}`),
		})
		done <- d
	}()

	// The SSE event proves the producer published before blocking.
	select {
	case ev := <-sub.Events():
		if ev.Type != "tool_use_requested" {
			t.Fatalf("event type = %q, want tool_use_requested", ev.Type)
		}
		var payload struct {
			ToolCallID string          `json:"tool_call_id"`
			Tool       string          `json:"tool"`
			Args       json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("event data not JSON: %v", err)
		}
		if payload.ToolCallID != "tc-1" || payload.Tool != "lenny/deploy" {
			t.Errorf("payload = %+v, want tc-1/lenny/deploy", payload)
		}
		if string(payload.Args) != `{"target":"prod"}` {
			t.Errorf("payload args = %s, want the forwarded args", payload.Args)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_use_requested event published within 2s")
	}

	// The interaction must be recorded, pending, directed at the owner.
	got, err := inter.Get(context.Background(), "acme", "sess_1", "alice", "tc-1")
	if err != nil {
		t.Fatalf("interaction not recorded: %v", err)
	}
	if got.Kind != interactionstore.KindToolUse || got.Phase != interactionstore.PhasePending {
		t.Errorf("interaction = {Kind:%q Phase:%q}, want tool_use/pending", got.Kind, got.Phase)
	}
	if got.Detail["tool"] != "lenny/deploy" {
		t.Errorf("interaction detail tool = %v, want lenny/deploy", got.Detail["tool"])
	}

	// Release the blocked goroutine.
	_ = waits.Resolve("sess_1", "tc-1", toolapproval.Decision{Approved: true})
	<-done
}

// TestToolApprovalGateApprove_spec_7_2 asserts the gate returns the
// approve verdict the registry delivers. F-7.2.18.
func TestToolApprovalGateApprove_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	waits := toolapproval.NewRegistry()
	gate := sessionserver.NewToolApprovalGate(store, interactionstore.NewMemory(), sessionevents.NewBus(8), waits, fixedNow, 0)

	out := make(chan executor.ApprovalDecision, 1)
	go func() {
		d, _ := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
		out <- d
	}()
	waitForPending(t, waits, "sess_1", "tc-1")
	_ = waits.Resolve("sess_1", "tc-1", toolapproval.Decision{Approved: true})
	if d := <-out; !d.Approved {
		t.Errorf("decision = %+v, want approved", d)
	}
}

// TestToolApprovalGateDeny_spec_7_2 asserts the gate relays the deny
// reason. F-7.2.18.
func TestToolApprovalGateDeny_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	waits := toolapproval.NewRegistry()
	gate := sessionserver.NewToolApprovalGate(store, interactionstore.NewMemory(), sessionevents.NewBus(8), waits, fixedNow, 0)

	out := make(chan executor.ApprovalDecision, 1)
	go func() {
		d, _ := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
		out <- d
	}()
	waitForPending(t, waits, "sess_1", "tc-1")
	_ = waits.Resolve("sess_1", "tc-1", toolapproval.Decision{Approved: false, Reason: "unsafe"})
	d := <-out
	if d.Approved || d.Reason != "unsafe" {
		t.Errorf("decision = %+v, want denied with reason unsafe", d)
	}
}

// TestToolApprovalGateTimeout_spec_7_2 asserts an unresolved approval
// times out into an implicit denial. F-7.2.18.
func TestToolApprovalGateTimeout_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	gate := sessionserver.NewToolApprovalGate(store, interactionstore.NewMemory(), sessionevents.NewBus(8), toolapproval.NewRegistry(), fixedNow, 20*time.Millisecond)

	d, err := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
	if err != nil {
		t.Fatalf("AwaitApproval: %v", err)
	}
	if d.Approved || d.Reason != "approval_timeout" {
		t.Errorf("decision = %+v, want denied with approval_timeout", d)
	}
}

// TestToolApprovalGateContextCancel_spec_7_2 asserts a cancelled request
// context aborts the wait with the context error. F-7.2.18.
func TestToolApprovalGateContextCancel_spec_7_2(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	gate := sessionserver.NewToolApprovalGate(store, interactionstore.NewMemory(), sessionevents.NewBus(8), toolapproval.NewRegistry(), fixedNow, 0)

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan error, 1)
	go func() {
		_, err := gate.AwaitApproval(ctx, "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
		out <- err
	}()
	cancel()
	select {
	case err := <-out:
		if err == nil {
			t.Error("AwaitApproval returned nil error on a cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitApproval did not return after context cancel")
	}
}

// TestToolUseApproveUnblocksGate_spec_7_2_18 is the end-to-end unblock:
// the REST approve endpoint delivers the verdict through the Server's
// shared waiter registry to a concurrently-blocked AwaitApproval call.
// F-7.2.18.
func TestToolUseApproveUnblocksGate_spec_7_2_18(t *testing.T) {
	store := memstore.New()
	seedSession(t, store)
	inter := interactionstore.NewMemory()
	bus := sessionevents.NewBus(8)
	waits := toolapproval.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{Interactions: inter, ToolApprovalWaits: waits})
	gate := sessionserver.NewToolApprovalGate(store, inter, bus, waits, fixedNow, 0)

	out := make(chan executor.ApprovalDecision, 1)
	go func() {
		d, _ := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
		out <- d
	}()
	waitForPending(t, waits, "sess_1", "tc-1")

	req := asAlice(httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_1/tool-use/tc-1/approve", nil))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve: %d, body=%s", rr.Code, rr.Body.String())
	}
	select {
	case d := <-out:
		if !d.Approved {
			t.Errorf("gate decision = %+v, want approved (REST unblock)", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("REST approve did not unblock the gate within 2s")
	}
	// The interaction phase is also the authoritative record.
	got, _ := inter.Get(context.Background(), "acme", "sess_1", "alice", "tc-1")
	if got.Phase != interactionstore.PhaseApproved {
		t.Errorf("interaction phase = %q, want approved", got.Phase)
	}
}

// resolveInStore writes the resolution directly onto the shared
// interaction store, the way a non-coordinator replica's approve/deny/
// dismiss endpoint would, without touching this gate's in-process waiter
// registry. It models the cross-replica path the store poll must wake on.
func resolveInStore(t *testing.T, store interactionstore.Store, sessionID, callID string, mutate func(*interactionstore.Interaction)) {
	t.Helper()
	if _, err := store.Resolve(context.Background(), "acme", sessionID, "alice", callID,
		func(in *interactionstore.Interaction) error {
			mutate(in)
			return nil
		}); err != nil {
		t.Fatalf("store-side resolve: %v", err)
	}
}

// waitForPendingInteraction blocks until the gate has recorded the pending
// interaction, so a store-side resolve targets an existing pending row.
func waitForPendingInteraction(t *testing.T, store interactionstore.Store, sessionID, callID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := store.Get(context.Background(), "acme", sessionID, "alice", callID)
		if err == nil && got.Phase == interactionstore.PhasePending {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending interaction for (%s,%s) never recorded", sessionID, callID)
}

// runStoreOnlyAwait drives AwaitApproval on a gate whose local registry is
// never resolved, then applies mutate to the shared interaction store once
// the pending interaction is recorded. It returns the decision the
// store-poll fallback produced. A long timeout means a regressed gate (no
// store poll) hangs the test rather than masking the failure as a timeout
// denial.
func runStoreOnlyAwait(t *testing.T, mutate func(*interactionstore.Interaction)) executor.ApprovalDecision {
	t.Helper()
	store := memstore.New()
	seedSession(t, store)
	inter := interactionstore.NewMemory()
	gate := sessionserver.NewToolApprovalGate(store, inter, sessionevents.NewBus(8), toolapproval.NewRegistry(), fixedNow, 10*time.Second)

	out := make(chan executor.ApprovalDecision, 1)
	go func() {
		d, _ := gate.AwaitApproval(context.Background(), "acme", "sess_1", executor.PendingToolCall{ID: "tc-1", Name: "x"})
		out <- d
	}()
	waitForPendingInteraction(t, inter, "sess_1", "tc-1")
	resolveInStore(t, inter, "sess_1", "tc-1", mutate)

	select {
	case d := <-out:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("store-only resolution did not wake the gate within 2s (no store-poll fallback — F-IA1)")
		return executor.ApprovalDecision{}
	}
}

// TestToolApprovalGateStorePollApprove_spec_7_2 asserts the gate wakes from
// a PhaseApproved that landed only on the shared interaction store, with no
// local registry resolution — the F-IA1 cross-replica approve path.
// spec: §7.2 (cross-replica approve wake), F-IA1.
func TestToolApprovalGateStorePollApprove_spec_7_2(t *testing.T) {
	d := runStoreOnlyAwait(t, func(in *interactionstore.Interaction) {
		in.Phase = interactionstore.PhaseApproved
	})
	if !d.Approved {
		t.Errorf("decision = %+v, want approved from the store resolution", d)
	}
}

// TestToolApprovalGateStorePollDeny_spec_7_2 asserts the gate wakes from a
// PhaseDenied that landed only on the shared store and relays the persisted
// deny reason — the F-IA1 cross-replica deny path.
// spec: §7.2 (cross-replica deny wake), F-IA1.
func TestToolApprovalGateStorePollDeny_spec_7_2(t *testing.T) {
	d := runStoreOnlyAwait(t, func(in *interactionstore.Interaction) {
		in.Phase = interactionstore.PhaseDenied
		in.Reason = "unsafe"
	})
	if d.Approved || d.Reason != "unsafe" {
		t.Errorf("decision = %+v, want denied with persisted reason %q", d, "unsafe")
	}
}

// TestToolApprovalGateStorePollDismiss_spec_7_2 asserts a PhaseDismissed
// resolution (a §11.4 user revocation or the resolver's timeout sweep) that
// landed only on the shared store is treated as a denial so the runtime's
// tool call does not execute. Fail closed.
// spec: §7.2 (dismissal is a denial), §11.4, F-IA1.
func TestToolApprovalGateStorePollDismiss_spec_7_2(t *testing.T) {
	d := runStoreOnlyAwait(t, func(in *interactionstore.Interaction) {
		in.Phase = interactionstore.PhaseDismissed
		in.Reason = "revoked"
	})
	if d.Approved {
		t.Errorf("decision = %+v, want denied (dismissal must not approve)", d)
	}
	if d.Reason != "revoked" {
		t.Errorf("dismiss reason = %q, want the persisted %q", d.Reason, "revoked")
	}
}

func waitForPending(t *testing.T, r *toolapproval.Registry, sessionID, toolCallID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.Pending(sessionID, toolCallID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiter for (%s,%s) never registered", sessionID, toolCallID)
}
