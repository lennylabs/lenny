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
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
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

// newMCPForInputWithRuntimes builds the MCP server like newMCPForInput
// and also returns the §5.1 runtime registry + a session event bus so
// the §8.8 line 869 `one_shot` tests can seed a runtime and read the
// elicitation_request SSE payload. F-8.8.10.
func newMCPForInputWithRuntimes(t *testing.T, timeout time.Duration) (*mcp.Server, sessionstore.Store, runtimestore.Store, *inputwait.Registry, *sessionevents.Bus) {
	t.Helper()
	store := memstore.New()
	reg := inputwait.NewRegistry()
	runtimes := runtimestore.NewMemory()
	bus := sessionevents.NewBus(0)
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:               store,
		Executor:            executor.NewEchoExecutor(),
		Runtimes:            runtimes,
		Events:              bus,
		InputWaits:          reg,
		RequestInputTimeout: timeout,
		Clock:               func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:              func() string { return "sess_mcp" },
		TenantID:            "acme",
	})
	return srv, store, runtimes, reg, bus
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

// TestSendMessageToolAcceptsContentUnion asserts the §8.5
// lenny/send_message `message` argument is the §15.4 message-input union:
// a bare string and a MessagePart[] array are both accepted and delivered,
// and the text projection echoes back. This is the MCP-side half of the
// §15.2.1 REST/MCP parity the contract suite pins end to end.
// spec: §15.4 (MessageEnvelope.input oneOf(string, MessagePart[])), §15.2.1
// (REST/MCP parity), §8.5 line 537.
func TestSendMessageToolAcceptsContentUnion_spec_15_4(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"bare string", `{"to":"sess_union","message":"hi-bare"}`, "hi-bare"},
		{"single text part", `{"to":"sess_union","message":[{"type":"text","inline":"hi-part"}]}`, "hi-part"},
		{"multipart", `{"to":"sess_union","message":[{"type":"text","inline":"see "},{"type":"image","ref":"lenny-blob://acme/s/p","mimeType":"image/png"}]}`, "see "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, store := newMCP(t)
			now := time.Now()
			_ = store.Create(context.Background(), sessionstore.Session{
				ID: "sess_union", TenantID: "acme", State: session.StateRunning,
				CreatedAt: now, UpdatedAt: now,
			})
			resp := call(t, srv.Handler(), "lenny/send_message", tc.args)
			if e, ok := resp["error"]; ok {
				t.Fatalf("union input %q returned an error: %v", tc.name, e)
			}
			result, _ := resp["result"].(map[string]any)
			content, _ := result["content"].([]any)
			if len(content) < 2 {
				t.Fatalf("send_message must return receipt + echo blocks; got %+v", content)
			}
			first, _ := content[0].(map[string]any)
			var envelope struct {
				DeliveryReceipt session.DeliveryReceipt `json:"deliveryReceipt"`
			}
			receiptJSON, _ := first["text"].(string)
			if err := json.Unmarshal([]byte(receiptJSON), &envelope); err != nil {
				t.Fatalf("first block must be a deliveryReceipt: %v; body=%s", err, receiptJSON)
			}
			if envelope.DeliveryReceipt.Status != session.DeliveryStatusDelivered {
				t.Errorf("union %q status = %q, want delivered", tc.name, envelope.DeliveryReceipt.Status)
			}
			second, _ := content[1].(map[string]any)
			if echo, _ := second["text"].(string); !strings.Contains(echo, tc.want) {
				t.Errorf("union %q echo = %q, want it to contain %q", tc.name, echo, tc.want)
			}
		})
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

// TestGetTaskTreeAcceptsEmptyArgsFromBoundPrincipal_spec_8_9_F_8_9_11
// verifies that lenny/get_task_tree honors the §8.9 lines 615-623 input
// schema (`{"properties":{},"required":[]}`): a spec-conformant caller
// who omits every argument and presents an authenticated principal
// gets the tree rooted at the principal's SessionID. F-8.9.11.
func TestGetTaskTreeAcceptsEmptyArgsFromBoundPrincipal_spec_8_9_F_8_9_11(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_principal_899", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid_899", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_principal_899",
		CreatedAt:       now, UpdatedAt: now,
	})
	principal := authmw.Principal{Subject: "alice", TenantID: "acme", SessionID: "sess_principal_899"}
	resp := callAs(t, srv.Handler(), principal, "lenny/get_task_tree", `{}`)
	text := resultText(t, resp)
	for _, want := range []string{"sess_principal_899", "sess_kid_899"} {
		if !strings.Contains(text, want) {
			t.Errorf("tree missing %q: %q", want, text)
		}
	}
}

// TestGetTaskTreeRejectsUnboundEmptyArgs_spec_8_9_F_8_9_11 verifies the
// negative path: a caller with neither a principal-bound SessionID nor
// a transport-fallback sessionId argument is rejected with
// VALIDATION_ERROR rather than silently selecting an arbitrary tree.
// F-8.9.11.
func TestGetTaskTreeRejectsUnboundEmptyArgs_spec_8_9_F_8_9_11(t *testing.T) {
	srv, _ := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError result; got %+v", resp)
	}
}

// TestGetTaskTreeInputSchemaHasNoRequired_spec_8_9_F_8_9_11 verifies
// the registered §8.9 input schema declares no required fields — the
// caller-implicit identification rule the spec at lines 615-623 sets.
// F-8.9.11.
func TestGetTaskTreeInputSchemaHasNoRequired_spec_8_9_F_8_9_11(t *testing.T) {
	srv, _ := newMCP(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] != "lenny/get_task_tree" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		required, _ := schema["required"].([]any)
		if len(required) != 0 {
			t.Errorf("get_task_tree inputSchema.required = %v, want [] (§8.9 lines 615-623)", required)
		}
		return
	}
	t.Fatalf("lenny/get_task_tree not in tools/list")
}

// TestGetTaskTreeEmitsCycleObservation_spec_8_9_F_8_9_10 verifies that
// the MCP walker fires the TreeCycleObserver when the persistent
// store has been corrupted into a ParentSessionID cycle (the §8.2
// pre-delegation detector is bypassed in this defensive path). The
// walker still returns a well-formed (truncated) tree so the response
// stays serializable. F-8.9.10.
func TestGetTaskTreeEmitsCycleObservation_spec_8_9_F_8_9_10(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	// A two-node cycle: sess_a.parent = sess_b, sess_b.parent = sess_a.
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_a", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_b",
		CreatedAt:       now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_b", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_a",
		CreatedAt:       now, UpdatedAt: now,
	})
	observer := &captureTreeCycle{}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:             store,
		TenantID:          "acme",
		TreeCycleObserver: observer,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_a"}`)
	if result, _ := resp["result"].(map[string]any); result == nil || result["isError"] == true {
		t.Fatalf("expected non-error result; got %+v", resp)
	}
	if len(observer.events) == 0 {
		t.Fatalf("expected at least one TreeCycleEvent; got none")
	}
	got := observer.events[0]
	if got.TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme", got.TenantID)
	}
	if got.Source != "mcp" {
		t.Errorf("Source = %q, want mcp", got.Source)
	}
	if got.RootSessionID != "sess_a" {
		t.Errorf("RootSessionID = %q, want sess_a", got.RootSessionID)
	}
	if got.CycleNodeID == "" {
		t.Errorf("CycleNodeID is empty; want the repeated node id")
	}
}

// TestGetTaskTreeAcyclicTreeEmitsNoCycle_spec_8_9_F_8_9_10 verifies
// the negative: a clean tree never fires the cycle observer. F-8.9.10.
func TestGetTaskTreeAcyclicTreeEmitsNoCycle_spec_8_9_F_8_9_10(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root_cl", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid_cl", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_root_cl",
		CreatedAt:       now, UpdatedAt: now,
	})
	observer := &captureTreeCycle{}
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:             store,
		TenantID:          "acme",
		TreeCycleObserver: observer,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root_cl"}`)
	if result, _ := resp["result"].(map[string]any); result == nil || result["isError"] == true {
		t.Fatalf("expected non-error result; got %+v", resp)
	}
	if len(observer.events) != 0 {
		t.Errorf("acyclic tree produced %d cycle events; want 0", len(observer.events))
	}
}

// captureTreeCycle is a §8.9 TreeCycleObserver fake that records
// every observation so a test can assert emission shape. F-8.9.10.
type captureTreeCycle struct {
	events []mcptools.TreeCycleEvent
}

func (c *captureTreeCycle) OnTreeCycle(_ context.Context, ev mcptools.TreeCycleEvent) {
	c.events = append(c.events, ev)
}

// TestDelegateTaskRejectsMCPTargetSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10
// verifies that the §8.2 line 50 type rejection in `lenny/delegate_task`
// surfaces TARGET_NOT_AN_AGENT through the §15.2.1 lenny error envelope
// rather than falling back to INTERNAL_ERROR. spec: §15.2.1 rule 3
// (shared error taxonomy); F-8.5.10.
func TestDelegateTaskRejectsMCPTargetSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10(t *testing.T) {
	srv, store, rt := newMCPWithRuntimes(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent_810", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = rt.Create(context.Background(), runtimestore.Runtime{
		Name: "fs-mcp", Type: runtimestore.TypeMCP, Image: "lenny/fs-mcp@sha256:abc",
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent_810","target":"fs-mcp","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "TARGET_NOT_AN_AGENT" {
		t.Errorf("envelope.code = %v, want TARGET_NOT_AN_AGENT", env["code"])
	}
	if env["category"] != "POLICY" {
		t.Errorf("envelope.category = %v, want POLICY", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("envelope.retryable = %v, want false", env["retryable"])
	}
}

// TestRequestInputTimeoutSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10
// verifies the §8.5 row `lenny/request_input` timeout surfaces
// REQUEST_INPUT_TIMEOUT through the lenny envelope. spec: §15.2.1
// rule 3; F-8.5.10.
func TestRequestInputTimeoutSurfacesEnvelopeCode_spec_15_2_1_F_8_5_10(t *testing.T) {
	srv, store, _ := newMCPForInput(t, 20*time.Millisecond)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_in", TenantID: "acme", State: session.StateRunning,
	})
	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_in","requestId":"req-x","parts":[{"type":"text","text":"hi"}]}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "REQUEST_INPUT_TIMEOUT" {
		t.Errorf("envelope.code = %v, want REQUEST_INPUT_TIMEOUT", env["code"])
	}
	if env["category"] != "TRANSIENT" {
		t.Errorf("envelope.category = %v, want TRANSIENT", env["category"])
	}
	if env["retryable"] != false {
		t.Errorf("envelope.retryable = %v, want false", env["retryable"])
	}
}

// TestToolErrorsCarryCanonicalEnvelopeCode asserts the §15.2.12
// conversion: tool error paths that previously returned a plain Go
// error (and so fell back to INTERNAL_ERROR / TRANSIENT / retryable=true
// in handleToolCall) now return *mcp.ToolError carrying the canonical
// lenny code, so the §15.2.1 rule 5(d) (category, retryable) pair
// matches the REST surface. spec: §15.2.1 rule 5(d) line 1396. F-15.2.12.
func TestToolErrorsCarryCanonicalEnvelopeCode(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	mustCreate := func(id string, st session.State) {
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: id, TenantID: "acme", State: st, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	mustCreate("sess_run", session.StateRunning)
	mustCreate("sess_done", session.StateCompleted)
	h := srv.Handler()

	cases := []struct {
		name      string
		tool      string
		args      string
		wantCode  string
		wantCat   string
		wantRetry bool
	}{
		// spec: 15:998 — a message to a terminal target is TARGET_TERMINAL.
		{"send_message_terminal_target", "lenny/send_message", `{"to":"sess_done","message":"x"}`, "TARGET_TERMINAL", "PERMANENT", false},
		// spec: 15:981 — a message to an unknown session is RESOURCE_NOT_FOUND.
		{"send_message_unknown_target", "lenny/send_message", `{"to":"sess_missing","message":"x"}`, "RESOURCE_NOT_FOUND", "PERMANENT", false},
		// spec: 15:978 — a missing required argument is VALIDATION_ERROR.
		{"set_tracing_missing_sessionid", "lenny/set_tracing_context", `{"context":{"k":"v"}}`, "VALIDATION_ERROR", "PERMANENT", false},
		// spec: 15:980 — output to the caller's own terminal session is INVALID_STATE_TRANSITION.
		{"output_terminal_session", "lenny/output", `{"sessionId":"sess_done","output":[{"type":"text","text":"x"}]}`, "INVALID_STATE_TRANSITION", "PERMANENT", false},
		// spec: 15:978 — await_children with empty childIds is VALIDATION_ERROR.
		{"await_children_missing_childids", "lenny/await_children", `{"sessionId":"sess_run","childIds":[]}`, "VALIDATION_ERROR", "PERMANENT", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := call(t, h, c.tool, c.args)
			result, _ := resp["result"].(map[string]any)
			env := readLennyErrorEnvelope(t, result)
			if env["code"] != c.wantCode {
				t.Errorf("code = %v, want %s", env["code"], c.wantCode)
			}
			if env["category"] != c.wantCat {
				t.Errorf("category = %v, want %s", env["category"], c.wantCat)
			}
			if env["retryable"] != c.wantRetry {
				t.Errorf("retryable = %v, want %v", env["retryable"], c.wantRetry)
			}
		})
	}
}

// TestDelegateTaskRejectsInsideInterceptorWeakeningCooldown_spec_8_3_181
// verifies that the §8.3 line 181 cluster-scoped weakening cooldown
// rejects every `delegate_task` whose effective DelegationPolicy is
// the freshly-weakened row, surfacing INTERCEPTOR_WEAKENING_COOLDOWN
// (TRANSIENT, HTTP 503) through the §15.2.1 lenny envelope with
// details.policyName + details.retryAfterSeconds. F-8.7.12 /
// F-13.5.7 / F-8.5.10.
func TestDelegateTaskRejectsInsideInterceptorWeakeningCooldown_spec_8_3_181(t *testing.T) {
	store := memstore.New()
	runtimes := runtimestore.NewMemory()
	policies := delegationpolicystore.NewMemory()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent_cd", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "gemini", Image: "lenny/gemini@sha256:abc",
		DelegationPolicyRef: "scan-policy",
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := policies.Create(context.Background(), delegationpolicystore.DelegationPolicy{
		TenantID: "acme", Name: "scan-policy",
		ContentPolicy: delegationpolicystore.ContentPolicy{
			InterceptorRef: "guardrails", ScanExportedFiles: false,
		},
		// 30 s into a 60 s cooldown window — retryAfter must surface 30.
		ScanExportedFilesWeakenedAt: now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	svc := delegation.NewService(store, delegation.Options{
		Runtimes:                     runtimes,
		Policies:                     policies,
		Clock:                        func() time.Time { return now },
		IDFunc:                       func() string { return "sess_child" },
		InterceptorWeakeningCooldown: 60 * time.Second,
	})

	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:      store,
		Executor:   executor.NewEchoExecutor(),
		Runtimes:   runtimes,
		Delegation: svc,
		Clock:      func() time.Time { return now },
		IDFunc:     func() string { return "sess_mcp" },
		TenantID:   "acme",
	})

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent_cd","target":"gemini","poolRef":"pool-b"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INTERCEPTOR_WEAKENING_COOLDOWN" {
		t.Errorf("envelope.code = %v, want INTERCEPTOR_WEAKENING_COOLDOWN", env["code"])
	}
	if env["category"] != "TRANSIENT" {
		t.Errorf("envelope.category = %v, want TRANSIENT", env["category"])
	}
	if env["retryable"] != true {
		t.Errorf("envelope.retryable = %v, want true", env["retryable"])
	}
	details, _ := env["details"].(map[string]any)
	if details == nil {
		t.Fatalf("envelope.details missing: %+v", env)
	}
	if details["policyName"] != "scan-policy" {
		t.Errorf("details.policyName = %v, want scan-policy", details["policyName"])
	}
	// JSON numbers decode as float64.
	if details["retryAfterSeconds"] != float64(30) {
		t.Errorf("details.retryAfterSeconds = %v, want 30", details["retryAfterSeconds"])
	}
	if details["cooldownSeconds"] != float64(60) {
		t.Errorf("details.cooldownSeconds = %v, want 60", details["cooldownSeconds"])
	}
}

// TestRequestInputTimeoutCarriesExpiredAt_spec_11_3_238 verifies the
// §11.3 line 238 timeout error envelope details include the ISO 8601
// `expiredAt` timestamp plus `requestId` and `timeoutSeconds`. F-11.3.23.
func TestRequestInputTimeoutCarriesExpiredAt_spec_11_3_238(t *testing.T) {
	srv, store, _ := newMCPForInput(t, 10*time.Millisecond)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_t1", TenantID: "acme", State: session.StateRunning,
	})
	resp := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_t1","requestId":"req-t1","parts":[{"type":"text","text":"hi"}]}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "REQUEST_INPUT_TIMEOUT" {
		t.Fatalf("envelope.code = %v, want REQUEST_INPUT_TIMEOUT", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details == nil {
		t.Fatalf("envelope.details missing: %+v", env)
	}
	if got := details["requestId"]; got != "req-t1" {
		t.Errorf("details.requestId = %v, want req-t1", got)
	}
	if _, ok := details["timeoutSeconds"]; !ok {
		t.Errorf("details.timeoutSeconds missing: %+v", details)
	}
	rawExpired, ok := details["expiredAt"].(string)
	if !ok || rawExpired == "" {
		t.Fatalf("details.expiredAt missing or not a string: %+v", details)
	}
	// The MCP test rig pins Clock to 2026-01-01 UTC; the test asserts
	// the timestamp parses as RFC3339Nano and matches the injected
	// clock, exercising the round-trip from clock() through
	// time.RFC3339Nano formatting.
	expiredAt, err := time.Parse(time.RFC3339Nano, rawExpired)
	if err != nil {
		t.Fatalf("details.expiredAt not RFC3339Nano: %v (got %q)", err, rawExpired)
	}
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !expiredAt.Equal(want) {
		t.Errorf("details.expiredAt = %v, want %v", expiredAt, want)
	}
}

// TestGetTaskTreeIncludesRuntimeRef_spec_8_5_F_8_5_1 verifies that
// `lenny/get_task_tree` surfaces `runtimeRef` on every node, matching
// the REST `/tree` projection (§15.2.1 REST↔MCP semantic equivalence).
// spec: §8.5 line 530. F-8.5.1.
func TestGetTaskTreeIncludesRuntimeRef_spec_8_5_F_8_5_1(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root2", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "claude",
		CreatedAt:  now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid2", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "gemini", ParentSessionID: "sess_root2",
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root2"}`)
	text := resultText(t, resp)
	for _, want := range []string{`"runtimeRef":"claude"`, `"runtimeRef":"gemini"`} {
		if !strings.Contains(text, want) {
			t.Errorf("tree response missing %q: %q", want, text)
		}
	}
}

// TestGetTaskTreeUsesTaskIDField_spec_8_5_540 verifies that the §8.5
// MCP `lenny/get_task_tree` node uses the `taskId` wire field rather
// than `sessionId`. spec: §8.5 line 540 — "Each node includes
// `taskId`, `state`, and `runtimeRef`". F-8.9.5.
func TestGetTaskTreeUsesTaskIDField_spec_8_5_540(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root_tid", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "claude",
		CreatedAt:  now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid_tid", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "gemini", ParentSessionID: "sess_root_tid",
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root_tid"}`)
	text := resultText(t, resp)
	for _, want := range []string{`"taskId":"sess_root_tid"`, `"taskId":"sess_kid_tid"`} {
		if !strings.Contains(text, want) {
			t.Errorf("tree node missing %s: %q", want, text)
		}
	}
	if strings.Contains(text, `"sessionId":"sess_root_tid"`) || strings.Contains(text, `"sessionId":"sess_kid_tid"`) {
		t.Errorf("tree node leaked legacy `sessionId` field instead of §8.5 line 540 `taskId`: %q", text)
	}
}

// TestGetTaskTreeSurfacesNodeAttributes_spec_8_9_1010 verifies that the
// §8.9 line 1010 per-node tracking attributes (generation, pod, lease,
// failure history) ride on each MCP tree node so a parent agent can
// inspect a child's recovery generation, pod assignment, granted lease,
// and failure history. F-8.9.1.
func TestGetTaskTreeSurfacesNodeAttributes_spec_8_9_1010(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root_attr", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "claude", CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_kid_attr", TenantID: "acme", State: session.StateFailed,
		RuntimeRef: "gemini", ParentSessionID: "sess_root_attr",
		RecoveryGeneration: 2, PodAssignment: "pod-xyz",
		DelegationLease: &sessionstore.DelegationLease{MaxTokenBudget: 5000},
		RetryCount:      1, FailureClass: session.FailureClass("infrastructure"),
		FailureReason: "READY_TIMEOUT",
		CreatedAt:     now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root_attr"}`)
	text := resultText(t, resp)
	for _, want := range []string{
		`"attributes":`, `"generation":2`, `"pod":"pod-xyz"`,
		`"maxTokenBudget":5000`, `"retryCount":1`,
		`"failureClass":"infrastructure"`, `"failureReason":"READY_TIMEOUT"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("tree node missing %s: %q", want, text)
		}
	}
}

func TestDelegateTaskTool(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", UserID: "user_alice",
		State:      session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
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
		CreatedAt: now, UpdatedAt: now,
	})
	// Delegating back to the parent's own (runtime, pool) is a cycle.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"claude","poolRef":"pool-a"}`)
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
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
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
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b"}`)
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

// TestCancelChildTool covers the default `cancel_all` policy: every
// non-terminal node reachable through a chain of `cancel_all` ancestors
// is cancelled. A terminal ancestor's own cascade ran when it settled,
// so the traversal does NOT descend through it — sess_a2x stays
// running below the already-terminal sess_a2.
// spec: §8.5 row for `cancel_child` ("cascades to its descendants per
// policy"); §8.10 lines 1066-1076. F-8.5.19.
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
	for _, want := range []string{"sess_a", "sess_a1"} {
		if !strings.Contains(text, want) {
			t.Errorf("cancel_child result %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "sess_a2x") {
		t.Errorf("cancel_child reached sess_a2x through a terminal ancestor: %q", text)
	}

	// Non-terminal descendants reachable through cancel_all ancestors are
	// cancelled. The terminal sess_a2 keeps its state and its cascade
	// does not run again — sess_a2x stays running below it.
	for _, id := range []string{"sess_a", "sess_a1"} {
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
	// sess_a2x lives below a terminal node so its cascade does not run.
	if row, _ := store.Get(context.Background(), "acme", "sess_a2x"); row.State != session.StateRunning {
		t.Errorf("sess_a2x state = %q, want running (under terminal sess_a2)", row.State)
	}
	// The calling parent and the untouched sibling stay running.
	for _, id := range []string{"sess_root", "sess_b"} {
		if row, _ := store.Get(context.Background(), "acme", id); row.State != session.StateRunning {
			t.Errorf("%s state = %q, want running unchanged", id, row.State)
		}
	}
}

// TestCancelChildToolHonoursAwaitCompletion verifies that
// `await_completion` on the cancelled node leaves its descendants
// running. spec: §8.10 lines 1066-1076. F-8.5.19.
func TestCancelChildToolHonoursAwaitCompletion(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_target", TenantID: "acme", State: session.StateRunning,
		ParentSessionID:  "sess_root",
		CascadeOnFailure: session.CascadeAwaitCompletion,
		CreatedAt:        now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed sess_target: %v", err)
	}
	mkSession(t, store, "sess_kid", session.StateRunning, "sess_target")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_target"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_target") {
		t.Errorf("cancel_child result %q missing target", text)
	}
	if strings.Contains(text, "sess_kid") {
		t.Errorf("await_completion should not cascade to sess_kid: %q", text)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_target"); row.State != session.StateCancelled {
		t.Errorf("sess_target state = %q, want cancelled", row.State)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_kid"); row.State != session.StateRunning {
		t.Errorf("sess_kid state = %q, want running (await_completion)", row.State)
	}
}

// TestCancelChildToolHonoursDetach verifies that `detach` on the
// cancelled node leaves its descendants running. spec: §8.10 lines
// 1066-1076. F-8.5.19.
func TestCancelChildToolHonoursDetach(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root", session.StateRunning, "")
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_target", TenantID: "acme", State: session.StateRunning,
		ParentSessionID:  "sess_root",
		CascadeOnFailure: session.CascadeDetach,
		CreatedAt:        now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed sess_target: %v", err)
	}
	mkSession(t, store, "sess_orphan", session.StateRunning, "sess_target")

	resp := call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_root","childSessionId":"sess_target"}`)
	text := resultText(t, resp)
	if strings.Contains(text, "sess_orphan") {
		t.Errorf("detach should leave sess_orphan alone: %q", text)
	}
	if row, _ := store.Get(context.Background(), "acme", "sess_orphan"); row.State != session.StateRunning {
		t.Errorf("sess_orphan state = %q, want running (detach)", row.State)
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

// TestRequestInputOneShotRejectsSecondCall_spec_8_8_869 verifies the
// §8.8 line 869 enforcement: a `one_shot` runtime's first
// lenny/request_input call lands; the second is rejected with
// ONE_SHOT_INPUT_EXHAUSTED. F-8.8.10.
func TestRequestInputOneShotRejectsSecondCall_spec_8_8_869(t *testing.T) {
	srv, store, runtimes, reg, _ := newMCPForInputWithRuntimes(t, 50*time.Millisecond)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt_one", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionOneShot,
		},
	}); err != nil {
		t.Fatalf("Create runtime: %v", err)
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_os", TenantID: "acme", State: session.StateRunning, RuntimeRef: "rt_one",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	// First call: lands, then times out (50ms request_input timeout). The
	// timeout is what we want — it lets the call return so we can issue
	// the second call. The Consumed counter has bumped to 1.
	resp1 := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_os","requestId":"req-1","parts":[{"type":"text","text":"go?"}]}`)
	result1, _ := resp1["result"].(map[string]any)
	env1 := readLennyErrorEnvelope(t, result1)
	if env1["code"] != "REQUEST_INPUT_TIMEOUT" {
		t.Fatalf("first call should land then timeout; got code %v", env1["code"])
	}
	if got := reg.Consumed("sess_os"); got != 1 {
		t.Fatalf("Consumed after first call = %d, want 1", got)
	}
	// Second call: rejected with ONE_SHOT_INPUT_EXHAUSTED — the channel
	// is never allocated so the Consumed counter does NOT bump again.
	resp2 := call(t, srv.Handler(), "lenny/request_input",
		`{"sessionId":"sess_os","requestId":"req-2","parts":[{"type":"text","text":"again?"}]}`)
	result2, _ := resp2["result"].(map[string]any)
	env2 := readLennyErrorEnvelope(t, result2)
	if env2["code"] != "ONE_SHOT_INPUT_EXHAUSTED" {
		t.Errorf("second call code = %v, want ONE_SHOT_INPUT_EXHAUSTED (§8.8 line 869)", env2["code"])
	}
	details, _ := env2["details"].(map[string]any)
	if details == nil || details["maxInputRounds"] == nil {
		t.Errorf("details = %v, want maxInputRounds: 1", details)
	}
	if got := reg.Consumed("sess_os"); got != 1 {
		t.Errorf("Consumed after rejected second call = %d, want 1 (rejection must not bump)", got)
	}
}

// TestRequestInputMultiTurnAllowsRepeat_spec_8_8_869 verifies a
// `multi_turn` runtime is NOT subject to the §8.8 one_shot constraint:
// a second request_input lands normally. F-8.8.10.
func TestRequestInputMultiTurnAllowsRepeat_spec_8_8_869(t *testing.T) {
	srv, store, runtimes, _, _ := newMCPForInputWithRuntimes(t, 30*time.Millisecond)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt_mt", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionMultiTurn,
		},
	}); err != nil {
		t.Fatalf("Create runtime: %v", err)
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_mt", TenantID: "acme", State: session.StateRunning, RuntimeRef: "rt_mt",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	for i := 0; i < 2; i++ {
		args := fmt.Sprintf(`{"sessionId":"sess_mt","requestId":"req-%d","parts":[{"type":"text","text":"q"}]}`, i)
		resp := call(t, srv.Handler(), "lenny/request_input", args)
		result, _ := resp["result"].(map[string]any)
		env := readLennyErrorEnvelope(t, result)
		// Both should time out (no responder), neither should be ONE_SHOT_INPUT_EXHAUSTED.
		if env["code"] == "ONE_SHOT_INPUT_EXHAUSTED" {
			t.Errorf("multi_turn runtime got ONE_SHOT_INPUT_EXHAUSTED on call %d (§8.8 line 869 only applies to one_shot)", i)
		}
	}
}

// TestRequestInputOneShotStampsMaxInputRoundsOnEvent_spec_8_8_869
// verifies the §8.8 line 869 elicitation_request annotation: a
// `one_shot` runtime's first request publishes the event with
// `metadata.maxInputRounds: 1` so client renderers see the constraint
// alongside the question. A `multi_turn` runtime emits no such
// metadata field. F-8.8.10.
func TestRequestInputOneShotStampsMaxInputRoundsOnEvent_spec_8_8_869(t *testing.T) {
	srv, store, runtimes, reg, bus := newMCPForInputWithRuntimes(t, 30*time.Millisecond)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "rt_one2", Type: runtimestore.TypeAgent,
		Capabilities: &runtimestore.RuntimeCapabilities{
			Interaction: runtimestore.InteractionOneShot,
		},
	}); err != nil {
		t.Fatalf("Create runtime: %v", err)
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_os2", TenantID: "acme", State: session.StateRunning, RuntimeRef: "rt_one2",
	}); err != nil {
		t.Fatalf("Create session: %v", err)
	}
	h := srv.Handler()
	done := make(chan map[string]any, 1)
	go func() {
		done <- call(t, h, "lenny/request_input",
			`{"sessionId":"sess_os2","requestId":"req-os2","parts":[{"type":"text","text":"pick"}]}`)
	}()
	waitPending(t, reg, "sess_os2", "req-os2")

	hist := bus.History("sess_os2", 0)
	if len(hist) != 1 {
		t.Fatalf("event history len = %d, want 1: %+v", len(hist), hist)
	}
	if !strings.Contains(hist[0].Data, `"maxInputRounds":1`) {
		t.Errorf("event data = %q, want metadata.maxInputRounds:1 for one_shot runtime", hist[0].Data)
	}
	reg.Cancel("sess_os2", "req-os2")
	select {
	case <-done:
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

// TestSendMessageTopologyAcceptsSibling — under `siblings` scope two
// sessions sharing a parent can message each other. The default `direct`
// scope denies this (see TestSendMessageScopeDirectRejectsSibling); the
// deployment must opt in to `siblings`.
// spec: §7.2 line 241; F-7.2.6, F-7.2.22.
func TestSendMessageTopologyAcceptsSibling(t *testing.T) {
	srv, store := newMCPMessaging(t, session.MessagingScopeSiblings, session.MessagingScopeSiblings, mcptools.MessagingRateLimit{})
	mkSession(t, store, "sess_parent", session.StateRunning, "")
	mkSession(t, store, "sess_a", session.StateRunning, "sess_parent")
	mkSession(t, store, "sess_b", session.StateRunning, "sess_parent")
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"to":"sess_a","message":"hi","fromSessionId":"sess_b"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		c0, _ := content[0].(map[string]any)
		t.Fatalf("sibling→sibling under `siblings` scope should be admitted; got error %v", c0["text"])
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

// TestAwaitChildrenYieldsInputRequiredPartial_spec_8_8_951 covers the
// §8.8 lines 951-971 contract: when an awaited child is blocked on a
// lenny/request_input round, lenny/await_children returns a partial
// result carrying the child's requestId and question parts instead of
// blocking until the child settles. F-8.5.5 / F-8.8.5.
func TestAwaitChildrenYieldsInputRequiredPartial_spec_8_8_951(t *testing.T) {
	srv, store, reg := newMCPForInput(t, time.Second)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c1", session.StateRunning, "sess_p")
	mkSession(t, store, "sess_c2", session.StateRunning, "sess_p")

	// sess_c1 is blocked on input; sess_c2 is merely running. The
	// partial must name sess_c1 with its requestId and question.
	parts := []json.RawMessage{json.RawMessage(`{"type":"text","text":"which branch?"}`)}
	if _, err := reg.Register("sess_c1", "req_001", parts); err != nil {
		t.Fatalf("seed pending input: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c1","sess_c2"],"mode":"all"}`)
	text := resultText(t, resp)

	var body struct {
		Partial       bool `json:"partial"`
		InputRequired []struct {
			ChildID   string            `json:"childId"`
			State     string            `json:"state"`
			RequestID string            `json:"requestId"`
			Parts     []json.RawMessage `json:"parts"`
		} `json:"inputRequired"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("decode partial %q: %v", text, err)
	}
	if !body.Partial {
		t.Fatalf("partial flag = false, want true; body=%q", text)
	}
	if len(body.InputRequired) != 1 {
		t.Fatalf("inputRequired len = %d, want 1; body=%q", len(body.InputRequired), text)
	}
	ir := body.InputRequired[0]
	if ir.ChildID != "sess_c1" || ir.RequestID != "req_001" || ir.State != "input_required" {
		t.Errorf("partial = %+v, want childId=sess_c1 requestId=req_001 state=input_required", ir)
	}
	if len(ir.Parts) != 1 || string(ir.Parts[0]) != `{"type":"text","text":"which branch?"}` {
		t.Errorf("partial parts = %v, want the registered question", ir.Parts)
	}
}

// TestAwaitChildrenResolvedInputDoesNotYieldPartial_spec_8_8_951
// verifies that once the pending input is resolved (and the children
// settle), await_children returns the terminal results rather than a
// stale input_required partial. F-8.8.5.
func TestAwaitChildrenResolvedInputDoesNotYieldPartial_spec_8_8_951(t *testing.T) {
	srv, store, reg := newMCPForInput(t, time.Second)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c1", session.StateCompleted, "sess_p")
	// A pending entry for an already-settled child must not block the
	// terminal result; resolve it so the registry surface is clean.
	if _, err := reg.Register("sess_c1", "req_001", nil); err != nil {
		t.Fatalf("seed pending input: %v", err)
	}
	if err := reg.Resolve("sess_c1", "req_001", "ans"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c1"],"mode":"all"}`)
	text := resultText(t, resp)
	if strings.Contains(text, "inputRequired") {
		t.Fatalf("settled await returned an input_required partial: %q", text)
	}
	if !strings.Contains(text, "completed") {
		t.Errorf("await result %q missing the terminal completed state", text)
	}
}

// TestAwaitChildrenAnyReturnsFirstChronologicallySettled_spec_8_8_945
// asserts that `any` mode returns the child that reached terminal first
// by wall clock, not the first-listed terminal child. Two siblings are
// settled in reverse order vs childIDs: sess_late is listed first and
// settles second; sess_early is listed second and settles first. F-8.8.12.
func TestAwaitChildrenAnyReturnsFirstChronologicallySettled_spec_8_8_945(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_late", session.StateRunning, "sess_p")
	mkSession(t, store, "sess_early", session.StateRunning, "sess_p")

	// sess_early settles first — its UpdatedAt records the earlier instant.
	if _, err := store.Update(context.Background(), "acme", "sess_early",
		func(row *sessionstore.Session) error { row.State = session.StateCompleted; return nil }); err != nil {
		t.Fatalf("settle sess_early: %v", err)
	}
	// A measurable gap so wall-clock advances regardless of host clock
	// granularity. The store's monotonicNext clamps to ≥+1ns regardless;
	// sleeping here keeps the two terminal timestamps visibly distinct.
	time.Sleep(2 * time.Millisecond)
	if _, err := store.Update(context.Background(), "acme", "sess_late",
		func(row *sessionstore.Session) error { row.State = session.StateCompleted; return nil }); err != nil {
		t.Fatalf("settle sess_late: %v", err)
	}

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_late","sess_early"],"mode":"any"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "sess_early") {
		t.Errorf("await_children any %q should return sess_early — settled first by wall clock", text)
	}
	if strings.Contains(text, "sess_late") {
		t.Errorf("await_children any %q should not include sess_late — it settled second", text)
	}
}

// TestAwaitChildrenSettledAliasesAll_spec_8_8_945 asserts that the
// `settled` mode is the alias for `all` named at §8.8 line 945 — both
// require every child terminal and return the full set. F-8.8.12.
func TestAwaitChildrenSettledAliasesAll_spec_8_8_945(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c1", session.StateCompleted, "sess_p")
	mkSession(t, store, "sess_c2", session.StateCompleted, "sess_p")

	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c1","sess_c2"],"mode":"settled"}`)
	text := resultText(t, resp)
	for _, want := range []string{"sess_c1", "sess_c2", "completed"} {
		if !strings.Contains(text, want) {
			t.Errorf("settled mode %q missing %q — settled must alias all and return the full set", text, want)
		}
	}
}

// TestAwaitChildrenSettledBlocksUntilAll_spec_8_8_945 confirms `settled`
// behaves identically to `all`: a single non-terminal child blocks both
// modes. F-8.8.12.
func TestAwaitChildrenSettledBlocksUntilAll_spec_8_8_945(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_done", session.StateCompleted, "sess_p")
	mkSession(t, store, "sess_running", session.StateRunning, "sess_p")
	h := srv.Handler()

	got := make(chan map[string]any, 1)
	go func() {
		got <- call(t, h, "lenny/await_children",
			`{"sessionId":"sess_p","childIds":["sess_done","sess_running"],"mode":"settled"}`)
	}()
	time.Sleep(60 * time.Millisecond)
	select {
	case <-got:
		t.Fatal("settled mode returned before every child reached a terminal state")
	default:
	}

	if _, err := store.Update(context.Background(), "acme", "sess_running",
		func(row *sessionstore.Session) error { row.State = session.StateCompleted; return nil }); err != nil {
		t.Fatalf("settle child: %v", err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("settled mode did not return after the last child settled")
	}
}

// awaitResults parses the lenny/await_children JSON body into the §8.8
// TaskResult list the tool returns. F-8.8.2 / F-8.8.4.
func awaitResults(t *testing.T, text string) []sessionrecord.Result {
	t.Helper()
	var body struct {
		Results []sessionrecord.Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("decode await_children body %q: %v", text, err)
	}
	return body.Results
}

// TestAwaitChildrenFailedSurfacesErrorBlock_spec_8_8_4 asserts a failed
// child's §8.8 TaskResult.error carries the code, the §15.2.1 classifier
// category, and retriesExhausted sourced from the row's retry budget.
// F-8.8.4.
func TestAwaitChildrenFailedSurfacesErrorBlock_spec_8_8_4(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	// A failed child that consumed its whole automatic-recovery budget.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_fail", TenantID: "acme", State: session.StateFailed, ParentSessionID: "sess_p",
		FailureReason: "DELEGATION_BUDGET_EXHAUSTED", RetryCount: 2,
		RetryPolicy: &session.RetryPolicy{MaxRetries: 2},
		CreatedAt:   time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed failed child: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_fail"],"mode":"all"}`)
	results := awaitResults(t, resultText(t, resp))
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("await_children = %+v, want one result with an error block", results)
	}
	e := results[0].Error
	if e.Code != "DELEGATION_BUDGET_EXHAUSTED" || e.Category != "POLICY" || !e.RetriesExhausted {
		t.Errorf("error block = %+v, want code=DELEGATION_BUDGET_EXHAUSTED category=POLICY retriesExhausted=true", e)
	}
	if results[0].State != "failed" {
		t.Errorf("state = %q, want failed", results[0].State)
	}
}

// TestAwaitChildrenUnknownCodeFallsBackToTransient_spec_8_8_4 covers the
// classifier's documented fallback: a terminal child with no
// FailureReason surfaces the per-state CHILD_<STATE> code, which is
// unknown to the §15.2.1 table and resolves to (TRANSIENT) — the §8.8
// RUNTIME_CRASH → TRANSIENT example is exactly this path. retriesExhausted
// is false because the row consumed no retry budget. F-8.8.4.
func TestAwaitChildrenUnknownCodeFallsBackToTransient_spec_8_8_4(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateFailed, "sess_p")
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c"],"mode":"all"}`)
	results := awaitResults(t, resultText(t, resp))
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("await_children = %+v, want one errored result", results)
	}
	e := results[0].Error
	if e.Code != "CHILD_FAILED" || e.Category != "TRANSIENT" || e.RetriesExhausted {
		t.Errorf("error block = %+v, want code=CHILD_FAILED category=TRANSIENT retriesExhausted=false", e)
	}
}

// TestAwaitChildrenPrefersArchivedRichBody_spec_8_8_2 asserts the await
// path prefers the §8.10 archive's richer TaskResult body (output.parts +
// artifactRefs, materialized at settle time where the transcript and
// catalog are in scope) over the row-only projection for a terminal
// child whose live row is still present. F-8.8.2.
func TestAwaitChildrenPrefersArchivedRichBody_spec_8_8_2(t *testing.T) {
	srv, store, archive := newMCPForArchive(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateCompleted, "sess_p")
	rich, _ := json.Marshal(sessionrecord.Result{
		SchemaVersion: sessionrecord.SchemaVersion,
		TaskID:        "sess_c",
		State:         "completed",
		Output: &sessionrecord.Output{
			Parts:        []sessionrecord.MessagePart{sessionrecord.TextPart("ANSWER42")},
			ArtifactRefs: []string{"lenny-blob://acme/workspace/sess_c/part_1"},
		},
	})
	if err := archive.Archive(context.Background(), treearchive.ArchivedNode{
		TenantID: "acme", RootSessionID: "sess_p", NodeSessionID: "sess_c",
		ParentSessionID: "sess_p", State: "completed", Result: string(rich),
		SettledAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed archive: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_c"],"mode":"all"}`)
	results := awaitResults(t, resultText(t, resp))
	if len(results) != 1 || results[0].Output == nil {
		t.Fatalf("await_children = %+v, want one result carrying the archived output", results)
	}
	out := results[0].Output
	if len(out.Parts) != 1 || out.Parts[0].Inline != "ANSWER42" {
		t.Errorf("output.parts = %+v, want the archived ANSWER42 text part", out.Parts)
	}
	if len(out.ArtifactRefs) != 1 || out.ArtifactRefs[0] != "lenny-blob://acme/workspace/sess_c/part_1" {
		t.Errorf("output.artifactRefs = %v, want the archived blob ref", out.ArtifactRefs)
	}
}

// TestArchiveCancelledMCPSpellingAndCategory_spec_8_8_7 asserts a
// cancel-cascade node lands in the §8.10 archive with the §8.8 MCP state
// spelling (`canceled`) and a classifier-populated error block, matching
// the settle-path body so a resumed parent replaying either route sees
// the same value. F-8.8.7 / F-8.8.4.
func TestArchiveCancelledMCPSpellingAndCategory_spec_8_8_7(t *testing.T) {
	srv, store, archive := newMCPForArchive(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_c", session.StateRunning, "sess_p")
	call(t, srv.Handler(), "lenny/cancel_child",
		`{"parentSessionId":"sess_p","childSessionId":"sess_c"}`)
	node, err := archive.GetByNode(context.Background(), "acme", "sess_c")
	if err != nil {
		t.Fatalf("archive GetByNode: %v", err)
	}
	var res sessionrecord.Result
	if err := json.Unmarshal([]byte(node.Result), &res); err != nil {
		t.Fatalf("decode archived body %q: %v", node.Result, err)
	}
	if res.State != "canceled" {
		t.Errorf("archived state = %q, want MCP spelling canceled", res.State)
	}
	if res.Error == nil || res.Error.Code != "CHILD_CANCELLED" || res.Error.Category == "" {
		t.Errorf("archived error = %+v, want CHILD_CANCELLED with a classifier category", res.Error)
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

// TestGetTaskTreeProjectsSessionStateMetadata_spec_8_8_871 verifies
// that the MCP tree walker stamps the §8.8 lines 871-883 supplementary
// metadata on tree nodes: a suspended session surfaces as
// `working + metadata.suspended:true`; a resume_pending session as
// `working + metadata.resuming:true`. F-8.8.9.
func TestGetTaskTreeProjectsSessionStateMetadata_spec_8_8_871(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_root_meta", session.StateRunning, "")
	mkSession(t, store, "sess_susp", session.StateSuspended, "sess_root_meta")
	mkSession(t, store, "sess_rp", session.StateResumePending, "sess_root_meta")
	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_root_meta"}`)
	text := resultText(t, resp)
	// Suspended → working + metadata.suspended:true
	if !strings.Contains(text, `"suspended":true`) {
		t.Errorf("tree response missing metadata.suspended:true for suspended child: %q", text)
	}
	// ResumePending → working + metadata.resuming:true
	if !strings.Contains(text, `"resuming":true`) {
		t.Errorf("tree response missing metadata.resuming:true for resume_pending child: %q", text)
	}
	// State strings are the §8.8 MCP-protocol projection. The Lenny-native
	// "running" string never appears for the root or either child.
	if strings.Contains(text, `"state":"running"`) || strings.Contains(text, `"state":"suspended"`) ||
		strings.Contains(text, `"state":"resume_pending"`) {
		t.Errorf("tree response leaked Lenny-native state names instead of MCP projection: %q", text)
	}
	if !strings.Contains(text, `"state":"working"`) {
		t.Errorf("tree response missing MCP-projection state \"working\" for any node: %q", text)
	}
}

// TestAwaitChildrenMapsStateToMCPSpelling_spec_8_8_867 verifies that
// `lenny/await_children` projects the §8.8 line 857 task-level state
// table at the MCP boundary: `cancelled` → `canceled` (MCP spelling)
// and `expired` → `failed` (with the §8.8 line 867 `expired:*` error
// code prefix when the row carries one). F-8.8.7.
func TestAwaitChildrenMapsStateToMCPSpelling_spec_8_8_867(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_canc", session.StateCancelled, "sess_p")
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_canc"],"mode":"all"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, `"state":"canceled"`) {
		t.Errorf("await_children for cancelled child = %q, want MCP spelling \"canceled\" (§8.8 line 857)", text)
	}
	if strings.Contains(text, `"state":"cancelled"`) {
		t.Errorf("await_children carried Lenny-native spelling \"cancelled\" instead of MCP \"canceled\": %q", text)
	}
}

// TestAwaitChildrenExpiredChildSurfacesFailedWithReasonCode_spec_8_8_867
// verifies the §8.8 line 867 `expired` → `failed` collapse: the state
// field uses MCP `failed`, the error.code carries the spec-prescribed
// `expired:*` prefix (here `expired:deadline` from the watchdog), and
// the Lenny-native `expired` spelling does not leak through. F-8.8.7
// / F-8.8.8.
func TestAwaitChildrenExpiredChildSurfacesFailedWithReasonCode_spec_8_8_867(t *testing.T) {
	srv, store := newMCP(t)
	mkSession(t, store, "sess_p", session.StateRunning, "")
	mkSession(t, store, "sess_exp", session.StateExpired, "sess_p")
	if _, err := store.Update(context.Background(), "acme", "sess_exp",
		func(r *sessionstore.Session) error {
			r.FailureReason = string(session.FailureExpiredDeadline)
			return nil
		}); err != nil {
		t.Fatalf("stamp FailureReason: %v", err)
	}
	resp := call(t, srv.Handler(), "lenny/await_children",
		`{"sessionId":"sess_p","childIds":["sess_exp"],"mode":"all"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, `"state":"failed"`) {
		t.Errorf("expired child state should collapse to \"failed\" per §8.8 line 867: %q", text)
	}
	if strings.Contains(text, `"state":"expired"`) {
		t.Errorf("Lenny-native \"expired\" leaked to MCP boundary: %q", text)
	}
	if !strings.Contains(text, `"code":"expired:deadline"`) {
		t.Errorf("expired child error.code should carry the §8.8 line 867 prefix \"expired:deadline\": %q", text)
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
