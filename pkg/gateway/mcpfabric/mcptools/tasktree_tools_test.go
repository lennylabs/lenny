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
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

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
