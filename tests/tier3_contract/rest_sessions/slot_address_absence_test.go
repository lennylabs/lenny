// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the client-facing absence of a slot address.
// A client addresses a session rather than a slot: every session is bound
// to a slot on every pod and a session-mode slot's identifier is its
// session's identifier, so no client-facing payload carries a separate slot
// key. These tests pin that on the §15.1 message request body, on the §7.2
// tool-approval interaction detail, and on the §7.2 tool_use_requested SSE
// payload.

package rest_sessions_test

import (
	"context"
	"encoding/json"
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

// spec: §7.2 (message dispatch); §15.4 (MessageEnvelope)
// diagnosis: the gateway either rejected a message body carrying a stale
// slotId key or let that key steer delivery. The key is not part of the
// client contract: it does not deserialize onto the payload, and the
// session named by the route is the session the message reaches.
func TestMessageBodyIgnoresStaleSlotAddress_spec_7_2(t *testing.T) {
	store := memstore.New()
	rec := &recordingExecutor{Executor: executor.NewEchoExecutor()}
	srv := sessionserver.New(store, sessionserver.Options{Executor: rec})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	id := createSession(t, ts)
	for _, step := range []string{"finalize", "start"} {
		if resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/"+step, nil); resp.StatusCode != 200 {
			t.Fatalf("POST /%s: status = %d, body=%v", step, resp.StatusCode, body)
		}
	}

	resp, body := do(t, ts, "POST", "/v1/sessions/"+id+"/messages", map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "hello", "slotId": "slot-from-an-older-client"},
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200 (a stale slotId key is ignored, not rejected); body=%v",
			resp.StatusCode, body)
	}
	if _, ok := body["deliveryReceipt"].(map[string]any); !ok {
		t.Fatalf("response carries no deliveryReceipt: %v", body)
	}
	if len(rec.dispatched) != 1 || rec.dispatched[0] != id {
		t.Errorf("executor dispatched for %v, want one dispatch to %q (the session the route names)",
			rec.dispatched, id)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal response: %v", err)
	}
	if containsKey(t, raw, "slotId") {
		t.Errorf("message response echoes a slotId key: %s", raw)
	}
}

// spec: §4.1 (message scope); §7.2 (tool-use approval)
// diagnosis: the tool-approval interaction detail or the
// tool_use_requested SSE payload carries a slot address. Both enclosing
// objects already name the session (the interaction's SessionID, and the
// per-session SSE stream), so a slot key duplicates an identifier the
// client already holds.
func TestToolApprovalPayloadsCarryNoSlotAddress_spec_4_1(t *testing.T) {
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_tool_1", TenantID: "acme", UserID: "alice@acme.com",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	inter := interactionstore.NewMemory()
	bus := sessionevents.NewBus(64)
	waits := toolapproval.NewRegistry()

	sub, err := bus.SubscribeForTenant("acme", "sess_tool_1", 0, 8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	now := func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) }
	gate := sessionserver.NewToolApprovalGate(store, inter, bus, waits, now, 250*time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = gate.AwaitApproval(context.Background(), "acme", "sess_tool_1",
			executor.PendingToolCall{
				ID:        "tc-1",
				Name:      "lenny/deploy",
				Arguments: json.RawMessage(`{"target":"prod"}`),
			})
	}()

	select {
	case ev := <-sub.Events():
		if ev.Type != "tool_use_requested" {
			t.Fatalf("event type = %q, want tool_use_requested", ev.Type)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("decode tool_use_requested payload: %v (data=%s)", err, ev.Data)
		}
		// The key set is pinned exhaustively so no address key can be
		// reintroduced alongside the three §7.2 members.
		wantKeys := map[string]bool{"tool_call_id": true, "tool": true, "args": true}
		for k := range payload {
			if !wantKeys[k] {
				t.Errorf("tool_use_requested payload carries unexpected key %q: %s", k, ev.Data)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_use_requested event published")
	}

	pending, err := inter.Get(context.Background(), "acme", "sess_tool_1", "alice@acme.com", "tc-1")
	if err != nil {
		t.Fatalf("read recorded interaction: %v", err)
	}
	if pending.SessionID != "sess_tool_1" {
		t.Errorf("interaction.SessionID = %q, want sess_tool_1 (the enclosing object names the session)",
			pending.SessionID)
	}
	wantDetail := map[string]bool{"tool": true, "args": true}
	for k := range pending.Detail {
		if !wantDetail[k] {
			t.Errorf("interaction detail carries unexpected key %q: %v", k, pending.Detail)
		}
	}
	<-done
}

// containsKey reports whether the JSON document raw carries key anywhere
// in its object tree.
func containsKey(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode JSON while scanning for %q: %v (doc=%s)", key, err, raw)
	}
	return jsonHasKey(v, key)
}

func jsonHasKey(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if k == key || jsonHasKey(sub, key) {
				return true
			}
		}
	case []any:
		for _, sub := range t {
			if jsonHasKey(sub, key) {
				return true
			}
		}
	}
	return false
}

// recordingExecutor records the session identifier each dispatch is
// addressed to so a test can assert that the route's session, rather than
// any body field, decides where a message lands.
type recordingExecutor struct {
	executor.Executor
	dispatched []string
}

func (r *recordingExecutor) Send(ctx context.Context, sessionID string, messages []executor.Message) (executor.Response, error) {
	r.dispatched = append(r.dispatched, sessionID)
	return r.Executor.Send(ctx, sessionID, messages)
}
