// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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
type fakeElicitationMetrics struct{ reasons []string }

func (f *fakeElicitationMetrics) RecordElicitationDrop(reason string) {
	f.reasons = append(f.reasons, reason)
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
