// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	idemmw "github.com/lennylabs/lenny/pkg/gateway/middleware/idempotency"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/pkg/mtls/certreload"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
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

// countingLeases counts the lease reads of the wrapped store, one per
// forward attempt that reaches the forward helper.
type countingLeases struct {
	leasestore.LeaseStore
	gets atomic.Int64
}

func (c *countingLeases) Get(ctx context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	c.gets.Add(1)
	return c.LeaseStore.Get(ctx, tenantID, sessionID)
}

// devAuth is the dev-header auth transport the test surfaces run behind.
var devAuth = authmw.Options{AllowDevHeaders: true, DevHeaderNamesTenant: true}

// forwardMaterial is the gateway certificate material and the forward client
// of one test.
type forwardMaterial struct {
	pki      *gatewaypki.Material
	reloader *certreload.Reloader
}

func newForwardMaterial(t *testing.T) *forwardMaterial {
	t.Helper()
	m := gatewaypki.Mint(t)
	reloader, err := certreload.New(m.CertFile, m.KeyFile)
	if err != nil {
		t.Fatalf("certreload: %v", err)
	}
	return &forwardMaterial{pki: m, reloader: reloader}
}

// serve serves h on a loopback CH-HOLDERFORWARD TLS listener and returns its
// address.
func (f *forwardMaterial) serve(t *testing.T, h http.Handler) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h, TLSConfig: sessionserver.HolderForwardServerTLS(f.reloader, f.pki.CAPool), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.ServeTLS(lis, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return lis.Addr().String()
}

// forwarder is a serving replica gw-a whose lease store names holder for
// sess_t, with the real forward client.
func (f *forwardMaterial) forwarder(t *testing.T, holder string) (*sessionserver.Server, *countingLeases) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	leases := &countingLeases{LeaseStore: leasestore.New(rc)}
	if _, err := leases.Acquire(context.Background(), "acme", "sess_t", holder, time.Minute); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	return sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader:        true,
		CoordinationLeaseStore: leases,
		ReplicaID:              "gw-a.00@127.0.0.1:1",
		HolderForwardClient:    sessionserver.NewHolderForwardClient(f.reloader, f.pki.CAPool),
		PodBinder:              &podsession.Binder{},
	}), leases
}

// closedListenerAddr is an address on which nothing listens.
func closedListenerAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	_ = lis.Close()
	return addr
}

// hangUpAfterRead closes the connection with no response after it reads the
// request.
var hangUpAfterRead = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body)
	if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
		_ = conn.Close()
	}
})

// keyedCancel posts lenny/cancel_session for sess_t with an Idempotency-Key.
func keyedCancel(h http.Handler, key string) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"lenny/cancel_session","arguments":{"sessionId":"sess_t"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestWiredCancelSessionForwardRefusalsStayOutOfTheIdempotencyCache_spec_11_5
// runs the idempotency cases of the forward helper against the wired
// lenny/cancel_session site behind the §11.5 middleware: a keyed call whose
// forward is not delivered, has an unknown outcome, or reaches a replica
// that no longer coordinates the session answers the tool error
// TARGET_NOT_READY, stores no idempotency row, and a same-key retry reaches
// the forward again with no internal mark on the client response; the
// serving replica writes nothing.
//
// spec: 11.5 (Idempotency) Mechanism, 29.3 (Interactive message send)
// Forwarding rules 5, 6, and 7, 15.2 (MCP API)
func TestWiredCancelSessionForwardRefusalsStayOutOfTheIdempotencyCache_spec_11_5(t *testing.T) {
	f := newForwardMaterial(t)
	nonHolder := func(t *testing.T) string {
		third, _ := f.forwarder(t, "gw-c.00@127.0.0.1:1")
		coord, store, _, _, _ := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) { d.HolderForward = third })
		mkSession(t, store, "sess_t", session.StateRunning, "")
		return f.serve(t, sessionserver.NewHolderForwardHandler(coord.Handler(), devAuth, nil))
	}
	targets := []struct {
		name string
		addr func(t *testing.T) string
	}{
		{"not delivered", closedListenerAddr},
		{"unknown outcome", func(t *testing.T) string { return f.serve(t, hangUpAfterRead) }},
		{"refused by a non-holder", nonHolder},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			fwd, leases := f.forwarder(t, "gw-b.00@"+target.addr(t))
			srv, store, _, _, _ := newMCPRoutingDeliveryWith(t, nil, func(d *mcptools.Deps) { d.HolderForward = fwd })
			mkSession(t, store, "sess_t", session.StateRunning, "")
			cache := idemmw.NewMemoryStore()
			h := authmw.Wrap(idemmw.Wrap(srv.Handler(), cache, idemmw.Options{}), devAuth)
			for i := 0; i < 2; i++ {
				rec := keyedCancel(h, "cancel-key")
				var resp map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("attempt %d: decode %s: %v", i, rec.Body.String(), err)
				}
				if code := toolErrorCode(t, resp); code != "TARGET_NOT_READY" {
					t.Fatalf("attempt %d answered %s, want TARGET_NOT_READY", i, rec.Body.String())
				}
				if rec.Header().Get("X-Lenny-Forward-No-Store") != "" {
					t.Errorf("attempt %d carries the internal no-store mark", i)
				}
			}
			if n := leases.gets.Load(); n != 2 {
				t.Errorf("forward reached %d times, want 2: a same-key retry after a refusal runs again", n)
			}
			if _, ok, _ := cache.Get(context.Background(), "acme", "cancel-key"); ok {
				t.Error("the refusal was stored in the idempotency cache")
			}
			if row, _ := store.Get(context.Background(), "acme", "sess_t"); row.State != session.StateRunning || !row.TerminatedAt.IsZero() {
				t.Errorf("row = %q (terminated %v), want running and unwritten", row.State, row.TerminatedAt)
			}
		})
	}
}
