// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/executor"
	"github.com/lennylabs/lenny/pkg/gateway/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// vtNode mirrors the get_task_tree wire node for assertion.
type vtNode struct {
	TaskID   string   `json:"taskId"`
	Children []vtNode `json:"children"`
}

func vtCount(n vtNode) int {
	c := 1
	for _, k := range n.Children {
		c += vtCount(k)
	}
	return c
}

func vtSeedVis(t *testing.T, store sessionstore.Store, id, parent string, vis session.TreeVisibility) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		ParentSessionID: parent, RuntimeRef: "echo", TreeVisibility: vis,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestMCPGetTaskTreeFullShowsApex_spec_8_5_540 — a mid-tree caller with
// the default `full` visibility sees the entire tree rooted at the apex.
// F-8.5.2 / F-8.9.2.
func TestMCPGetTaskTreeFullShowsApex_spec_8_5_540(t *testing.T) {
	srv, store := newMCP(t)
	vtSeedVis(t, store, "sess_root", "", session.VisibilityFull)
	vtSeedVis(t, store, "sess_mid", "sess_root", session.VisibilityFull)
	vtSeedVis(t, store, "sess_leaf", "sess_mid", session.VisibilityFull)

	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_mid"}`)
	var root vtNode
	if err := json.Unmarshal([]byte(resultText(t, resp)), &root); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	if root.TaskID != "sess_root" || vtCount(root) != 3 {
		t.Errorf("full from mid: root=%q count=%d, want sess_root/3", root.TaskID, vtCount(root))
	}
}

// TestMCPGetTaskTreeParentAndSelf_spec_8_5_540 — a `parent-and-self`
// caller sees its parent and itself; sibling subtrees and its own
// descendants are pruned. F-8.5.2 / F-8.9.2.
func TestMCPGetTaskTreeParentAndSelf_spec_8_5_540(t *testing.T) {
	srv, store := newMCP(t)
	vtSeedVis(t, store, "sess_root", "", session.VisibilityFull)
	vtSeedVis(t, store, "sess_mid", "sess_root", session.VisibilityParentAndSelf)
	vtSeedVis(t, store, "sess_sib", "sess_root", session.VisibilityFull)
	vtSeedVis(t, store, "sess_leaf", "sess_mid", session.VisibilityFull)

	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_mid"}`)
	var root vtNode
	if err := json.Unmarshal([]byte(resultText(t, resp)), &root); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	if root.TaskID != "sess_root" || vtCount(root) != 2 {
		t.Fatalf("parent-and-self: root=%q count=%d, want sess_root/2", root.TaskID, vtCount(root))
	}
	if len(root.Children) != 1 || root.Children[0].TaskID != "sess_mid" || len(root.Children[0].Children) != 0 {
		t.Errorf("parent-and-self must show only [sess_mid] with no descendants: %+v", root.Children)
	}
}

// TestMCPGetTaskTreeSelfOnly_spec_8_5_540 — a `self-only` caller sees only
// its own node. F-8.5.2 / F-8.9.2.
func TestMCPGetTaskTreeSelfOnly_spec_8_5_540(t *testing.T) {
	srv, store := newMCP(t)
	vtSeedVis(t, store, "sess_root", "", session.VisibilityFull)
	vtSeedVis(t, store, "sess_mid", "sess_root", session.VisibilitySelfOnly)
	vtSeedVis(t, store, "sess_leaf", "sess_mid", session.VisibilityFull)

	resp := call(t, srv.Handler(), "lenny/get_task_tree", `{"sessionId":"sess_mid"}`)
	var root vtNode
	if err := json.Unmarshal([]byte(resultText(t, resp)), &root); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	if root.TaskID != "sess_mid" || vtCount(root) != 1 {
		t.Errorf("self-only: root=%q count=%d, want sess_mid/1", root.TaskID, vtCount(root))
	}
}

// newMCPForDelegateVis wires an MCP server whose delegation Service runs
// over the same store. No Runtimes/Environments are wired, so the §10.6
// scope filter is permissive and the treeVisibility lease rules are the
// only gate under test.
func newMCPForDelegateVis(t *testing.T) (*mcp.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:    store,
		Executor: executor.NewEchoExecutor(),
		Delegation: delegation.NewService(store, delegation.Options{
			Clock:  func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			IDFunc: func() string { return "sess_child" },
		}),
		Clock:    func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:   func() string { return "sess_mcp" },
		TenantID: "acme",
	})
	return srv, store
}

func vtSeedParent(t *testing.T, store sessionstore.Store, vis session.TreeVisibility) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_parent", TenantID: "acme", State: session.StateRunning,
		UserID: "user_alice", RuntimeRef: "claude", PoolRef: "pool-a",
		IsolationProfile: isolation.ProfileSandboxed, TreeVisibility: vis,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
}

// TestMCPDelegateTreeVisibilityWeakening_spec_8_3_317 — delegate_task maps
// a widening child treeVisibility to the canonical TREE_VISIBILITY_WEAKENING
// envelope (POLICY, 422-equivalent, retryable=false) with both sides in
// details. F-8.5.2 / F-13.5.8.
func TestMCPDelegateTreeVisibilityWeakening_spec_8_3_317(t *testing.T) {
	srv, store := newMCPForDelegateVis(t)
	vtSeedParent(t, store, session.VisibilityParentAndSelf)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b","treeVisibility":"full"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "TREE_VISIBILITY_WEAKENING" {
		t.Fatalf("code = %v, want TREE_VISIBILITY_WEAKENING", env["code"])
	}
	if env["category"] != "POLICY" || env["retryable"] != false {
		t.Errorf("envelope (category, retryable) = (%v, %v), want (POLICY, false)", env["category"], env["retryable"])
	}
	details, _ := env["details"].(map[string]any)
	if details["parentTreeVisibility"] != "parent-and-self" || details["childTreeVisibility"] != "full" {
		t.Errorf("details = %+v, want parent=parent-and-self child=full", details)
	}
	// The child row must not have been created.
	if _, err := store.Get(context.Background(), "acme", "sess_child"); err == nil {
		t.Errorf("weakening must reject before creating the child")
	}
}

// TestMCPDelegateTreeVisibilityNarrowingAccepted_spec_8_3_316 — a narrowing
// child lease is admitted and the resolved value is stamped on the child.
func TestMCPDelegateTreeVisibilityNarrowingAccepted_spec_8_3_316(t *testing.T) {
	srv, store := newMCPForDelegateVis(t)
	vtSeedParent(t, store, session.VisibilityFull)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b","treeVisibility":"self-only"}`)
	_ = resultText(t, resp) // asserts no error
	child, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("child not created: %v", err)
	}
	if child.TreeVisibility != session.VisibilitySelfOnly {
		t.Errorf("child treeVisibility = %q, want self-only", child.TreeVisibility)
	}
}

// TestMCPDelegateTreeVisibilityInvalid_spec_8_5_540 — a malformed
// treeVisibility is rejected at the MCP boundary with INVALID_LEASE_FIELD.
func TestMCPDelegateTreeVisibilityInvalid_spec_8_5_540(t *testing.T) {
	srv, store := newMCPForDelegateVis(t)
	vtSeedParent(t, store, session.VisibilityFull)

	resp := call(t, srv.Handler(), "lenny/delegate_task",
		`{"parentSessionId":"sess_parent","target":"gemini","poolRef":"pool-b","treeVisibility":"everything"}`)
	result, _ := resp["result"].(map[string]any)
	env := readLennyErrorEnvelope(t, result)
	if env["code"] != "INVALID_LEASE_FIELD" {
		t.Fatalf("code = %v, want INVALID_LEASE_FIELD", env["code"])
	}
	details, _ := env["details"].(map[string]any)
	if details["field"] != "treeVisibility" {
		t.Errorf("details.field = %v, want treeVisibility", details["field"])
	}
}
