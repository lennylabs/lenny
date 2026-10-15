// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §7.2 paths 1-7 — lenny/send_message routing.
// F-7.2.5.

// newMCPRouting builds an MCP server whose lenny/send_message handler is
// wired with the §7.2 inbox + DLQ coordinator (miniredis-backed DLQ +
// in-memory inbox), returning the inbox and DLQ so a test can assert the
// message landed in the right buffer.
func newMCPRouting(t *testing.T) (*mcp.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: dlq})
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:      store,
		Executor:   executor.NewEchoExecutor(),
		InputWaits: inputwait.NewRegistry(),
		Messaging:  coord,
		Delivery:   &dlqDelivery{coord: coord},
		Clock:      func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:     func() string { return "msg_fixed" },
		TenantID:   "acme",
	})
	return srv, store, inbox, dlq
}

// TestSendMessageRouting_InputRequiredBuffersInbox covers §7.2 path 3:
// lenny/send_message to an input_required target buffers in the inbox and
// returns a queued receipt instead of delivering to the executor.
func TestSendMessageRouting_InputRequiredBuffersInbox_spec_7_2_319(t *testing.T) {
	srv, store, inbox, _ := newMCPRouting(t)
	mkSession(t, store, "sess_ir", session.StateInputRequired, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_ir","message":"hi"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_ir"); n != 1 {
		t.Errorf("inbox depth = %d, want 1", n)
	}
}

// TestSendMessageRouting_SuspendedHoldsAndResumes_spec_7_2_path6 covers
// §7.2 path 6 on MCP: a message to a suspended target is held in the DLQ
// rather than the inbox, answers queued, and resumes the session with the
// classified row; nothing is sent to the runtime.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_SuspendedHoldsAndResumes_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, dlq, del := newMCPRoutingDelivery(t, nil)
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"later"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("inbox depth = %d, want 0", n)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_sus"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
	if len(del.resumed) != 1 || del.resumed[0].State != session.StateSuspended {
		t.Errorf("resumed = %+v, want one suspended row", del.resumed)
	}
	if del.locked != del.unlocked {
		t.Errorf("delivery lock taken %d times, released %d", del.locked, del.unlocked)
	}
}

// TestSendMessageRouting_SuspendedHoldTerminalNoResume_spec_7_2_path6 pins
// that a hold that finds the session terminal answers TARGET_TERMINAL and
// does not resume.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_SuspendedHoldTerminalNoResume_spec_7_2_path6(t *testing.T) {
	srv, store, _, _, del := newMCPRoutingDelivery(t, nil)
	del.outcome = sessionserver.HoldTerminal
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"later"}`)
	result, _ := resp["result"].(map[string]any)
	if env := readLennyErrorEnvelope(t, result); env["code"] != "TARGET_TERMINAL" {
		t.Errorf("error code = %v, want TARGET_TERMINAL", env["code"])
	}
	if len(del.resumed) != 0 {
		t.Errorf("resumed %d sessions, want none", len(del.resumed))
	}
}

// TestSendMessageRouting_SuspendedHoldErrorNoResume_spec_7_2_path6 pins that
// a hold error answers error with inbox_unavailable and does not resume, and
// that a nil delivery seam answers the same for a suspended target.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_SuspendedHoldErrorNoResume_spec_7_2_path6(t *testing.T) {
	srv, store, _, _, del := newMCPRoutingDelivery(t, nil)
	del.holdErr = errors.New("redis down")
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`))
	if got.Status != session.DeliveryStatusError || got.Reason != session.DeliveryReasonInboxUnavailable {
		t.Errorf("receipt = %+v, want error inbox_unavailable", got)
	}
	if len(del.resumed) != 0 {
		t.Errorf("resumed %d sessions, want none", len(del.resumed))
	}
}

// TestSendMessageRouting_ResumingHoldsDLQ_spec_7_2_path6 pins that a resuming
// target holds the message in the DLQ and resumes nothing.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_ResumingHoldsDLQ_spec_7_2_path6(t *testing.T) {
	srv, store, _, dlq, del := newMCPRoutingDelivery(t, nil)
	mkSession(t, store, "sess_rs", session.StateResuming, "")

	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_rs","message":"x"}`))
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_rs"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
	if len(del.resumed) != 0 {
		t.Errorf("resumed %d sessions, want none", len(del.resumed))
	}
}

// TestSendMessageRouting_RunningDeliversHeldFirst_spec_7_2_path6 pins that
// path 2 delivers the session's held messages before the new message, and
// that a held-delivery error is the error of the send.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_RunningDeliversHeldFirst_spec_7_2_path6(t *testing.T) {
	exec := &countingExecutor{}
	srv, store, _, _, del := newMCPRoutingDelivery(t, exec)
	mkSession(t, store, "sess_run", session.StateRunning, "")

	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_run","message":"hello"}`))
	if got.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", got.Status)
	}
	if len(del.delivered) != 1 || exec.sends != 1 || del.order[0] != "held" {
		t.Errorf("held deliveries %d, sends %d, order %v; want held then send", len(del.delivered), exec.sends, del.order)
	}

	del.deliverErr = errors.New("adapter gone")
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_run","message":"again"}`)
	if result, _ := resp["result"].(map[string]any); result["isError"] != true {
		t.Errorf("send after a held-delivery error = %v, want an error result", resp)
	}
	if exec.sends != 1 {
		t.Errorf("sends = %d after a held-delivery error, want 1", exec.sends)
	}
}

// TestSendMessageRouting_SealedRefusalReroutes_spec_7_2_path6 pins that the
// adapter's sealed refusal of the send routes the message again from a
// primary re-read: a row the idle suspension moved to suspended is held and
// resumed, and nothing is reported delivered.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6), 6.2 (Pod State
// Machine)
func TestSendMessageRouting_SealedRefusalReroutes_spec_7_2_path6(t *testing.T) {
	exec := &countingExecutor{}
	srv, store, _, dlq, del := newMCPRoutingDelivery(t, exec)
	mkSession(t, store, "sess_seal", session.StateRunning, "")
	exec.onSend = func() error {
		_, _ = store.Update(context.Background(), "acme", "sess_seal", func(r *sessionstore.Session) error {
			r.State = session.StateSuspended
			return nil
		})
		return &executor.SealedError{}
	}

	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_seal","message":"x"}`))
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_seal"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
	if len(del.resumed) != 1 {
		t.Errorf("resumed %d sessions, want 1", len(del.resumed))
	}
}

// TestSendMessageRouting_UnboundSendReroutes_spec_6_2 pins that a send this
// replica could not make because it holds no binding routes the message
// again when the session left running after the classification read, and
// fails when the session is still running.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 7.2
// (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_UnboundSendReroutes_spec_6_2(t *testing.T) {
	exec := &countingExecutor{}
	srv, store, _, dlq, del := newMCPRoutingDelivery(t, exec)
	mkSession(t, store, "sess_unbound", session.StateRunning, "")
	exec.onSend = func() error {
		_, _ = store.Update(context.Background(), "acme", "sess_unbound", func(r *sessionstore.Session) error {
			r.State = session.StateSuspended
			return nil
		})
		return fmt.Errorf("podexec: session sess_unbound: %w", executor.ErrNotBound)
	}
	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_unbound","message":"x"}`))
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_unbound"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
	if len(del.resumed) != 1 {
		t.Errorf("resumed %d sessions, want 1", len(del.resumed))
	}

	mkSession(t, store, "sess_running", session.StateRunning, "")
	exec.onSend = func() error { return fmt.Errorf("podexec: session sess_running: %w", executor.ErrNotBound) }
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_running","message":"x"}`)
	if result, _ := resp["result"].(map[string]any); result["isError"] != true {
		t.Fatalf("unbound send on a running session = %v, want a tool error", resp)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_running"); n != 0 {
		t.Errorf("DLQ depth = %d for a running session, want 0", n)
	}
}

// TestSendMessageRouting_SealedAfterDeliveryIsDelivered_spec_7_2_path6 pins
// that a refusal the adapter returned after the runtime received the message
// reports it delivered and routes nothing again.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_SealedAfterDeliveryIsDelivered_spec_7_2_path6(t *testing.T) {
	exec := &countingExecutor{}
	srv, store, _, dlq, _ := newMCPRoutingDelivery(t, exec)
	mkSession(t, store, "sess_seal", session.StateRunning, "")
	exec.onSend = func() error { return &executor.SealedError{Delivered: 1} }

	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_seal","message":"x"}`))
	if got.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_seal"); n != 0 {
		t.Errorf("DLQ depth = %d, want 0", n)
	}
}

// TestSendMessageRouting_RefusedPreDeliveryHoldsNothing_spec_7_2_path6 pins
// that a PreMessageDelivery refusal runs before the classification: a
// message to a suspended target that the interceptor rejects is neither held
// nor resumes the session.
//
// spec: 4.8 (Gateway Policy Engine), 7.2 (Message delivery routing — seven
// paths, path 6)
func TestSendMessageRouting_RefusedPreDeliveryHoldsNothing_spec_7_2_path6(t *testing.T) {
	srv, store, _, dlq, del := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) {
		d.Interceptors = rejectingChain(t)
	})
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`)
	if result, _ := resp["result"].(map[string]any); result["isError"] != true {
		t.Fatalf("send = %v, want a refusal", resp)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("DLQ depth = %d, want 0", n)
	}
	if len(del.resumed) != 0 || del.holds != 0 {
		t.Errorf("holds %d resumes %d, want none", del.holds, len(del.resumed))
	}
}

// TestSendMessageRouting_RecoveringBuffersDLQ covers §7.2 path 7
// (recovering row): a resume_pending target buffers in the DLQ.
func TestSendMessageRouting_RecoveringBuffersDLQ_spec_7_2_331(t *testing.T) {
	srv, store, _, dlq := newMCPRouting(t)
	mkSession(t, store, "sess_rp", session.StateResumePending, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_rp","message":"dead"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_rp"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
}

// TestSendMessageRouting_PreRunningInterSessionBuffersDLQ covers the §7.2
// dead-letter table pre-running row: a parent→child message to a not-yet
// -running child buffers in the DLQ (queued) rather than being rejected.
func TestSendMessageRouting_PreRunningInterSessionBuffersDLQ_spec_7_2_339(t *testing.T) {
	srv, store, _, dlq := newMCPRouting(t)
	mkSession(t, store, "sess_created", session.StateCreated, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_created","message":"early"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_created"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
}

// TestSendMessageRouting_RunningDelivers confirms path 2 is unchanged: a
// running target delivers to the executor and returns `delivered`.
func TestSendMessageRouting_RunningDelivers_spec_7_2_path2(t *testing.T) {
	srv, store, inbox, _ := newMCPRouting(t)
	mkSession(t, store, "sess_run", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_run","message":"hello"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", got.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_run"); n != 0 {
		t.Errorf("inbox depth = %d, want 0 (delivered)", n)
	}
}

// TestSendMessageRouting_InboxUnavailable covers the degraded path: with
// no coordinator and no delivery seam wired, a held path returns an `error`
// receipt with reason inbox_unavailable rather than delivering to a
// non-running runtime.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestSendMessageRouting_InboxUnavailable_spec_7_2(t *testing.T) {
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "msg_fixed" },
		TenantID: "acme",
		// No Messaging coordinator.
	})
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusError {
		t.Errorf("status = %q, want error", got.Status)
	}
	if got.Reason != session.DeliveryReasonInboxUnavailable {
		t.Errorf("reason = %q, want inbox_unavailable", got.Reason)
	}
}

// TestSendMessageToSuspendedAtCapIsRefused_spec_11_1 pins the resume
// admission check on lenny/send_message: a message to a suspended target
// whose check refuses answers the QUOTA_EXCEEDED tool error with the
// refusal's details and is neither held nor resumed, and a check error is
// the tool's Go error with the same outcome. The check runs before the hold.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Concurrent-session count)
func TestSendMessageToSuspendedAtCapIsRefused_spec_11_1(t *testing.T) {
	t.Run("refusal", func(t *testing.T) {
		srv, store, _, dlq, del := newMCPRoutingDelivery(t, nil)
		del.admitErr = &sessionserver.CapRefusal{Scope: sessionserver.CapScopeUser, Limit: 2, Active: 2}
		mkSession(t, store, "sess_sus", session.StateSuspended, "")

		resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`)
		result, _ := resp["result"].(map[string]any)
		env := readLennyErrorEnvelope(t, result)
		if env["code"] != "QUOTA_EXCEEDED" || env["message"] != "the user concurrent-session limit was reached" {
			t.Errorf("error = %v %v, want QUOTA_EXCEEDED with the user-scope message", env["code"], env["message"])
		}
		details, _ := env["details"].(map[string]any)
		if details["scope"] != "user" || details["limit"] != float64(2) || details["active"] != float64(2) {
			t.Errorf("details = %v, want scope user, limit 2, active 2", details)
		}
		if n, _ := dlq.Len(context.Background(), "acme", "sess_sus"); n != 0 || del.holds != 0 || len(del.resumed) != 0 {
			t.Errorf("DLQ %d holds %d resumed %d, want nothing held or resumed", n, del.holds, len(del.resumed))
		}
		if del.admits != 1 || del.locked != del.unlocked {
			t.Errorf("admits %d, lock %d/%d; want one check and the lock released", del.admits, del.locked, del.unlocked)
		}
	})
	t.Run("check error", func(t *testing.T) {
		srv, store, _, dlq, del := newMCPRoutingDelivery(t, nil)
		del.admitErr = errors.New("count failed")
		mkSession(t, store, "sess_sus", session.StateSuspended, "")

		resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`)
		if !toolFailed(resp) {
			t.Fatalf("response = %+v, want the tool's error", resp)
		}
		if n, _ := dlq.Len(context.Background(), "acme", "sess_sus"); n != 0 || del.holds != 0 || len(del.resumed) != 0 {
			t.Errorf("DLQ %d holds %d resumed %d, want nothing held or resumed", n, del.holds, len(del.resumed))
		}
	})
	t.Run("admitted holds and resumes", func(t *testing.T) {
		srv, store, _, dlq, del := newMCPRoutingDelivery(t, nil)
		mkSession(t, store, "sess_sus", session.StateSuspended, "")
		got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"x"}`))
		if got.Status != session.DeliveryStatusQueued {
			t.Fatalf("receipt = %+v, want queued", got)
		}
		if n, _ := dlq.Len(context.Background(), "acme", "sess_sus"); n != 1 || len(del.resumed) != 1 {
			t.Errorf("DLQ %d resumed %d, want 1 and 1", n, len(del.resumed))
		}
		if len(del.order) == 0 || del.order[0] != "admit" {
			t.Errorf("order = %v, want the check first", del.order)
		}
	})
}

// toolFailed reports whether resp carries a JSON-RPC error or an isError
// result.
func toolFailed(resp map[string]any) bool {
	if _, ok := resp["error"]; ok {
		return true
	}
	result, _ := resp["result"].(map[string]any)
	return result["isError"] == true
}

// TestSendMessageSealedFallbackAtCapIsHeld_spec_11_1 pins that the resume
// admission check gates only a row classified suspended. A message the
// adapter refuses as sealed twice while the row stays running takes the
// held-branch fallback: it is held and answers queued even when every cap
// would refuse, because the running session already counts toward the caps.
// The check never runs on that path.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §11.1
// (Resume admission)
func TestSendMessageSealedFallbackAtCapIsHeld_spec_11_1(t *testing.T) {
	exec := &countingExecutor{onSend: func() error { return &executor.SealedError{} }}
	srv, store, _, dlq, del := newMCPRoutingDelivery(t, exec)
	del.admitErr = &sessionserver.CapRefusal{Scope: sessionserver.CapScopeTenant, Limit: 1, Active: 1}
	mkSession(t, store, "sess_seal", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_seal","message":"x"}`)
	if toolFailed(resp) {
		t.Fatalf("response = %+v, want a queued receipt", resp)
	}
	if got := receiptFromResult(t, resp); got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_seal"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
	if del.admits != 0 {
		t.Errorf("admission checks = %d, want none on the sealed fallback", del.admits)
	}
	if len(del.resumed) != 1 {
		t.Errorf("resumed %d sessions, want 1", len(del.resumed))
	}
}
