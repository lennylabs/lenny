// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func newMCP(t *testing.T) (*mcp.Server, sessionstore.Store) {
	srv, store, _ := newMCPWithRuntimes(t)
	return srv, store
}

// newMCPWithRuntimes builds the MCP server like newMCP and also returns
// the §5.1 runtime registry so discover_agents tests can seed it.
func newMCPWithRuntimes(t *testing.T) (*mcp.Server, sessionstore.Store, runtimestore.Store) {
	t.Helper()
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Runtimes:     runtimes,
		Events:       sessionevents.NewBus(0),
		InputWaits:   inputwait.NewRegistry(),
		Interactions: interactionstore.NewMemory(),
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	return srv, store, runtimes
}

// newMCPForInput builds the MCP server with a lenny/request_input
// registry and the given §11.3 timeout, returning the registry.
func newMCPForInput(t *testing.T, timeout time.Duration) (*mcp.Server, sessionstore.Store, *inputwait.Registry) {
	t.Helper()
	store := memstore.New()
	reg := inputwait.NewRegistry()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		InputWaits:          reg,
		RequestInputTimeout: timeout,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	return srv, store, reg
}

// newMCPForArchive builds the MCP server with a §8.10 tree archive and
// returns the archive so the cancel_child archiving test can read it.
func newMCPForArchive(t *testing.T) (*mcp.Server, sessionstore.Store, treearchive.Store) {
	t.Helper()
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:       store,
		TreeArchive: archive,
		Clock:       func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:      func() string { return "sess_mcp" },
		TenantID:    "acme",
	})
	return srv, store, archive
}

// newMCPForElicitation builds the MCP server with a §9.2 interaction
// store and the given maxElicitationWait, returning the store.
func newMCPForElicitation(t *testing.T, timeout time.Duration) (*mcp.Server, sessionstore.Store, interactionstore.Store) {
	t.Helper()
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:              store,
		Interactions:       interactions,
		ElicitationTimeout: timeout,
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "elic_gen" },
		TenantID:           "acme",
	})
	return srv, store, interactions
}

// waitElicitation blocks until an elicitation is recorded, or fails.
func waitElicitation(t *testing.T, interactions interactionstore.Store, sessionID, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := interactions.Get(context.Background(), "acme", sessionID, "", id); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("elicitation %s was never recorded", id)
}

// waitPending blocks until a request is registered, or fails the test.
func waitPending(t *testing.T, reg *inputwait.Registry, sessionID, requestID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reg.Pending(sessionID, requestID) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("request %s/%s never became pending", sessionID, requestID)
}

// newMCPForOutput builds the MCP server with a §15.1 event bus and
// returns the bus so the lenny/output tests can read published events.
func newMCPForOutput(t *testing.T) (*mcp.Server, sessionstore.Store, *sessionevents.Bus) {
	t.Helper()
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Events:   bus,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	return srv, store, bus
}

func call(t *testing.T, h http.Handler, tool, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// readLennyErrorEnvelope extracts the full §15.2.1 lenny error envelope
// (code, category, retryable, message, details) from the `lenny/error`
// content block of an isError tool result. Used by the §9.2 elicitation
// tests to verify the MCP envelope shape. F-9.2.17 / F-9.2.18.
func readLennyErrorEnvelope(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["isError"] != true {
		t.Fatalf("expected isError result, got %+v", result)
	}
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "lenny/error" {
			continue
		}
		text, _ := block["text"].(string)
		var env map[string]any
		if err := json.Unmarshal([]byte(text), &env); err != nil {
			t.Fatalf("decode error envelope: %v", err)
		}
		return env
	}
	t.Fatalf("no lenny/error block in %+v", content)
	return nil
}

func resultText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", resp)
	}
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("tool returned error: %v", c0["text"])
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	c0, _ := content[0].(map[string]any)
	s, _ := c0["text"].(string)
	return s
}

func TestCreateSessionTool(t *testing.T) {
	srv, store := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_mcp") {
		t.Errorf("create_session result: %q", text)
	}
	row, err := store.Get(context.Background(), "acme", "sess_mcp")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.RuntimeRef != "echo" || row.State != session.StateRunning {
		t.Errorf("stored row: %+v", row)
	}
}

// TestCreateSessionToolDevModeIsolation_spec_5_3 verifies the §5.3 line
// 677 dev-mode fallback in the lenny/create_session tool: the default
// session profile is sandboxed normally and standard (runc) when the
// adapter is wired with DevMode.
//
// spec: §5.3 line 677.
func TestCreateSessionToolDevModeIsolation_spec_5_3(t *testing.T) {
	// Default (production) mode: sandboxed.
	_, store := newMCP(t)
	{
		srv, st := newMCP(t)
		_ = call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
		row, err := st.Get(context.Background(), "acme", "sess_mcp")
		if err != nil {
			t.Fatalf("session not stored: %v", err)
		}
		if row.IsolationProfile != isolation.ProfileSandboxed {
			t.Errorf("default-mode isolation = %q, want sandboxed", row.IsolationProfile)
		}
	}
	_ = store

	// Dev mode: standard (runc).
	devStore := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    devStore,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimestore.NewMemory(),
		DevMode:  true,
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_dev" },
		TenantID: "acme",
	})
	_ = call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
	row, err := devStore.Get(context.Background(), "acme", "sess_dev")
	if err != nil {
		t.Fatalf("dev session not stored: %v", err)
	}
	if row.IsolationProfile != isolation.ProfileStandard {
		t.Errorf("dev-mode isolation = %q, want standard", row.IsolationProfile)
	}
}

func TestCreateSessionToolRecordsEnvironment(t *testing.T) {
	srv, store := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/create_session",
		`{"runtimeRef":"echo","userId":"alice","environment":"security-team"}`)
	if !strings.Contains(resultText(t, resp), "sess_mcp") {
		t.Fatalf("create_session result: %+v", resp)
	}
	row, err := store.Get(context.Background(), "acme", "sess_mcp")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "security-team" {
		t.Errorf("stored §10.6 environment: got %q, want security-team", row.Environment)
	}
}

func TestCreateSessionToolRejectsMissingRuntime(t *testing.T) {
	srv, _ := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/create_session", `{}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("missing runtimeRef should be a tool error: %+v", resp)
	}
}

// newMCPWithChain builds the MCP server with a §4 interceptor chain so
// the PreMessageDelivery wiring tests can register interceptors.
func newMCPWithChain(t *testing.T, chain *interceptor.Chain) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:        store,
		Executor:     executor.NewEchoExecutor(),
		Interceptors: chain,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:       func() string { return "sess_mcp" },
		TenantID:     "acme",
	})
	return srv, store
}

// staticInterceptor is a built-in interceptor returning a fixed result.
type staticInterceptor struct{ result interceptor.Result }

func (s staticInterceptor) Name() string                       { return "test-static" }
func (s staticInterceptor) Priority() int32                    { return 200 }
func (s staticInterceptor) Builtin() bool                      { return true }
func (s staticInterceptor) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }
func (s staticInterceptor) Timeout() time.Duration             { return 0 }
func (s staticInterceptor) Intercept(context.Context, interceptor.Request) (interceptor.Result, error) {
	return s.result, nil
}

func TestSendMessageTool(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	// spec: §15.4 lines 1725-1737 — every lenny/send_message call
	// returns a `delivery_receipt` envelope as the first text block,
	// followed by the runtime's text output. F-7.2.10.
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) < 2 {
		t.Fatalf("send_message must return receipt + output blocks; got %d blocks: %+v", len(content), content)
	}
	first, _ := content[0].(map[string]any)
	receiptJSON, _ := first["text"].(string)
	var envelope struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
	}
	if err := json.Unmarshal([]byte(receiptJSON), &envelope); err != nil {
		t.Fatalf("first block must be a deliveryReceipt JSON envelope: %v; body=%s", err, receiptJSON)
	}
	if envelope.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("delivery receipt status = %q, want %q", envelope.DeliveryReceipt.Status, session.DeliveryStatusDelivered)
	}
	if envelope.DeliveryReceipt.MessageID == "" {
		t.Error("delivery receipt messageId is empty; §15.4 line 1784 requires a gateway-assigned id")
	}
	if envelope.DeliveryReceipt.DeliveredAt.IsZero() {
		t.Error("delivery receipt deliveredAt is zero for status=delivered")
	}
	second, _ := content[1].(map[string]any)
	if echo, _ := second["text"].(string); !strings.Contains(echo, "ping") {
		t.Errorf("second block must carry the executor echo; got %q", echo)
	}
}

// TestSendMessageToolDeliveryReceiptOnInReplyTo asserts that the
// §8.5 inReplyTo path also returns a §15.4 delivery_receipt — the
// runtime consumed the answer, so status = delivered. The pre-fix
// shape `{"resolved":"req-1"}` carried no receipt and clients tracking
// receipts to reconcile retries had no signal on the inReplyTo path.
// spec: §15.4 lines 1725-1737; §8.5 inReplyTo resolution. F-7.2.10.
func TestSendMessageToolDeliveryReceiptOnInReplyTo(t *testing.T) {
	srv, store, reg := newMCPForInput(t, 5*time.Second)
	mkSession(t, store, "sess_i", session.StateRunning, "")
	h := srv.Handler()

	go func() {
		_ = call(t, h, "lenny/request_input",
			`{"sessionId":"sess_i","requestId":"req-1","parts":[{"type":"text","text":"pick a color"}]}`)
	}()
	waitPending(t, reg, "sess_i", "req-1")

	resp := call(t, h, "lenny/send_message",
		`{"to":"sess_i","message":"blue","inReplyTo":"req-1","messageId":"msg_caller"}`)
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) < 1 {
		t.Fatalf("inReplyTo result must contain a receipt block; got %+v", content)
	}
	first, _ := content[0].(map[string]any)
	receiptJSON, _ := first["text"].(string)
	var envelope struct {
		DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
		Resolved        string                  `json:"resolved"`
	}
	if err := json.Unmarshal([]byte(receiptJSON), &envelope); err != nil {
		t.Fatalf("inReplyTo result must be a deliveryReceipt envelope: %v; body=%s", err, receiptJSON)
	}
	if envelope.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
		t.Errorf("inReplyTo receipt status = %q, want %q", envelope.DeliveryReceipt.Status, session.DeliveryStatusDelivered)
	}
	if envelope.DeliveryReceipt.MessageID != "msg_caller" {
		t.Errorf("inReplyTo receipt messageId = %q, want the sender-supplied msg_caller", envelope.DeliveryReceipt.MessageID)
	}
	if envelope.Resolved != "req-1" {
		t.Errorf("inReplyTo receipt `resolved` = %q, want req-1", envelope.Resolved)
	}
}

func TestSendMessageToolRejectsTerminalSession(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_done", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_done","message":"x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("terminal session should be a tool error: %+v", resp)
	}
}

func TestSendMessageRejectedByInterceptor(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreMessageDelivery, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionReject, Reason: "prompt injection detected"},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	srv, store := newMCPWithChain(t, chain)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a PreMessageDelivery REJECT should be a tool error: %+v", resp)
	}
}

func TestSendMessageModifiedByInterceptor(t *testing.T) {
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreMessageDelivery, staticInterceptor{
		result: interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("scrubbed")},
	}); err != nil {
		t.Fatalf("register interceptor: %v", err)
	}
	srv, store := newMCPWithChain(t, chain)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	// The echo executor reflects the delivered body. A PreMessageDelivery
	// MODIFY must rewrite what the target session receives.
	resp := call(t, srv.Handler(), "lenny/send_message", `{"to":"sess_x","message":"ping"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "scrubbed") {
		t.Errorf("echo response %q should reflect the interceptor-modified body", text)
	}
	if strings.Contains(text, "ping") {
		t.Errorf("echo response %q still carries the original body — MODIFY was not applied", text)
	}
}

func TestGetTaskTreeTool(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_root",
		CreatedAt:       now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_root") || !strings.Contains(text, "sess_kid") {
		t.Errorf("task tree: %q", text)
	}
}

func TestDelegateTaskTool(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:  now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"gemini","poolRef":"pool-b"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_child") {
		t.Errorf("delegate result: %q", text)
	}
	child, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("child not stored: %v", err)
	}
	if child.ParentSessionID != "sess_parent" {
		t.Errorf("child parent: %q", child.ParentSessionID)
	}
}

func TestDelegateTaskToolDetectsCycle(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:  now, UpdatedAt: now,
	})
	// Delegating back to the parent's own (runtime, pool) is a cycle.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"claude","poolRef":"pool-a"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cycle should be a tool error: %+v", resp)
	}
}

// spec: §8.2 line 17 — `lenny/delegate_task` returns a TaskHandle. v1
// ships the typed envelope (childSessionId, state, runtimeRef, depth)
// so callers can decode against a stable shape rather than a
// hand-rolled JSON string.
func TestDelegateTaskToolReturnsTaskHandleEnvelope(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:  now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"gemini","poolRef":"pool-b"}`)
	text := resultText(t, resp)
	var handle struct {
		ChildSessionID string `json:"childSessionId"`
		State          string `json:"state"`
		RuntimeRef     string `json:"runtimeRef"`
		Depth          int    `json:"depth"`
	}
	if err := json.Unmarshal([]byte(text), &handle); err != nil {
		t.Fatalf("TaskHandle is not valid JSON: %v (raw=%q)", err, text)
	}
	if handle.ChildSessionID != "sess_child" {
		t.Errorf("childSessionId = %q, want sess_child", handle.ChildSessionID)
	}
	if handle.State != string(session.StateCreated) {
		t.Errorf("state = %q, want %q", handle.State, session.StateCreated)
	}
	if handle.RuntimeRef != "gemini" {
		t.Errorf("runtimeRef = %q, want gemini", handle.RuntimeRef)
	}
	if handle.Depth != 1 {
		t.Errorf("depth = %d, want 1 (root parent → child)", handle.Depth)
	}
}

// spec: §8.2 line 58 — Delegate rejects a userless parent with
// ErrParentNoUser; the MCP shim surfaces DELEGATION_PARENT_NO_USER
// so callers can distinguish it from the generic error path.
func TestDelegateTaskToolRejectsUserlessParent(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		// UserID intentionally omitted.
		ID: "sess_parent", TenantID: "acme",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt:  now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"gemini","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("userless parent must be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("error result has no content: %+v", resp)
	}
	c0, _ := content[0].(map[string]any)
	errText, _ := c0["text"].(string)
	if !strings.Contains(errText, "DELEGATION_PARENT_NO_USER") {
		t.Errorf("error text = %q, want DELEGATION_PARENT_NO_USER reason", errText)
	}
	// No child must be created when the gate trips.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Error("a child was created despite the userless-parent rejection")
	}
}

// mkSession inserts a session row for the cancel_child tree tests.
func mkSession(t *testing.T, store sessionstore.Store, id string, state session.State, parent string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: state, ParentSessionID: parent,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func TestCancelChildTool(t *testing.T) {
	srv, store := newMCP(t)
	// sess_root → sess_a → {sess_a1 (running), sess_a2 (completed) → sess_a2x (running)}
	// sess_root → sess_b is a sibling of sess_a, outside the cancelled subtree.
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")
	mkSession(t, store, "sess_a2", session.StateCompleted, "sess_a")
	mkSession(t, store, "sess_a2x", session.StateRunning, "sess_a2")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_root")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`)
	text := resultText(t, resp)
	for _, want := range []string{"sess_a", "sess_a1", "sess_a2x"} {
		if !strings.Contains(text, want) {
			t.Errorf("cancel_child result %q missing %q", text, want)
		}
	}

	// The child and every non-terminal descendant are cancelled — the
	// traversal descends through the terminal sess_a2 to reach sess_a2x.
	for _, id := range []string{"sess_a", "sess_a1", "sess_a2x"} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateCancelled {
			t.Errorf("%s state = %q, want cancelled", id, row.State)
		}
	}
	// The already-terminal descendant keeps its terminal state.
	if row, _ := store.Get(context.Background(), "acme", "sess_a2"); row.State != session.StateCompleted {
		t.Errorf("sess_a2 state = %q, want completed unchanged", row.State)
	}
	// The calling parent and the untouched sibling stay running.
	for _, id := range []string{"sess_root", "sess_b"} {
		if row, _ := store.Get(context.Background(), "acme", id); row.State != session.StateRunning {
			t.Errorf("%s state = %q, want running unchanged", id, row.State)
		}
	}
}

func TestCancelChildToolRejectsNonDescendant(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_unrelated", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_unrelated"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cancelling a non-descendant should be a tool error: %+v", resp)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_unrelated"); row.State != session.StateRunning {
		t.Errorf("non-descendant state = %q, want running unchanged", row.State)
	}
}

func TestCancelChildToolRejectsTerminalChild(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_done", session.StateCompleted, "sess_root")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_done"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cancelling a terminal child should be a tool error: %+v", resp)
	}
}

func TestCancelChildArchivesCancelledSubtree(t *testing.T) {
	srv, store, archive := newMCPForArchive(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_a1", session.StateRunning, "sess_a")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_a"}`)
	resultText(t, resp) // fails the test on a tool error

	// §8.10: every cancelled subtree node is archived under the tree
	// root (sess_root), in original-settlement order.
	archived, err := archive.Replay(context.Background(), "acme", "sess_root")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(archived) != 2 {
		t.Fatalf("archived %d nodes, want 2 (sess_a, sess_a1)", len(archived))
	}
	for _, n := range archived {
		if n.State != string(session.StateCancelled) {
			t.Errorf("archived node %s state = %q, want cancelled", n.NodeSessionID, n.State)
		}
		if !strings.Contains(n.Result, "CHILD_CANCELLED") {
			t.Errorf("archived node %s result %q missing the cancellation error", n.NodeSessionID, n.Result)
		}
	}
}

func TestCancelChildToolRejectsSelf(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_root"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cancelling self should be a tool error: %+v", resp)
	}
}

// mkRuntime registers a runtime for the discover_agents tests.
func mkRuntime(t *testing.T, runtimes runtimestore.Store, name string, typ runtimestore.RuntimeType) {
	t.Helper()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: name, Type: typ}); err != nil {
		t.Fatalf("seed runtime %s: %v", name, err)
	}
}

func TestDiscoverAgentsTool(t *testing.T) {
	srv, _, runtimes := newMCPWithRuntimes(t)
	mkRuntime(t, runtimes, "claude-agent", runtimestore.TypeAgent)
	mkRuntime(t, runtimes, "gemini-agent", runtimestore.TypeAgent)
	mkRuntime(t, runtimes, "filesystem-mcp", runtimestore.TypeMCP)

	resp := call(t, srv.Handler(), "lenny/discover_agents", `{}`)
	text := resultText(t, resp)
	for _, want := range []string{"claude-agent", "gemini-agent"} {
		if !strings.Contains(text, want) {
			t.Errorf("discover_agents result %q missing agent %q", text, want)
		}
	}
	// §8.5: type:mcp runtimes are never delegation targets.
	if strings.Contains(text, "filesystem-mcp") {
		t.Errorf("discover_agents result %q leaked a type:mcp runtime", text)
	}
}

func TestDiscoverAgentsToolNameFilter(t *testing.T) {
	srv, _, runtimes := newMCPWithRuntimes(t)
	mkRuntime(t, runtimes, "claude-agent", runtimestore.TypeAgent)
	mkRuntime(t, runtimes, "claude-haiku", runtimestore.TypeAgent)
	mkRuntime(t, runtimes, "gemini-agent", runtimestore.TypeAgent)

	resp := call(t, srv.Handler(), "lenny/discover_agents", `{"nameContains":"claude"}`)
	text := resultText(t, resp)
	for _, want := range []string{"claude-agent", "claude-haiku"} {
		if !strings.Contains(text, want) {
			t.Errorf("filtered discover_agents %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "gemini-agent") {
		t.Errorf("filtered discover_agents %q should exclude gemini-agent", text)
	}
}

func TestDiscoverAgentsToolExcludesSoftDeleted(t *testing.T) {
	srv, _, runtimes := newMCPWithRuntimes(t)
	mkRuntime(t, runtimes, "live-agent", runtimestore.TypeAgent)
	mkRuntime(t, runtimes, "retired-agent", runtimestore.TypeAgent)
	if err := runtimes.SoftDelete(context.Background(), "retired-agent", time.Now()); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/discover_agents", `{}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "live-agent") {
		t.Errorf("discover_agents %q missing live-agent", text)
	}
	if strings.Contains(text, "retired-agent") {
		t.Errorf("discover_agents %q leaked a soft-deleted runtime", text)
	}
}

func TestSetTracingContextTool(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_t","context":{"langsmith_run_id":"run_abc"}}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "run_abc") {
		t.Errorf("set_tracing_context result: %q", text)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_t")
	if row.TracingContext["langsmith_run_id"] != "run_abc" {
		t.Errorf("stored tracingContext = %v, want langsmith_run_id=run_abc", row.TracingContext)
	}
}

func TestSetTracingContextToolMerges(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	// First registration — the inherited / parent entries.
	resultText(t, call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_t","context":{"trace-id":"parent","span_id":"p1"}}`))
	// Second registration — a child extending the context. The
	// colliding key must not be overwritten (§8.3 merge semantics).
	resultText(t, call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_t","context":{"trace-id":"override-attempt","run_id":"c1"}}`))

	row, _ := store.Get(context.Background(), "acme", "sess_t")
	if row.TracingContext["trace-id"] != "parent" {
		t.Errorf("trace-id = %q, want parent — an existing entry must survive", row.TracingContext["trace-id"])
	}
	if row.TracingContext["run_id"] != "c1" {
		t.Errorf("run_id = %q, want c1 — a new entry should be added", row.TracingContext["run_id"])
	}
}

func TestSetTracingContextToolRejectsSensitiveKey(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_t","context":{"api_token":"abc"}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a sensitive key should be a tool error: %+v", resp)
	}
	// spec: §8.3 — the structured envelope carries the stable code so
	// REST and MCP transports map the failure to the same §15.2.1
	// (category, retryable) pair instead of INTERNAL_ERROR. F-8.5.17.
	envelope := readLennyErrorEnvelope(t, result)
	if got := envelope["code"]; got != "TRACING_CONTEXT_SENSITIVE_KEY" {
		t.Errorf("envelope.code = %v, want TRACING_CONTEXT_SENSITIVE_KEY", got)
	}
	if got := envelope["category"]; got != "PERMANENT" {
		t.Errorf("envelope.category = %v, want PERMANENT", got)
	}
	if got, _ := envelope["retryable"].(bool); got {
		t.Errorf("envelope.retryable = true, want false")
	}
}

func TestSetTracingContextToolRejectsURLValue(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_t","context":{"endpoint":"https://evil.example.com"}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a URL value should be a tool error: %+v", resp)
	}
	// spec: §8.3 — F-8.5.17.
	envelope := readLennyErrorEnvelope(t, result)
	if got := envelope["code"]; got != "TRACING_CONTEXT_URL_NOT_ALLOWED" {
		t.Errorf("envelope.code = %v, want TRACING_CONTEXT_URL_NOT_ALLOWED", got)
	}
	if got := envelope["category"]; got != "PERMANENT" {
		t.Errorf("envelope.category = %v, want PERMANENT", got)
	}
}

// TestSetTracingContextToolRejectsTooLarge_spec_8_3 covers the third
// §8.3 validation gate: an oversized tracingContext value surfaces
// the TRACING_CONTEXT_TOO_LARGE code through the structured envelope
// rather than as an INTERNAL_ERROR. F-8.5.17.
func TestSetTracingContextToolRejectsTooLarge_spec_8_3(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_t", session.StateRunning, "")

	oversize := strings.Repeat("x", 2049)
	body := fmt.Sprintf(`{"sessionId":"sess_t","context":{"k":%q}}`, oversize)
	resp := call(t, srv.Handler(), "lenny/set_tracing_context", body)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an oversize value should be a tool error: %+v", resp)
	}
	envelope := readLennyErrorEnvelope(t, result)
	if got := envelope["code"]; got != "TRACING_CONTEXT_TOO_LARGE" {
		t.Errorf("envelope.code = %v, want TRACING_CONTEXT_TOO_LARGE", got)
	}
}

func TestSetTracingContextToolRejectsTerminalSession(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_done", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/set_tracing_context",
		`{"sessionId":"sess_done","context":{"trace-id":"x"}}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a terminal session should be a tool error: %+v", resp)
	}
}

func TestOutputTool(t *testing.T) {
	srv, store, bus := newMCPForOutput(t)
	mkSession(t, store, "sess_o", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/output",
		`{"sessionId":"sess_o","output":[{"type":"text","inline":"hello"}]}`)
	text := resultText(t, resp)
	if !strings.Contains(text, `"emitted":1`) {
		t.Errorf("output result = %q, want emitted:1", text)
	}

	hist := bus.History("sess_o", 0)
	if len(hist) != 1 {
		t.Fatalf("event history has %d events, want 1", len(hist))
	}
	if hist[0].Type != "agent_output" {
		t.Errorf("event type = %q, want agent_output", hist[0].Type)
	}
	if !strings.Contains(hist[0].Data, "hello") {
		t.Errorf("event data %q missing the emitted output", hist[0].Data)
	}
}

func TestOutputToolRejectsTerminalSession(t *testing.T) {
	srv, store, _ := newMCPForOutput(t)
	mkSession(t, store, "sess_done", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/output",
		`{"sessionId":"sess_done","output":[{"type":"text","inline":"x"}]}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("emitting output to a terminal session should be a tool error: %+v", resp)
	}
}

func TestOutputToolRejectsEmptyOutput(t *testing.T) {
	srv, store, _ := newMCPForOutput(t)
	mkSession(t, store, "sess_o", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/output", `{"sessionId":"sess_o","output":[]}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("an empty output array should be a tool error: %+v", resp)
	}
}

func TestRequestInputResolvedByMessage(t *testing.T) {
	srv, store, reg := newMCPForInput(t, 5*time.Second)
	mkSession(t, store, "sess_i", session.StateRunning, "")
	h := srv.Handler()

	// request_input blocks; run it in a goroutine and resolve it from
	// the main goroutine via send_message with a matching inReplyTo.
	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_i","requestId":"req-1","parts":[{"type":"text","text":"pick a color"}]}`)
	}()
	waitPending(t, reg, "sess_i", "req-1")

	resp := call(t, h, "lenny/send_message",
		`{"to":"sess_i","message":"blue","inReplyTo":"req-1"}`)
	if text := resultText(t, resp); !strings.Contains(text, `"resolved":"req-1"`) {
		t.Errorf("send_message inReplyTo result = %q, want resolved req-1", text)
	}

	select {
	case ri := <-got:
		if text := resultText(t, ri); !strings.Contains(text, `"answer":"blue"`) {
			t.Errorf("request_input result = %q, want answer blue", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_input did not return after the message resolved it")
	}
}

func TestRequestInputTimeout(t *testing.T) {
	srv, store, _ := newMCPForInput(t, 40*time.Millisecond)
	mkSession(t, store, "sess_i", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_i","requestId":"req-1","parts":[{"type":"text","text":"ask"}]}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a timed-out request_input should be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "REQUEST_INPUT_TIMEOUT") {
		t.Errorf("timeout error = %q, want REQUEST_INPUT_TIMEOUT", msg)
	}
}

func TestRequestInputRejectsTerminalSession(t *testing.T) {
	srv, store, _ := newMCPForInput(t, time.Second)
	mkSession(t, store, "sess_done", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_done","requestId":"req-1","parts":[{"type":"text","text":"ask"}]}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("request_input on a terminal session should be a tool error: %+v", resp)
	}
}

// TestRequestInputPublishesElicitationRequestEvent_spec_7_2 asserts that
// lenny/request_input surfaces the prompt on the session stream as the
// canonical §7.2 line 136 `elicitation_request` SSE event, not as the
// pre-fix `request_input` synonym that was not in the §7.2 catalog and
// silently bypassed clients filtering on the documented event name.
// spec: §7.2 line 136. F-7.2.17.
func TestRequestInputPublishesElicitationRequestEvent_spec_7_2(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Events:              bus,
		InputWaits:          reg,
		RequestInputTimeout: time.Second,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	mkSession(t, store, "sess_i", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_i","requestId":"req-1","parts":[{"type":"text","text":"pick a color"}]}`)
	}()
	waitPending(t, reg, "sess_i", "req-1")

	hist := bus.History("sess_i", 0)
	if len(hist) != 1 {
		t.Fatalf("event history has %d events, want 1: %+v", len(hist), hist)
	}
	if hist[0].Type != "elicitation_request" {
		t.Errorf("event type = %q, want elicitation_request (§7.2 line 136 canonical name)", hist[0].Type)
	}
	// spec: §8.5 line 539 — the elicitation_request event payload now
	// carries the structured `parts` array (F-8.5.12) rather than the
	// legacy flat `prompt` string.
	if !strings.Contains(hist[0].Data, "req-1") || !strings.Contains(hist[0].Data, "pick a color") {
		t.Errorf("event data = %q, want it to carry the requestId + parts", hist[0].Data)
	}
	if !strings.Contains(hist[0].Data, `"parts"`) {
		t.Errorf("event data = %q, want a `parts` array (F-8.5.12)", hist[0].Data)
	}

	// Unblock the goroutine so the test does not hang on teardown.
	reg.Cancel("sess_i", "req-1")
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("request_input did not return after cancel")
	}
}

// TestSendMessageTopologyAcceptsDirectChild — spec §7.2 line 240
// `direct` scope admits a parent→child message.
// spec: §7.2 line 240, 373; F-7.2.22.
func TestSendMessageTopologyAcceptsDirectChild(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_child", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_child","message":"hi","fromSessionId":"sess_parent"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("parent→child should be admitted; got error %v", c0["text"])
	}
}

// TestSendMessageTopologyAcceptsParent — spec §7.2 line 373 child→
// parent path is allowed under `direct` scope.
// spec: §7.2 line 373; F-7.2.22.
func TestSendMessageTopologyAcceptsParent(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_child", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_parent","message":"hi","fromSessionId":"sess_child"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("child→parent should be admitted; got error %v", c0["text"])
	}
}

// TestSendMessageTopologyAcceptsSibling — siblings share a parent so
// the message is in the `siblings` scope (a superset of `direct`).
// spec: §7.2 line 240; F-7.2.22.
func TestSendMessageTopologyAcceptsSibling(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("sibling→sibling should be admitted; got error %v", c0["text"])
	}
}

// TestSendMessageTopologyRejectsUnrelatedSession — any session pair
// that is not parent/child/sibling is rejected with SCOPE_DENIED.
// spec: §7.2 line 240; §15.1 SCOPE_DENIED. F-7.2.22.
func TestSendMessageTopologyRejectsUnrelatedSession(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_a", session.StateRunning, "")
	mkSession(t, store, "sess_b", session.StateRunning, "")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "SCOPE_DENIED" {
		t.Errorf("error code = %v, want SCOPE_DENIED", env["code"])
	}
}

// TestSendMessageTopologyRejectsGrandparent — two-hop relationships
// fall outside the parent/child/sibling neighborhood.
// spec: §7.2 line 240; F-7.2.22.
func TestSendMessageTopologyRejectsGrandparent(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_mid", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_leaf", session.StateRunning, "sess_mid")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_root","message":"hi","fromSessionId":"sess_leaf"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "SCOPE_DENIED" {
		t.Errorf("error code = %v, want SCOPE_DENIED (grandparent is outside the local neighborhood)", env["code"])
	}
}

// TestSendMessageTopologyRejectsSelf — sender cannot target itself.
// spec: §7.2 line 240; F-7.2.22.
func TestSendMessageTopologyRejectsSelf(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_self", session.StateRunning, "")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_self","message":"hi","fromSessionId":"sess_self"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "SCOPE_DENIED" {
		t.Errorf("error code = %v, want SCOPE_DENIED for self-message", env["code"])
	}
}

// TestSendMessageTopologyDegradesWhenFromSessionIDOmitted — until
// every caller upgrades to declare fromSessionId, the topology check
// is skipped (the existing tests cover this baseline).
// spec: §7.2 line 373; F-7.2.22.
func TestSendMessageTopologyDegradesWhenFromSessionIDOmitted(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_a", session.StateRunning, "")
	mkSession(t, store, "sess_b", session.StateRunning, "")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("call without fromSessionId should fall through to existing behaviour; got error %v", c0["text"])
	}
}

func TestSendMessageInReplyToFallsThroughWithoutPendingInput(t *testing.T) {
	srv, store, _ := newMCPForInput(t, time.Second)
	mkSession(t, store, "sess_i", session.StateRunning, "")

	// inReplyTo references no pending request — the message is an
	// ordinary threaded message and is delivered to the runtime.
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_i","message":"hello","inReplyTo":"req-absent"}`)
	if text := resultText(t, resp); !strings.Contains(text, "hello") {
		t.Errorf("a non-matching inReplyTo should deliver normally, got %q", text)
	}
}

func TestAwaitChildrenAll(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c1", session.StateCompleted, "sess_p")
	mkSession(t, store, "sess_c2", session.StateCompleted, "sess_p")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c1","sess_c2"],"mode":"all"}`)
	text := resultText(t, resp)
	for _, want := range []string{"sess_c1", "sess_c2", "completed"} {
		if !strings.Contains(text, want) {
			t.Errorf("await_children result %q missing %q", text, want)
		}
	}
}

func TestAwaitChildrenAny(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_done", session.StateCompleted, "sess_p")
	mkSession(t, store, "sess_running", session.StateRunning, "sess_p")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_done","sess_running"],"mode":"any"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_done") {
		t.Errorf("await_children any %q should return the settled child", text)
	}
	if strings.Contains(text, "sess_running") {
		t.Errorf("await_children any %q should not include a still-running child", text)
	}
}

func TestAwaitChildrenBlocksUntilSettled(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateRunning, "sess_p")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/await_children",
			`{"sessionId":"sess_p","childIds":["sess_c"],"mode":"all"}`)
	}()

	// The call blocks while the child is still running.
	time.Sleep(60 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("await_children returned before the child reached a terminal state")
	default:
	}

	if _, err := store.Update(context.Background(), "acme", "sess_c",
		func(row *sessionstore.Session) error { row.State = session.StateCompleted; return nil }); err != nil {
		t.Fatalf("settle child: %v", err)
	}

	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "completed") {
			t.Errorf("await_children result = %q, want the settled child", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("await_children did not return after the child settled")
	}
}

func TestAwaitChildrenRejectsNonChild(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_orphan", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_orphan"],"mode":"all"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("awaiting a session that is not a child should be a tool error: %+v", resp)
	}
}

func TestAwaitChildrenReplaysArchivedChild(t *testing.T) {
	srv, store, archive := newMCPForArchive(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	// The child's live row is gone; only the §8.10 archive has it.
	if err := archive.Archive(context.Background(), treearchive.ArchivedNode{
		TenantID:        "acme",
		RootSessionID:   "sess_parent",
		NodeSessionID:   "sess_gone",
		ParentSessionID: "sess_parent",
		State:           "completed",
		Result:          `{"schemaVersion":1,"taskId":"sess_gone","state":"completed"}`,
		SettledAt:       time.Now(),
	}); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_parent","childIds":["sess_gone"],"mode":"all"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_gone") || !strings.Contains(text, "completed") {
		t.Errorf("await_children result %q should replay the archived child", text)
	}
}

func TestAwaitChildrenRejectsArchivedNonChild(t *testing.T) {
	srv, store, archive := newMCPForArchive(t)
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	// The archived child belongs to a different parent.
	if err := archive.Archive(context.Background(), treearchive.ArchivedNode{
		TenantID:        "acme",
		RootSessionID:   "other_root",
		NodeSessionID:   "sess_gone",
		ParentSessionID: "other_parent",
		State:           "completed",
		Result:          `{"taskId":"sess_gone"}`,
		SettledAt:       time.Now(),
	}); err != nil {
		t.Fatalf("seed archive: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_parent","childIds":["sess_gone"],"mode":"all"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("awaiting an archived non-child should be a tool error: %+v", resp)
	}
}

func TestAwaitChildrenFailedChildCarriesError(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_failed", session.StateFailed, "sess_p")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_failed"],"mode":"all"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, `"error"`) {
		t.Errorf("await_children result %q for a failed child should carry an error", text)
	}
}

func TestRequestElicitationResolvedByResponse(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, 5*time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	// A human responds — the path the §15.1 respond endpoint drives.
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "option-A"
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "option-A") {
			t.Errorf("request_elicitation result = %q, want the human response", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the response")
	}
}

func TestRequestElicitationDismissed(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, 5*time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseDismissed
			return nil
		}); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	select {
	case resp := <-got:
		if text := resultText(t, resp); !strings.Contains(text, "dismissed") {
			t.Errorf("request_elicitation result = %q, want a dismissed result", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return after the dismissal")
	}
}

// TestRequestElicitationTimeout_spec_9_2 verifies the §9.2 line 103
// timeout path returns a structured ELICITATION_TIMEOUT envelope: the
// lenny code lands in the lenny/error content block and the §15.2.1
// classifier resolves it to (TRANSIENT, retryable=false). F-9.2.18.
func TestRequestElicitationTimeout_spec_9_2(t *testing.T) {
	srv, store, _ := newMCPForElicitation(t, 40*time.Millisecond)
	mkSession(t, store, "sess_e", session.StateRunning, "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("a timed-out elicitation should be a tool error: %+v", resp)
	}
	envelope := readLennyErrorEnvelope(t, result)
	if got := envelope["code"]; got != "ELICITATION_TIMEOUT" {
		t.Errorf("envelope.code = %v, want ELICITATION_TIMEOUT", got)
	}
	if got := envelope["category"]; got != "TRANSIENT" {
		t.Errorf("envelope.category = %v, want TRANSIENT", got)
	}
	if got, _ := envelope["retryable"].(bool); got {
		t.Errorf("envelope.retryable = true, want false (the original elicitation is now dismissed)")
	}
	details, _ := envelope["details"].(map[string]any)
	if id, _ := details["elicitationId"].(string); id != "elic_x" {
		t.Errorf("envelope.details.elicitationId = %v, want elic_x", id)
	}
}

func TestRequestElicitationBudgetExceeded(t *testing.T) {
	srv, store, interactions := newMCPForElicitation(t, time.Second)
	mkSession(t, store, "sess_e", session.StateRunning, "")
	// Fill the default §9.1 per-session elicitation budget (50).
	for i := 0; i < 50; i++ {
		if err := interactions.Put(context.Background(), interactionstore.Interaction{
			ID:        "elic-" + strconv.Itoa(i),
			Kind:      interactionstore.KindElicitation,
			SessionID: "sess_e", TenantID: "acme",
		}); err != nil {
			t.Fatalf("seed elicitation %d: %v", i, err)
		}
	}

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"one too many","schema":{},"elicitationId":"elic_over"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an over-budget elicitation should be a tool error: %+v", resp)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if msg, _ := c0["text"].(string); !strings.Contains(msg, "budget") {
		t.Errorf("error = %q, want an elicitation-budget message", msg)
	}
	// The over-budget elicitation was dropped, not recorded.
	if _, err := interactions.Get(context.Background(), "acme", "sess_e", "", "elic_over"); err == nil {
		t.Error("the dropped elicitation was recorded in the interaction store")
	}
}

// fakeElicitationMetrics records the §9.1 elicitation drop reasons.
type fakeElicitationMetrics struct{ reasons []string }

func (f *fakeElicitationMetrics) RecordElicitationDrop(reason string) {
	f.reasons = append(f.reasons, reason)
}

func TestRequestElicitationDropRecordsMetric(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	rec := &fakeElicitationMetrics{}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                     store,
		Interactions:              interactions,
		ElicitationMetrics:        rec,
		MaxElicitationsPerSession: 1,
		Clock:                     func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:                    func() string { return "elic_gen" },
		TenantID:                  "acme",
	})
	mkSession(t, store, "sess_e", session.StateRunning, "")
	// One elicitation already recorded fills the cap of 1.
	if err := interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_0", Kind: interactionstore.KindElicitation, SessionID: "sess_e", TenantID: "acme",
	}); err != nil {
		t.Fatalf("seed elicitation: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_e","message":"over budget","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("an over-budget elicitation should be a tool error: %+v", resp)
	}
	if len(rec.reasons) != 1 || rec.reasons[0] != "budget_exceeded" {
		t.Errorf("recorded drop reasons = %v, want [budget_exceeded]", rec.reasons)
	}
}

func TestRequestElicitationSuppressedAtDepth(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                      store,
		Interactions:               interactions,
		ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
		ElicitationSuppressAtDepth: 2,
		ElicitationTimeout:         time.Second,
		IDFunc:                     func() string { return "elic_gen" },
		TenantID:                   "acme",
	})
	// A delegation tree root → mid → leaf; the leaf sits at depth 2.
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_mid", session.StateRunning, "sess_root")
	mkSession(t, store, "sess_leaf", session.StateRunning, "sess_mid")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_leaf","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	// §9.2: a suppressed elicitation returns a SUPPRESSED response (not
	// an error) the originating pod handles as "user declined".
	text := resultText(t, resp)
	if !strings.Contains(text, "suppressed") {
		t.Errorf("result = %q, want a suppressed response", text)
	}
	// The suppressed elicitation was not recorded against any session
	// in the chain.
	for _, sid := range []string{"sess_leaf", "sess_mid", "sess_root"} {
		if _, err := interactions.Get(context.Background(), "acme", sid, "", "elic_x"); err == nil {
			t.Errorf("a suppressed elicitation was recorded against %s", sid)
		}
	}
}

func TestRequestElicitationNotSuppressedBelowDepth(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:                      store,
		Interactions:               interactions,
		ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
		ElicitationSuppressAtDepth: 5, // higher than the session's depth
		ElicitationTimeout:         5 * time.Second,
		IDFunc:                     func() string { return "elic_gen" },
		TenantID:                   "acme",
	})
	mkSession(t, store, "sess_root", session.StateRunning, "")
	mkSession(t, store, "sess_mid", session.StateRunning, "sess_root") // depth 1
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_mid","message":"ask","schema":{},"elicitationId":"elic_x"}`)
	}()
	// §9.2: the elicitation was not suppressed below the depth and
	// forwards up the chain to the human-facing root. It is recorded
	// against the chain resolver, sess_root.
	waitElicitation(t, interactions, "sess_root", "elic_x")
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_root", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseResponded
			i.Response = "ok"
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case resp := <-got:
		resultText(t, resp) // a non-error result confirms it was not suppressed
	case <-time.After(3 * time.Second):
		t.Fatal("request_elicitation did not return")
	}
}

func TestRequestElicitationRejectsTerminalSession(t *testing.T) {
	srv, store, _ := newMCPForElicitation(t, time.Second)
	mkSession(t, store, "sess_done", session.StateCompleted, "")

	resp := call(t, srv.Handler(), "lenny/request_elicitation",
		`{"sessionId":"sess_done","message":"x","schema":{},"elicitationId":"elic_x"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("a terminal session should be a tool error: %+v", resp)
	}
}

// TestRequestElicitationPublishesElicitationRequestEvent_spec_7_2 asserts
// that lenny/request_elicitation surfaces the elicitation on the
// resolver session's stream as the canonical §7.2 line 136
// `elicitation_request` event, not the pre-fix `elicitation_requested`
// synonym (which appeared nowhere in the §7.2 catalog).
// spec: §7.2 line 136. F-7.2.17.
func TestRequestElicitationPublishesElicitationRequestEvent_spec_7_2(t *testing.T) {
	store := memstore.New()
	interactions := interactionstore.NewMemory()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:              store,
		Events:             bus,
		Interactions:       interactions,
		ElicitationTimeout: 5 * time.Second,
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "elic_gen" },
		TenantID:           "acme",
	})
	mkSession(t, store, "sess_e", session.StateRunning, "")
	h := srv.Handler()

	done := make(chan map[string]any, 1)
	go func() {
		done <- call(t, h, "lenny/request_elicitation",
			`{"sessionId":"sess_e","message":"pick one","schema":{},"elicitationId":"elic_x"}`)
	}()
	waitElicitation(t, interactions, "sess_e", "elic_x")

	hist := bus.History("sess_e", 0)
	if len(hist) != 1 {
		t.Fatalf("event history has %d events, want 1: %+v", len(hist), hist)
	}
	if hist[0].Type != "elicitation_request" {
		t.Errorf("event type = %q, want elicitation_request (§7.2 line 136 canonical name)", hist[0].Type)
	}
	if !strings.Contains(hist[0].Data, "elic_x") || !strings.Contains(hist[0].Data, "pick one") {
		t.Errorf("event data = %q, want it to carry the elicitationId + message", hist[0].Data)
	}

	// Resolve so the goroutine returns cleanly.
	if _, err := interactions.Resolve(context.Background(), "acme", "sess_e", "", "elic_x",
		func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseDismissed
			return nil
		}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request_elicitation did not return after dismissal")
	}
}

func TestToolsListIncludesLennyTools(t *testing.T) {
	srv, _ := newMCP(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		names[m["name"].(string)] = true
	}
	for _, want := range []string{
		"lenny/create_session", "lenny/send_message",
		"lenny/get_task_tree", "lenny/cancel_child", "lenny/await_children",
		"lenny/discover_agents", "lenny/set_tracing_context",
		"lenny/output", "lenny/request_input", "lenny/request_elicitation",
		"lenny/delegate_task",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %q: %v", want, names)
		}
	}
}

// TestRequestInputPerRuntimeTimeoutOverride asserts the §11.3 /
// §5.1 limits.maxRequestInputWaitSeconds per-runtime wait cap overrides the
// platform default. The platform default is 40ms (which would time the
// call out immediately), but the session's runtime declares a large
// per-runtime cap, so the request stays pending long enough for a peer to
// resolve it.
func TestRequestInputPerRuntimeTimeoutOverride(t *testing.T) {
	store := memstore.New()
	reg := inputwait.NewRegistry()
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "slow-input", Type: runtimestore.TypeAgent, Image: "x@sha256:a",
		Limits: &runtimestore.Limits{MaxRequestInputWaitSeconds: 3600},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		InputWaits:          reg,
		Runtimes:            runtimes,
		RequestInputTimeout: 40 * time.Millisecond,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_r", TenantID: "acme", State: session.StateRunning, RuntimeRef: "slow-input",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_r","requestId":"req-1","parts":[{"type":"text","text":"pick"}]}`)
	}()
	waitPending(t, reg, "sess_r", "req-1")
	// Sleep well past the 40ms platform default to prove the per-runtime
	// override (3600s) kept the request pending.
	time.Sleep(200 * time.Millisecond)
	call(t, h, "lenny/send_message", `{"to":"sess_r","message":"blue","inReplyTo":"req-1"}`)

	select {
	case ri := <-got:
		if text := resultText(t, ri); !strings.Contains(text, `"answer":"blue"`) {
			t.Errorf("request_input result = %q, want answer blue (per-runtime override not applied)", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request_input did not return after resolution")
	}
}
