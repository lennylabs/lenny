// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// spec: §7.2 paths 1-7 — message-delivery routing.
// F-7.2.5.

// newRoutingServer builds a Server wired with the §7.2 inbox + DLQ
// coordinator (miniredis-backed DLQ + in-memory inbox) so the buffered
// paths (3, 6, 7) exercise the real machinery. It returns the inbox and
// DLQ so tests can assert the message landed in the right buffer.
func newRoutingServer(t *testing.T) (*sessionserver.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ) {
	t.Helper()
	return newRoutingServerWith(t, nil)
}

// newRoutingServerWith is newRoutingServer with an Options mutator.
func newRoutingServerWith(t *testing.T, mutate func(*sessionserver.Options)) (*sessionserver.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: dlq})
	store := memstore.New()
	opts := sessionserver.Options{
		DevTenantHeader: true,
		Executor:        executor.NewEchoExecutor(),
		Transcripts:     transcriptstore.NewMemory(),
		Messaging:       coord,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return sessionserver.New(store, opts), store, inbox, dlq
}

func seedSessionState(t *testing.T, store sessionstore.Store, id string, state session.State) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: state, RuntimeRef: "chat",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestMessageRouting_InputRequiredBuffersInbox covers §7.2 path 3: an
// `input_required` target buffers the message in the inbox and returns a
// `queued` receipt rather than delivering it.
func TestMessageRouting_InputRequiredBuffersInbox_spec_7_2_319(t *testing.T) {
	srv, store, inbox, _ := newRoutingServer(t)
	seedSessionState(t, store, "sess_ir", session.StateInputRequired)

	rr := sendMessageRequest(t, srv.Handler(), "sess_ir", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("while-blocked")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", resp.DeliveryReceipt.Status)
	}
	if len(resp.Output) != 0 {
		t.Errorf("buffered message must not produce executor output, got %+v", resp.Output)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_ir"); n != 1 {
		t.Errorf("inbox depth = %d, want 1 (message buffered)", n)
	}
}

// TestMessageRouting_SuspendedResumesAndDelivers_spec_7_2_path6 covers §7.2
// path 6 for a suspended session that holds its pod, with or without
// `delivery: "immediate"`: the coordinating replica resumes it and delivers
// the message, and nothing waits in the inbox.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestMessageRouting_SuspendedResumesAndDelivers_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, _ := newRoutingServer(t)
	seedSessionState(t, store, "sess_sus", session.StateSuspended)

	rr := sendMessageRequest(t, srv.Handler(), "sess_sus", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("for-later")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", resp.DeliveryReceipt.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("inbox depth = %d, want 0", n)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_sus"); row.State != session.StateRunning {
		t.Errorf("state = %q, want running", row.State)
	}
}

// TestMessageRouting_SuspendedWithPendingInputWaitResumes_spec_7_2_path6
// pins that §7.2 path 6 wins for a suspended session whose runtime was
// interrupted while blocked in lenny/request_input: the wait stays in the
// registry, yet a message without a matching inReplyTo resumes the
// session and is delivered, and nothing waits in the inbox.
//
// spec: 7.2 (Message delivery routing — seven paths, path 6)
func TestMessageRouting_SuspendedWithPendingInputWaitResumes_spec_7_2_path6(t *testing.T) {
	waits := inputwait.NewRegistry()
	srv, store, inbox, _ := newRoutingServerWith(t, func(o *sessionserver.Options) { o.InputWaits = waits })
	seedSessionState(t, store, "sess_sus", session.StateSuspended)
	if _, err := waits.Register("sess_sus", "req_blocked", nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	rr := sendMessageRequest(t, srv.Handler(), "sess_sus", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("after-interrupt")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 0 {
		t.Errorf("inbox depth = %d, want 0 (path 6 never enqueues to the inbox)", n)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_sus"); row.State == session.StateSuspended {
		t.Errorf("state = %q, want the session moved out of suspended", row.State)
	}
}

// TestMessageRouting_RecoveringBuffersDLQ covers §7.2 path 7: a
// recovering (resume_pending) target buffers the message in the DLQ with
// a `queued` receipt.
func TestMessageRouting_RecoveringBuffersDLQ_spec_7_2_331(t *testing.T) {
	srv, store, _, dlq := newRoutingServer(t)
	seedSessionState(t, store, "sess_rp", session.StateResumePending)

	rr := sendMessageRequest(t, srv.Handler(), "sess_rp", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("dead-letter")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", resp.DeliveryReceipt.Status)
	}
	if n, _ := dlq.Len(context.Background(), "acme", "sess_rp"); n != 1 {
		t.Errorf("DLQ depth = %d, want 1", n)
	}
}

// TestMessageRouting_RunningDelivers confirms §7.2 path 2 is unchanged:
// a running target with no concurrent input_required delivers to the
// executor and returns `delivered`.
func TestMessageRouting_RunningDelivers_spec_7_2_path2(t *testing.T) {
	srv, store, inbox, _ := newRoutingServer(t)
	seedSessionState(t, store, "sess_run", session.StateRunning)

	rr := sendMessageRequest(t, srv.Handler(), "sess_run", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hello")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("status = %q, want delivered", resp.DeliveryReceipt.Status)
	}
	if len(resp.Output) == 0 {
		t.Error("running delivery must produce executor output")
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_run"); n != 0 {
		t.Errorf("inbox depth = %d, want 0 (delivered, not buffered)", n)
	}
}

// TestMessageRouting_InboxUnavailableWhenUnwired covers the degraded
// path: with no messaging coordinator wired, a buffered path returns a
// 200 `delivery_receipt` carrying status:"error"/reason:"inbox_unavailable"
// rather than the retired 503 INBOX_UNAVAILABLE envelope, matching the
// MCP send_message receipt form per the §15.2.1 REST/MCP parity contract.
// spec: §15.4 (inbox_unavailable receipt), §15.2.1 (REST/MCP parity).
// F-MS4.
func TestMessageRouting_InboxUnavailableWhenUnwired_spec_15_4(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		Executor:        executor.NewEchoExecutor(),
		// No Messaging coordinator.
	})
	seedSessionState(t, store, "sess_nb", session.StateResumePending)

	rr := sendMessageRequest(t, srv.Handler(), "sess_nb", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("x")}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusError {
		t.Errorf("status = %q, want error", resp.DeliveryReceipt.Status)
	}
	if resp.DeliveryReceipt.Reason != session.DeliveryReasonInboxUnavailable {
		t.Errorf("reason = %q, want inbox_unavailable", resp.DeliveryReceipt.Reason)
	}
}
