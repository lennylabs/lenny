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
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
)

// spec: §7.2 paths 1-7 (lines 313-331) — message-delivery routing.
// F-7.2.5.

// newRoutingServer builds a Server wired with the §7.2 inbox + DLQ
// coordinator (miniredis-backed DLQ + in-memory inbox) so the buffered
// paths (3, 6, 7) exercise the real machinery. It returns the inbox and
// DLQ so tests can assert the message landed in the right buffer.
func newRoutingServer(t *testing.T) (*sessionserver.Server, sessionstore.Store, *sessioninbox.MemoryInbox, *sessioninbox.DLQ) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: dlq})
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Messaging:   coord,
	})
	return srv, store, inbox, dlq
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
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "while-blocked"}},
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

// TestMessageRouting_SuspendedBuffersInbox covers §7.2 path 6: a
// suspended target buffers the message in the inbox with a `queued`
// receipt (the pod-held resume-and-deliver is a pod-adapter behaviour).
func TestMessageRouting_SuspendedBuffersInbox_spec_7_2_path6(t *testing.T) {
	srv, store, inbox, _ := newRoutingServer(t)
	seedSessionState(t, store, "sess_sus", session.StateSuspended)

	rr := sendMessageRequest(t, srv.Handler(), "sess_sus", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "for-later"}},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued", resp.DeliveryReceipt.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "sess_sus"); n != 1 {
		t.Errorf("inbox depth = %d, want 1", n)
	}
}

// TestMessageRouting_RecoveringBuffersDLQ covers §7.2 path 7: a
// recovering (resume_pending) target buffers the message in the DLQ with
// a `queued` receipt.
func TestMessageRouting_RecoveringBuffersDLQ_spec_7_2_331(t *testing.T) {
	srv, store, _, dlq := newRoutingServer(t)
	seedSessionState(t, store, "sess_rp", session.StateResumePending)

	rr := sendMessageRequest(t, srv.Handler(), "sess_rp", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "dead-letter"}},
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
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "hello"}},
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
// path: with no messaging coordinator wired, a buffered path returns
// 503 INBOX_UNAVAILABLE rather than silently dropping the message.
func TestMessageRouting_InboxUnavailableWhenUnwired_spec_7_2(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Executor: executor.NewEchoExecutor(),
		// No Messaging coordinator.
	})
	seedSessionState(t, store, "sess_nb", session.StateSuspended)

	rr := sendMessageRequest(t, srv.Handler(), "sess_nb", sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{Role: "user", Content: "x"}},
	})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}
