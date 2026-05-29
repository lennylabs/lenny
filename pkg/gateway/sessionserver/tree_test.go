// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §8 delegation task tree; §15.1 GET /v1/sessions/{id}/tree.

func seedTreeSession(t *testing.T, store sessionstore.Store, id, parent string) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		ParentSessionID: parent, RuntimeRef: "echo",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func getTree(t *testing.T, h http.Handler, id string) (*httptest.ResponseRecorder, sessionserver.TreeResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+id+"/tree", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var resp sessionserver.TreeResponse
	if rr.Code == http.StatusOK {
		_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	}
	return rr, resp
}

func TestTreeSingleNode(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	srv := sessionserver.New(store, sessionserver.Options{})

	rr, resp := getTree(t, srv.Handler(), "sess_root")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if resp.NodeCount != 1 || resp.Root.TaskID != "sess_root" {
		t.Errorf("single-node tree: %+v", resp)
	}
	if len(resp.Root.Children) != 0 {
		t.Errorf("root should have no children: %+v", resp.Root.Children)
	}
}

func TestTreeWithChildren(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSession(t, store, "sess_child_a", "sess_root")
	seedTreeSession(t, store, "sess_child_b", "sess_root")
	seedTreeSession(t, store, "sess_grandchild", "sess_child_a")
	srv := sessionserver.New(store, sessionserver.Options{})

	rr, resp := getTree(t, srv.Handler(), "sess_root")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if resp.NodeCount != 4 {
		t.Errorf("node count: got %d, want 4", resp.NodeCount)
	}
	if len(resp.Root.Children) != 2 {
		t.Fatalf("root children: got %d, want 2", len(resp.Root.Children))
	}
	// Find child_a and confirm it has the grandchild.
	var childA *sessionserver.TreeNode
	for i := range resp.Root.Children {
		if resp.Root.Children[i].TaskID == "sess_child_a" {
			childA = &resp.Root.Children[i]
		}
	}
	if childA == nil || len(childA.Children) != 1 {
		t.Fatalf("child_a subtree: %+v", childA)
	}
	if childA.Children[0].TaskID != "sess_grandchild" {
		t.Errorf("grandchild: %+v", childA.Children[0])
	}
}

// seedTreeSessionVis seeds a session with an explicit §8.5
// treeVisibility so the visibility-scoping tests can exercise the three
// enum values. spec: §8.5 line 540.
func seedTreeSessionVis(t *testing.T, store sessionstore.Store, id, parent string, vis session.TreeVisibility) {
	t.Helper()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	row := sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		ParentSessionID: parent, RuntimeRef: "echo",
		TreeVisibility: vis,
		CreatedAt:      now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestTreeFullVisibilityShowsEntireTreeFromApex_spec_8_5_540 verifies the
// §8.5 `full` (default) semantics: a mid-tree session sees the entire
// tree rooted at the apex, including its parent and siblings, not just
// its own subtree. F-8.5.2 / F-8.9.2.
func TestTreeFullVisibilityShowsEntireTreeFromApex_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSession(t, store, "sess_mid", "sess_root")
	seedTreeSession(t, store, "sess_leaf", "sess_mid")
	srv := sessionserver.New(store, sessionserver.Options{})

	// sess_mid carries the default (empty → full) visibility, so the
	// tree it sees is rooted at the apex (sess_root) with all 3 nodes.
	_, resp := getTree(t, srv.Handler(), "sess_mid")
	if resp.NodeCount != 3 || resp.Root.TaskID != "sess_root" {
		t.Errorf("full from mid: got root=%q count=%d, want sess_root/3 (§8.5 line 540)", resp.Root.TaskID, resp.NodeCount)
	}
}

// TestTreeParentAndSelfVisibility_spec_8_5_540 verifies the §8.5
// `parent-and-self` scoping: the session sees only its own node and its
// direct parent's node — sibling subtrees are pruned. F-8.5.2 / F-8.9.2.
func TestTreeParentAndSelfVisibility_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSessionVis(t, store, "sess_mid", "sess_root", session.VisibilityParentAndSelf)
	seedTreeSession(t, store, "sess_sibling", "sess_root")
	seedTreeSession(t, store, "sess_leaf", "sess_mid")
	srv := sessionserver.New(store, sessionserver.Options{})

	_, resp := getTree(t, srv.Handler(), "sess_mid")
	// Rooted at the parent (sess_root) with exactly sess_mid as its only
	// visible child; the sibling and the leaf are pruned. 2 nodes.
	if resp.NodeCount != 2 || resp.Root.TaskID != "sess_root" {
		t.Fatalf("parent-and-self: got root=%q count=%d, want sess_root/2", resp.Root.TaskID, resp.NodeCount)
	}
	if len(resp.Root.Children) != 1 || resp.Root.Children[0].TaskID != "sess_mid" {
		t.Errorf("parent-and-self children: %+v, want [sess_mid]", resp.Root.Children)
	}
	if len(resp.Root.Children[0].Children) != 0 {
		t.Errorf("parent-and-self must prune the leaf under self: %+v", resp.Root.Children[0].Children)
	}
}

// TestTreeSelfOnlyVisibility_spec_8_5_540 verifies the §8.5 `self-only`
// scoping: the session sees only its own node. F-8.5.2 / F-8.9.2.
func TestTreeSelfOnlyVisibility_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSessionVis(t, store, "sess_mid", "sess_root", session.VisibilitySelfOnly)
	seedTreeSession(t, store, "sess_leaf", "sess_mid")
	srv := sessionserver.New(store, sessionserver.Options{})

	_, resp := getTree(t, srv.Handler(), "sess_mid")
	if resp.NodeCount != 1 || resp.Root.TaskID != "sess_mid" || len(resp.Root.Children) != 0 {
		t.Errorf("self-only: got %+v, want sess_mid alone", resp)
	}
}

// TestTreeParentAndSelfAtRootDegradesToSelf_spec_8_3_315 verifies that a
// root session (no parent) with parent-and-self visibility sees only
// itself rather than fabricating a parent node. spec: §8.3 line 315.
func TestTreeParentAndSelfAtRootDegradesToSelf_spec_8_3_315(t *testing.T) {
	store := memstore.New()
	seedTreeSessionVis(t, store, "sess_root", "", session.VisibilityParentAndSelf)
	seedTreeSession(t, store, "sess_child", "sess_root")
	srv := sessionserver.New(store, sessionserver.Options{})

	_, resp := getTree(t, srv.Handler(), "sess_root")
	if resp.NodeCount != 1 || resp.Root.TaskID != "sess_root" || len(resp.Root.Children) != 0 {
		t.Errorf("parent-and-self at root: got %+v, want sess_root alone", resp)
	}
}

func TestTreeMissingSession(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	rr, _ := getTree(t, srv.Handler(), "sess_missing")
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing session: got %d, want 404", rr.Code)
	}
}

// spec: §4.2 line 157 design clarification — "task record == session
// row linked by parent_session_id". The v1 invariant is that every
// task record IS a session row; the tree walker observes the parent
// chain through sessions.parent_session_id rather than a separate
// tasks table. This test asserts the invariant by seeding a chain
// and confirming every node in the §15.1 tree response is one of
// the seeded session IDs.
func TestTreeRecordsAreSessionRowsLinkedByParentSessionID(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSession(t, store, "sess_child", "sess_root")
	seedTreeSession(t, store, "sess_grandchild", "sess_child")
	srv := sessionserver.New(store, sessionserver.Options{})

	rr, resp := getTree(t, srv.Handler(), "sess_root")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if resp.NodeCount != 3 {
		t.Fatalf("node count = %d, want 3 (root + child + grandchild)", resp.NodeCount)
	}
	// Walk the response collecting every observed session id. The
	// invariant: every observed id is one of the seeded session IDs
	// — there is no separate task identifier surfaced by the tree
	// handler.
	want := map[string]bool{"sess_root": true, "sess_child": true, "sess_grandchild": true}
	var walk func(n sessionserver.TreeNode)
	walk = func(n sessionserver.TreeNode) {
		if !want[n.TaskID] {
			t.Errorf("tree node %q is not a seeded session id (§4.2 line 157: task record == session row)", n.TaskID)
		}
		delete(want, n.TaskID)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(resp.Root)
	if len(want) != 0 {
		t.Errorf("missing session rows from tree: %v", want)
	}

	// The invariant also says the link is sessions.parent_session_id.
	// Verify by reading the seeded grandchild row and confirming the
	// ParentSessionID points at the seeded child row.
	row, err := store.Get(context.Background(), "acme", "sess_grandchild")
	if err != nil {
		t.Fatalf("read grandchild: %v", err)
	}
	if row.ParentSessionID != "sess_child" {
		t.Errorf("grandchild.ParentSessionID = %q, want sess_child (§4.2 line 157)", row.ParentSessionID)
	}
}

func TestTreeCrossTenantIsolation(t *testing.T) {
	store := memstore.New()
	now := time.Now()
	// A foreign-tenant session must not appear in acme's tree even
	// if its ParentSessionID points at an acme session.
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_root", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_foreign", TenantID: "globex", State: session.StateRunning,
		ParentSessionID: "sess_root",
		CreatedAt:       now, UpdatedAt: now,
	})
	srv := sessionserver.New(store, sessionserver.Options{})
	_, resp := getTree(t, srv.Handler(), "sess_root")
	if resp.NodeCount != 1 {
		t.Errorf("cross-tenant child must not appear: %+v", resp)
	}
}

// TestTreeReadsOnlySessionsInTheRequestedTree_spec_8_9_1010 pins the
// §8.9 line 1010 / §12.5 line 101 single-shard projection: the REST
// `/tree` handler reads only rows belonging to the requested
// session's delegation tree (via the `idx_sessions_root` index), so a
// sibling tree in the same tenant never appears in the response.
// F-8.9.7 / F-8.9.8.
func TestTreeReadsOnlySessionsInTheRequestedTree_spec_8_9_1010(t *testing.T) {
	store := memstore.New()
	// Tree A: sess_root_a + child.
	seedTreeSession(t, store, "sess_root_a", "")
	seedTreeSession(t, store, "sess_kid_a", "sess_root_a")
	// Tree B (sibling — different root): sess_root_b + child.
	seedTreeSession(t, store, "sess_root_b", "")
	seedTreeSession(t, store, "sess_kid_b", "sess_root_b")
	srv := sessionserver.New(store, sessionserver.Options{})

	rr, resp := getTree(t, srv.Handler(), "sess_root_a")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	// The response must include only tree A's nodes.
	if resp.NodeCount != 2 {
		t.Errorf("tree A node count = %d, want 2 (tree B must not leak)", resp.NodeCount)
	}
	body := rr.Body.String()
	if strings.Contains(body, "sess_root_b") || strings.Contains(body, "sess_kid_b") {
		t.Errorf("tree A response leaked tree B nodes: %q", body)
	}
}

// TestTreeUsesTaskIDField_spec_8_5_540 pins the §8.5 line 540 wire
// contract: the REST `/tree` projection emits `taskId` (matching the
// MCP `lenny/get_task_tree` shape per §15.2.1 REST↔MCP semantic
// equivalence). F-8.9.5.
func TestTreeUsesTaskIDField_spec_8_5_540(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root_rt", "")
	seedTreeSession(t, store, "sess_kid_rt", "sess_root_rt")
	srv := sessionserver.New(store, sessionserver.Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_root_rt/tree", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`"taskId":"sess_root_rt"`, `"taskId":"sess_kid_rt"`} {
		if !strings.Contains(body, want) {
			t.Errorf("tree response missing %s: %q", want, body)
		}
	}
	if strings.Contains(body, `"sessionId":"sess_root_rt"`) || strings.Contains(body, `"sessionId":"sess_kid_rt"`) {
		t.Errorf("tree response leaked legacy `sessionId` field instead of §8.5 line 540 `taskId`: %q", body)
	}
}

// captureTreeCycle is a §8.9 cycle observer fake that records every
// observation so a test can assert emission shape. F-8.9.10.
type captureTreeCycle struct {
	events []sessionserver.TreeCycleEvent
}

func (c *captureTreeCycle) OnTreeCycle(_ context.Context, ev sessionserver.TreeCycleEvent) {
	c.events = append(c.events, ev)
}

// TestTreeEmitsCycleObservation_spec_8_9_F_8_9_10 verifies that the
// REST /v1/sessions/{id}/tree walker fires the TreeCycleObserver
// when the persistent store has been corrupted into a
// ParentSessionID cycle. The walker still returns a well-formed
// (truncated) tree so the response stays serializable. F-8.9.10.
func TestTreeEmitsCycleObservation_spec_8_9_F_8_9_10(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A two-node cycle: sess_a.parent = sess_b, sess_b.parent = sess_a.
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_a_899", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_b_899",
		CreatedAt:       now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_b_899", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_a_899",
		CreatedAt:       now, UpdatedAt: now,
	})
	observer := &captureTreeCycle{}
	srv := sessionserver.New(store, sessionserver.Options{
		TreeCycleObserver: observer,
	})

	rr, resp := getTree(t, srv.Handler(), "sess_a_899")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if resp.Root.TaskID != "sess_a_899" {
		t.Errorf("root = %q, want sess_a_899", resp.Root.TaskID)
	}
	if len(observer.events) == 0 {
		t.Fatalf("expected at least one TreeCycleEvent; got none")
	}
	got := observer.events[0]
	if got.TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme", got.TenantID)
	}
	if got.Source != "rest" {
		t.Errorf("Source = %q, want rest", got.Source)
	}
	if got.RootSessionID != "sess_a_899" {
		t.Errorf("RootSessionID = %q, want sess_a_899", got.RootSessionID)
	}
	if got.CycleNodeID == "" {
		t.Errorf("CycleNodeID is empty; want the repeated node id")
	}
}

// TestTreeAcyclicEmitsNoCycle_spec_8_9_F_8_9_10 verifies the negative
// path: a clean tree never fires the cycle observer. F-8.9.10.
func TestTreeAcyclicEmitsNoCycle_spec_8_9_F_8_9_10(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root_clean", "")
	seedTreeSession(t, store, "sess_kid_clean", "sess_root_clean")

	observer := &captureTreeCycle{}
	srv := sessionserver.New(store, sessionserver.Options{
		TreeCycleObserver: observer,
	})
	rr, _ := getTree(t, srv.Handler(), "sess_root_clean")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	if len(observer.events) != 0 {
		t.Errorf("acyclic tree produced %d cycle events; want 0", len(observer.events))
	}
}

// TestTreeNilObserverIsSafe_spec_8_9_F_8_9_10 verifies that the walker
// keeps the legacy contract when no observer is wired: a cycle is
// truncated and the response is well-formed. F-8.9.10.
func TestTreeNilObserverIsSafe_spec_8_9_F_8_9_10(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_x_899", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_y_899",
		CreatedAt:       now, UpdatedAt: now,
	})
	_ = store.Create(context.Background(), sessionstore.Session{
		ID: "sess_y_899", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "sess_x_899",
		CreatedAt:       now, UpdatedAt: now,
	})
	srv := sessionserver.New(store, sessionserver.Options{})
	rr, _ := getTree(t, srv.Handler(), "sess_x_899")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
}
