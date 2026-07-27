// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
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
)

// spec: §7.2 paths 1-7 (lines 313-331) — lenny/send_message routing.
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

// TestSendMessageRouting_SuspendedBuffersInbox covers §7.2 path 6.
func TestSendMessageRouting_SuspendedBuffersInbox_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, _ := newMCPRouting(t)
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_sus","message":"later"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 1 {
		t.Errorf("inbox depth = %d, want 1", n)
	}
}

// stubHeldPodResumer stands in for the session server's coordinator-side
// §7.2 path-6 resume. It records the call and either flips the row to
// running (the pod-held resume) or returns a fault so the handler's
// fail-closed buffering runs.
type stubHeldPodResumer struct {
	store sessionstore.Store
	err   error
	calls int
}

func (s *stubHeldPodResumer) ResumeHeldPodService(ctx context.Context, tenantID, id string) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	_, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		row.State = session.StateRunning
		return nil
	})
	return err
}

// newMCPRoutingWithResumer builds the routing harness with a §7.2 path-6
// resume primitive wired, mirroring the production wiring where the MCP
// surface and the REST surface share the session server's resume.
func newMCPRoutingWithResumer(t *testing.T, resumeErr error) (*mcp.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *stubHeldPodResumer) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: sessioninbox.NewDLQ(rc, 10)})
	store := memstore.New()
	resumer := &stubHeldPodResumer{store: store, err: resumeErr}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:          store,
		Executor:       executor.NewEchoExecutor(),
		InputWaits:     inputwait.NewRegistry(),
		Messaging:      coord,
		HeldPodResumer: resumer,
		Clock:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:         func() string { return "msg_fixed" },
		TenantID:       "acme",
	})
	return srv, store, inbox, resumer
}

// spec: §7.2 path 6 (lines 326-330) — "Pod still held: The gateway
// atomically resumes the session (`suspended → running`) and delivers the
// message ... The delivery receipt is `delivered` on successful
// resume-and-deliver", and path 6 "applies uniformly to all message
// sources: external client (`POST /v1/sessions/{id}/messages`) and
// inter-session via `lenny/send_message`".
func TestSendMessageRouting_SuspendedImmediateResumesAndDelivers_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, resumer := newMCPRoutingWithResumer(t, nil)
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_sus","message":"now","delivery":"immediate"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", got.Status)
	}
	if resumer.calls != 1 {
		t.Errorf("resume calls = %d, want 1", resumer.calls)
	}
	row, err := store.Get(context.Background(), "acme", "sess_sus")
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("target state = %q, want running", row.State)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("inbox depth = %d, want 0 (the message was delivered, not buffered)", n)
	}
}

// spec: §7.2 path 6 (line 330) — a resume the coordinating replica cannot
// perform "falls back to inbox buffering with a `queued` delivery receipt
// status — the message is not silently dropped."
func TestSendMessageRouting_SuspendedImmediateResumeFaultBuffers_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, resumer := newMCPRoutingWithResumer(t, errors.New("resume write failed"))
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_sus","message":"now","delivery":"immediate"}`)
	got := receiptFromResult(t, resp)
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if resumer.calls != 1 {
		t.Errorf("resume calls = %d, want 1", resumer.calls)
	}
	row, err := store.Get(context.Background(), "acme", "sess_sus")
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if row.State != session.StateSuspended {
		t.Errorf("target state = %q, want suspended (the resume did not happen)", row.State)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 1 {
		t.Errorf("inbox depth = %d, want 1", n)
	}
}

// spec: §15.4 (`delivery` closed enum) — "No other values are valid. The
// gateway rejects unknown `delivery` values with `400
// INVALID_DELIVERY_VALUE`."
func TestSendMessage_InvalidDeliveryValueRejected_spec_15_4(t *testing.T) {
	srv, store, inbox, resumer := newMCPRoutingWithResumer(t, nil)
	mkSession(t, store, "sess_sus", session.StateSuspended, "")

	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_sus","message":"now","delivery":"asap"}`)
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("no tool result: %+v", resp)
	}
	envelope := readLennyErrorEnvelope(t, result)
	if envelope["code"] != "INVALID_DELIVERY_VALUE" {
		t.Errorf("code = %v, want INVALID_DELIVERY_VALUE", envelope["code"])
	}
	// The rejection precedes routing: nothing is buffered and no resume runs.
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("inbox depth = %d, want 0", n)
	}
	if resumer.calls != 0 {
		t.Errorf("resume calls = %d, want 0", resumer.calls)
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
// no coordinator wired, a buffered path returns an `error` receipt with
// reason inbox_unavailable rather than delivering to a non-running runtime.
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
