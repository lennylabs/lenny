// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// specDeliveryTimeout is §7.2 path 2's default delivery timeout: the window in
// which the adapter must acknowledge the stdin write before the gateway treats
// the message as undeliverable on the direct-delivery path. The spec calls the
// value configurable with this default; a deployment that shortens it shortens
// the window this test bounds.
//
// spec: §7.2 path 2 ("the adapter acknowledges the write within the
// configurable delivery timeout (default: 30 seconds)").
const specDeliveryTimeout = 30 * time.Second

// neverConsumingExecutor models a runtime that never consumes the message
// written to its stdin pipe: Send blocks until the caller's context is
// cancelled and never acknowledges. It is the executor-level stand-in for an
// adapter that never reports `ready_for_input` and never acknowledges the
// stdin write, which the always-ready in-process echo executor cannot express.
type neverConsumingExecutor struct{}

func (neverConsumingExecutor) Send(ctx context.Context, _ string, _ []executor.Message) (executor.Response, error) {
	<-ctx.Done()
	return executor.Response{}, ctx.Err()
}

func (neverConsumingExecutor) Close(context.Context, string) error { return nil }

func seedRunningSessionRow(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", UserID: "alice", RuntimeRef: "echo",
		State: session.StateRunning, PodAssignment: "pod-slow",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed running %s: %v", id, err)
	}
}

// TestDirectDeliveryTimeoutFallsThroughToQueuedInbox pins the §7.2 path-2
// delivery-timeout fallback: a message routed to a `running` session whose
// runtime never consumes it from the stdin pipe must not hang and must not
// report `delivered`. Once the delivery timeout elapses the gateway treats the
// message as undeliverable for the direct-delivery path, buffers it in the
// session inbox (path 5 behavior), and returns a `queued` delivery receipt.
//
// The executor blocks forever, so the assertion is two-part: the request must
// return within the delivery timeout plus slack, and the receipt it returns
// must be `queued` with the message preserved in the inbox.
//
// spec: 7.2 (path 2 line 320 — "If the runtime does not consume the message
// within this timeout, the gateway treats it as undeliverable for this path and
// falls through to inbox buffering (path 5 behavior); in this fallback case the
// delivery receipt status is `queued`, not `delivered`."), 15.4.1 (the
// `delivery` field's `queued` receipt on an unconfirmed stdin consumption)
//
// diagnosis: a failure means the gateway has no bound on direct delivery to the
// runtime — a runtime that never consumes the message either hangs the client
// request indefinitely or reports `delivered` for a message the runtime never
// read, so the sender believes a message was consumed that was in fact lost.
func TestDirectDeliveryTimeoutFallsThroughToQueuedInbox(t *testing.T) {
	t.Skip("the §7.2 path-2 delivery timeout and the §4.7 adapter stdin-consumption acknowledgement are unbuilt; the gateway delivers through a synchronous executor.Send with no delivery deadline")

	store := memstore.New()
	seedRunningSessionRow(t, store, "s-no-consume")
	srv, inbox := inboxServer(t, store, neverConsumingExecutor{})

	body, _ := json.Marshal(sessionserverMessage("are you there"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/s-no-consume/messages", strings.NewReader(string(body))).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Handler().ServeHTTP(rr, req)
	}()

	// The delivery timeout bounds the direct-delivery attempt; the slack
	// covers the inbox enqueue and the receipt render that follow it.
	select {
	case <-done:
	case <-time.After(specDeliveryTimeout + 15*time.Second):
		cancel()
		t.Fatalf("POST /messages did not return within the delivery timeout: a runtime that never consumes the message must fall through to inbox buffering rather than block the request")
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s (an unconsumed message falls through to a 200 queued receipt)", rr.Code, rr.Body.String())
	}
	var resp sessionserver.MessageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.DeliveryReceipt.Status != session.DeliveryStatusQueued {
		t.Errorf("status = %q, want queued (the runtime never acknowledged the stdin write within the delivery timeout)", resp.DeliveryReceipt.Status)
	}
	if n, _ := inbox.Len(context.Background(), "acme", "s-no-consume"); n != 1 {
		t.Errorf("inbox depth = %d, want 1 (the undelivered message buffers for FIFO redelivery on the next ready_for_input)", n)
	}
	row, _ := store.Get(context.Background(), "acme", "s-no-consume")
	if row.State != session.StateRunning {
		t.Errorf("state = %q, want running (the delivery timeout buffers the message; it does not change session state)", row.State)
	}
}

func sessionserverMessage(text string) sessionserver.MessageRequest {
	return sessionserver.MessageRequest{
		Messages: []sessionserver.MessagePayload{{
			Role:    "user",
			Content: sessionrecord.MessageContentFromText(text),
		}},
	}
}
