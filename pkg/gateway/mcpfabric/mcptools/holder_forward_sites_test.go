// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// fakeForwarder is a HolderForwarder that answers each call with a fixed
// outcome, result, and error, and records the calls it received.
type fakeForwarder struct {
	mu      sync.Mutex
	outcome sessionserver.ForwardOutcome
	result  mcp.ToolResult
	err     error
	calls   []forwardedCall
}

type forwardedCall struct {
	kind sessionserver.ForwardKind
	tool string
	args string
}

func (f *fakeForwarder) ForwardToolCall(_ context.Context, _ sessionstore.Session, kind sessionserver.ForwardKind, tool string, args json.RawMessage) (mcp.ToolResult, sessionserver.ForwardOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, forwardedCall{kind: kind, tool: tool, args: string(args)})
	return f.result, f.outcome, f.err
}

// countingExec counts the sends that reach the local executor.
type countingExec struct {
	executor.Executor
	mu    sync.Mutex
	sends int
}

func (c *countingExec) Send(ctx context.Context, id string, msgs []executor.Message) (executor.Response, error) {
	c.mu.Lock()
	c.sends++
	c.mu.Unlock()
	return c.Executor.Send(ctx, id, msgs)
}

func refusal(state session.State) error {
	return mcp.NewToolError("TARGET_NOT_READY", "unreachable", map[string]any{"currentState": string(state)})
}

// forwardedHandler serves h with every request marked as one the forward
// listener received.
func forwardedHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(sessionserver.WithForwarded(r.Context())))
	})
}

// TestOffHolderSendMessageReturnsTheCoordinatorsAnswer_spec_29_3 pins the
// forward of lenny/send_message: a delivered forward's result is the tool's
// answer, and the scope check, the inReplyTo resolution, and the local
// delivery path do not run on the serving replica. A sender that does not
// exist would be refused with SCOPE_DENIED locally, so a delivered answer
// shows the forward ran before the scope check. An outcome-unknown forward
// and a not-delivered forward of a message carrying inReplyTo return the
// refusal and hold nothing.
//
// spec: 29.3 (Interactive message send) Forwarding rules 1, 2, 6, and 7,
// 15.2 (MCP API)
func TestOffHolderSendMessageReturnsTheCoordinatorsAnswer_spec_29_3(t *testing.T) {
	coordinatorAnswer := mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: `{"deliveryReceipt":{"status":"delivered","messageId":"m-coord"}}`}}}
	cases := []struct {
		name     string
		fwd      *fakeForwarder
		args     string
		wantKind sessionserver.ForwardKind
		wantCode string
	}{
		{
			"delivered", &fakeForwarder{outcome: sessionserver.ForwardDelivered, result: coordinatorAnswer},
			`{"to":"sess_t","message":"hi","fromSessionId":"sess_ghost"}`, sessionserver.ForwardKindMessageNoReplyTo, "",
		},
		{
			"outcome unknown", &fakeForwarder{outcome: sessionserver.ForwardUnknown, err: refusal(session.StateRunning)},
			`{"to":"sess_t","message":"hi"}`, sessionserver.ForwardKindMessageNoReplyTo, "TARGET_NOT_READY",
		},
		{
			"inReplyTo not delivered", &fakeForwarder{outcome: sessionserver.ForwardNotDelivered, err: refusal(session.StateInputRequired)},
			`{"to":"sess_t","message":"hi","inReplyTo":"req-1"}`, sessionserver.ForwardKindMessage, "TARGET_NOT_READY",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := &countingExec{Executor: executor.NewEchoExecutor()}
			srv, store, _, _, del := newMCPRoutingDeliveryWith(t, exec, func(d *mcptools.Deps) { d.HolderForward = tc.fwd })
			mkSession(t, store, "sess_t", session.StateRunning, "")
			resp := call(t, srv.Handler(), "lenny/send_message", tc.args)
			if len(tc.fwd.calls) != 1 || tc.fwd.calls[0].tool != "lenny/send_message" || tc.fwd.calls[0].kind != tc.wantKind {
				t.Fatalf("forward calls = %+v, want one lenny/send_message of kind %d", tc.fwd.calls, tc.wantKind)
			}
			if tc.fwd.calls[0].args != tc.args {
				t.Errorf("forwarded arguments = %s, want the client's %s", tc.fwd.calls[0].args, tc.args)
			}
			if tc.wantCode == "" {
				if got := receiptFromResult(t, resp); got.MessageID != "m-coord" {
					t.Errorf("receipt = %+v, want the coordinator's", got)
				}
			} else if code := toolErrorCode(t, resp); code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if exec.sends != 0 || del.holds != 0 || del.undelivered != 0 {
				t.Errorf("local sends %d, holds %d, undelivered holds %d; want none on the serving replica", exec.sends, del.holds, del.undelivered)
			}
		})
	}
}

// TestUndeliveredSendMessageIsCheckedThenHeld_spec_29_3 pins rule 6 on the
// MCP site: a message carrying no inReplyTo whose forward is not delivered
// runs the scope check, refusing an out-of-scope target with SCOPE_DENIED and
// holding nothing, and an in-scope message is held through the
// undelivered-forward hold, not classified, and answered queued.
//
// spec: 29.3 (Interactive message send) Forwarding rule 6, 7.2 (Message
// delivery routing — seven paths, path 6)
func TestUndeliveredSendMessageIsCheckedThenHeld_spec_29_3(t *testing.T) {
	fwd := &fakeForwarder{outcome: sessionserver.ForwardNotDelivered}
	exec := &countingExec{Executor: executor.NewEchoExecutor()}
	srv, store, _, _, del := newMCPRoutingDeliveryWith(t, exec, func(d *mcptools.Deps) { d.HolderForward = fwd })
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")

	if code := toolErrorCode(t, call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)); code != "SCOPE_DENIED" {
		t.Fatalf("out-of-scope code = %q, want SCOPE_DENIED", code)
	}
	if del.undelivered != 0 {
		t.Fatalf("an out-of-scope message was held %d times, want none", del.undelivered)
	}
	got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_parent"}`))
	if got.Status != session.DeliveryStatusQueued {
		t.Errorf("in-scope receipt = %q, want queued", got.Status)
	}
	if del.undelivered != 1 || exec.sends != 0 || del.locked != 0 {
		t.Errorf("undelivered holds %d, sends %d, delivery locks %d; want one hold and no classification", del.undelivered, exec.sends, del.locked)
	}
}

// TestLocalForwardOutcomeRunsTheLocalSendPath_spec_29_3 pins that a local
// outcome leaves the handler on its local delivery path.
//
// spec: 29.3 (Interactive message send) Forwarding rule 1
func TestLocalForwardOutcomeRunsTheLocalSendPath_spec_29_3(t *testing.T) {
	fwd := &fakeForwarder{outcome: sessionserver.ForwardLocal}
	exec := &countingExec{Executor: executor.NewEchoExecutor()}
	srv, store, _, _, _ := newMCPRoutingDeliveryWith(t, exec, func(d *mcptools.Deps) { d.HolderForward = fwd })
	mkSession(t, store, "sess_t", session.StateRunning, "")
	if got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_t","message":"hi"}`)); got.Status != session.DeliveryStatusDelivered {
		t.Fatalf("receipt = %q, want delivered", got.Status)
	}
	if exec.sends != 1 {
		t.Errorf("local sends = %d, want 1", exec.sends)
	}
}

// TestForwardedSendMessageSkipsTheSenderLimits_spec_29_3 pins that the
// coordinator of a forwarded call does not count the sender-keyed limits,
// which the serving replica applied, while an unmarked call still counts
// them.
//
// spec: 29.3 (Interactive message send) Forwarding rule 4, 8.3 (Delegation
// Policy and Lease)
func TestForwardedSendMessageSkipsTheSenderLimits_spec_29_3(t *testing.T) {
	srv, store := newMCPMessaging(t, "", "", mcptools.MessagingRateLimit{MaxPerMinute: 1, MaxPerSession: 1, MaxInboundPerMinute: 100})
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateRunning, "sess_parent")
	args := `{"to":"sess_c","message":"hi","fromSessionId":"sess_parent"}`
	for i := 0; i < 2; i++ {
		if got := receiptFromResult(t, call(t, forwardedHandler(srv.Handler()), "lenny/send_message", args)); got.Status != session.DeliveryStatusDelivered {
			t.Fatalf("forwarded send %d = %q, want delivered", i+1, got.Status)
		}
	}
	if got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", args)); got.Status != session.DeliveryStatusDelivered {
		t.Fatalf("first unmarked send = %q, want delivered", got.Status)
	}
	if got := receiptFromResult(t, call(t, srv.Handler(), "lenny/send_message", args)); got.Status != session.DeliveryStatusRateLimited {
		t.Errorf("second unmarked send = %q, want rate_limited", got.Status)
	}
}

// TestOffHolderCancelSessionReturnsTheForwardAnswer_spec_29_3 pins the
// forward of lenny/cancel_session: the forward's answer, a refusal included,
// is returned under every reason, so the playground_client_closed hint path
// never turns a refusal into an accepted answer, and the serving replica
// writes nothing. A precondition refusal is answered before the forward.
//
// spec: 29.3 (Interactive message send) Forwarding rules 1 and 6, 15.2 (MCP
// API), 27.6
func TestOffHolderCancelSessionReturnsTheForwardAnswer_spec_29_3(t *testing.T) {
	for _, reason := range []string{"", "playground_client_closed"} {
		t.Run("reason "+reason, func(t *testing.T) {
			fwd := &fakeForwarder{outcome: sessionserver.ForwardNotDelivered, err: refusal(session.StateRunning)}
			srv, store, _, _, _ := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) { d.HolderForward = fwd })
			mkSession(t, store, "sess_t", session.StateRunning, "")
			args := `{"sessionId":"sess_t","reason":"` + reason + `"}`
			if code := toolErrorCode(t, call(t, srv.Handler(), "lenny/cancel_session", args)); code != "TARGET_NOT_READY" {
				t.Fatalf("code = %q, want TARGET_NOT_READY", code)
			}
			if len(fwd.calls) != 1 || fwd.calls[0].kind != sessionserver.ForwardKindTerminate || fwd.calls[0].tool != "lenny/cancel_session" {
				t.Errorf("forward calls = %+v, want one lenny/cancel_session terminate", fwd.calls)
			}
			row, _ := store.Get(context.Background(), "acme", "sess_t")
			if row.State != session.StateRunning || !row.TerminatedAt.IsZero() {
				t.Errorf("row = %q (terminated %v), want running and unwritten", row.State, row.TerminatedAt)
			}
		})
	}
	t.Run("delivered", func(t *testing.T) {
		fwd := &fakeForwarder{outcome: sessionserver.ForwardDelivered, result: mcp.ToolResult{Content: []mcp.ToolContent{{Type: "text", Text: `{"sessionId":"sess_t","state":"cancelled"}`}}}}
		srv, store, _, _, _ := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) { d.HolderForward = fwd })
		mkSession(t, store, "sess_t", session.StateRunning, "")
		resp := call(t, srv.Handler(), "lenny/cancel_session", `{"sessionId":"sess_t"}`)
		result, _ := resp["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatalf("delivered cancel returned an error: %+v", result)
		}
		if row, _ := store.Get(context.Background(), "acme", "sess_t"); row.State != session.StateRunning {
			t.Errorf("serving replica wrote %q, want no local write", row.State)
		}
	})
	t.Run("terminal row refused before the forward", func(t *testing.T) {
		fwd := &fakeForwarder{outcome: sessionserver.ForwardDelivered}
		srv, store, _, _, _ := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) { d.HolderForward = fwd })
		mkSession(t, store, "sess_t", session.StateCompleted, "")
		_ = call(t, srv.Handler(), "lenny/cancel_session", `{"sessionId":"sess_t"}`)
		if len(fwd.calls) != 0 {
			t.Errorf("a terminal row was forwarded %d times, want the precondition refusal first", len(fwd.calls))
		}
	})
}
