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
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
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
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Runtimes: runtimes,
		Delegation: delegation.NewService(store, delegation.Options{
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	return srv, store, runtimes
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
		"lenny/get_task_tree", "lenny/cancel_child",
		"lenny/discover_agents", "lenny/delegate_task",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %q: %v", want, names)
		}
	}
}
