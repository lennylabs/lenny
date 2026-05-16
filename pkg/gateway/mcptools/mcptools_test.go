// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
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
		Store:      store,
		Executor:   executor.NewEchoExecutor(),
		Runtimes:   runtimes,
		Events:     events.NewBus(0),
		InputWaits: inputwait.NewRegistry(),
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
func newMCPForOutput(t *testing.T) (*mcp.Server, sessionstore.Store, *events.Bus) {
	t.Helper()
	store := memstore.New()
	bus := events.NewBus(0)
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
	resp := call(t, srv.Handler(), "lenny/send_message", `{"sessionId":"sess_x","content":"ping"}`)
	text := resultText(t, resp)
	if !strings.Contains(text, "ping") {
		t.Errorf("send_message echo: %q", text)
	}
}

func TestSendMessageToolRejectsTerminalSession(t *testing.T) {
	srv, store := newMCP(t)
	now := time.Now()
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_done", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	})
	resp := call(t, srv.Handler(), "lenny/send_message", `{"sessionId":"sess_done","content":"x"}`)
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

	resp := call(t, srv.Handler(), "lenny/send_message", `{"sessionId":"sess_x","content":"ping"}`)
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
	resp := call(t, srv.Handler(), "lenny/send_message", `{"sessionId":"sess_x","content":"ping"}`)
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
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
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
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "claude", PoolRef: "pool-a", IsolationProfile: isolation.ProfileSandboxed,
		CreatedAt: now, UpdatedAt: now,
	})
	// Delegating back to the parent's own (runtime, pool) is a cycle.
	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","runtimeRef":"claude","poolRef":"pool-a"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("cycle should be a tool error: %+v", resp)
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
			`{"sessionId":"sess_i","requestId":"req-1","prompt":"pick a color"}`)
	}()
	waitPending(t, reg, "sess_i", "req-1")

	resp := call(t, h, "lenny/send_message",
		`{"sessionId":"sess_i","content":"blue","inReplyTo":"req-1"}`)
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
		`{"sessionId":"sess_i","requestId":"req-1"}`)
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
		`{"sessionId":"sess_done","requestId":"req-1"}`)
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != true {
		t.Errorf("request_input on a terminal session should be a tool error: %+v", resp)
	}
}

func TestSendMessageInReplyToFallsThroughWithoutPendingInput(t *testing.T) {
	srv, store, _ := newMCPForInput(t, time.Second)
	mkSession(t, store, "sess_i", session.StateRunning, "")

	// inReplyTo references no pending request — the message is an
	// ordinary threaded message and is delivered to the runtime.
	resp := call(t, srv.Handler(), "lenny/send_message",
		`{"sessionId":"sess_i","content":"hello","inReplyTo":"req-absent"}`)
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
		"lenny/output", "lenny/request_input", "lenny/delegate_task",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %q: %v", want, names)
		}
	}
}
