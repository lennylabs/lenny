// SPDX-License-Identifier: MIT

package sessionserver

// §4.2 task-record design clarification:
//
// The §4.2 line 157 Session Manager bullet ("Task records and
// parent/child lineage (task tree)") is realised in v1 by the
// sessions table — there is no separate task table. Every task
// record IS a session row, identified by sessions.id and linked to
// its parent via sessions.parent_session_id. The §8.8 TaskRecord
// state machine in pkg/task/state describes the externally-visible
// MCP task lifecycle, but the persistent record is the session row
// the task corresponds to.
//
// The tree handler below walks this lineage: every session whose
// parent chain leads back to the requested session is a descendant.
// Cross-session metadata (cwd, pod assignment, recovery generation,
// resume window, retry count, policy enforcement state) lives on the
// same row and travels with the task. A future iteration may split
// long-lived task metadata onto a dedicated table, but the v1
// invariant is "task record == session row linked by
// parent_session_id".
// spec: §4.2 line 157.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// TreeCycleObserver is invoked when a §8.9 tree walker hits a cycle
// in the §8.2 ParentSessionID lineage. The §8.2 cycle detector
// prevents cycles at delegation time, so a non-zero call rate
// signals a corrupted persistent store (e.g., a §8.10 recovery
// write that re-parented a node into a cycle). The walker truncates
// the cycle in the returned tree; the observer surfaces the
// corruption to operators via the §11.7 audit log + §16.1
// `lenny_delegation_tree_cycle_detected_total` counter so it does
// not silently disappear. The source field is `rest` for the
// /v1/sessions/{id}/tree handler. spec: §8.9 line 1003; F-8.9.10.
type TreeCycleObserver interface {
	OnTreeCycle(ctx context.Context, ev TreeCycleEvent)
}

// TreeCycleEvent is the §8.9 tree-cycle audit/metric payload. The
// fields mirror the §11.7 audit row the gateway writes.
type TreeCycleEvent struct {
	TenantID      string
	RootSessionID string
	CycleNodeID   string
	Source        string
}

// TreeNode is one node in the §8 delegation task tree. The wire field
// is `taskId` per §8.5 line 540 — "Each node includes `taskId`, `state`,
// and `runtimeRef`". The v1 invariant "task record == session row"
// (§4.2 line 157) means TaskID is the session row's id; the REST and
// MCP projections of `lenny/get_task_tree` use the same field name per
// the §15.2.1 REST↔MCP semantic-equivalence rule. F-8.9.5.
//
// Attributes carries the §8.9 line 1010 per-node tracking projection
// (generation, pod, lease, failure history) so an operator can inspect
// a child's recovery generation, pod assignment, granted lease, and
// failure history from the tree. F-8.9.1.
type TreeNode struct {
	TaskID     string                      `json:"taskId"`
	State      string                      `json:"state"`
	RuntimeRef string                      `json:"runtimeRef,omitempty"`
	Attributes sessionstore.NodeAttributes `json:"attributes"`
	Children   []TreeNode                  `json:"children"`
}

// TreeResponse is the §15.1 GET /v1/sessions/{id}/tree envelope.
type TreeResponse struct {
	// Root is the delegation tree rooted at the requested session.
	Root TreeNode `json:"root"`

	// NodeCount is the total node count in the returned subtree.
	NodeCount int `json:"nodeCount"`
}

// handleTree implements GET /v1/sessions/{id}/tree per §15.1.
//
// The tree is reconstructed from the §7.1 ParentSessionID lineage
// pointers on the session rows, then scoped to the {id} session's
// effective §8.3 `treeVisibility` per §8.5 line 540: `full` returns the
// entire tree rooted at the apex (including siblings and their
// descendants), `parent-and-self` returns the session's parent and
// itself, and `self-only` returns just the session. This keeps the REST
// projection semantically equivalent to the MCP `lenny/get_task_tree`
// tool per §15.2.1. F-8.5.2 / F-8.9.2.
//
// Returns 404 RESOURCE_NOT_FOUND when the session does not exist or
// belongs to another tenant.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	caller, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// spec: §8.9 line 1010 — the §12.5 `idx_sessions_root` index
	// supports a single-shard tree-scoped projection. Read only the
	// rows belonging to the requested session's delegation tree
	// instead of the whole tenant; the cost is O(tree size), not
	// O(tenant size). F-8.9.7.
	rootSessionID := caller.RootSessionID
	if rootSessionID == "" {
		rootSessionID = caller.ID
	}
	all, err := s.store.ListByRoot(r.Context(), tenantID, rootSessionID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	childrenByParent := map[string][]sessionstore.Session{}
	for _, sess := range all {
		if sess.ParentSessionID != "" {
			childrenByParent[sess.ParentSessionID] = append(childrenByParent[sess.ParentSessionID], sess)
		}
	}

	// spec: §8.5 line 540 / §8.3 lines 311-319 — scope the response to the
	// session's effective treeVisibility. F-8.5.2 / F-8.9.2.
	respRoot, allowed := sessionstore.VisibleTree(caller, all, caller.TreeVisibility)
	count := 0
	ctx := treeWalkContext{
		ctx:           r.Context(),
		tenantID:      tenantID,
		rootSessionID: caller.ID,
		observer:      s.treeCycleObserver,
		allowed:       allowed,
	}
	node := buildTreeNode(ctx, respRoot, childrenByParent, &count, map[string]bool{})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TreeResponse{Root: node, NodeCount: count})
}

// treeWalkContext carries the per-walk fields buildTreeNode passes
// to the cycle observer. The observer fires once per repeated node;
// the walker still truncates the cycle to keep the response well-
// formed. spec: §8.9 line 1003; F-8.9.10.
type treeWalkContext struct {
	ctx           context.Context
	tenantID      string
	rootSessionID string
	observer      TreeCycleObserver
	// allowed, when non-nil, restricts the §8.5 treeVisibility-scoped
	// walk to exactly its member session IDs; a nil set means "no
	// restriction" (the `full` visibility case). F-8.5.2 / F-8.9.2.
	allowed map[string]bool
}

// buildTreeNode recursively assembles the subtree rooted at sess.
// The `seen` set guards against a cycle in the ParentSessionID
// graph (which the §8.2 cycle detector prevents at delegation time,
// but the tree walker stays defensive in case of a corrupt store).
// When the guard fires, the walker emits a §8.9 cycle observation
// so the corruption surfaces rather than silently truncates.
// spec: §8.9 line 1003; F-8.9.10.
func buildTreeNode(wctx treeWalkContext, sess sessionstore.Session, childrenByParent map[string][]sessionstore.Session, count *int, seen map[string]bool) TreeNode {
	*count++
	node := TreeNode{
		TaskID:     sess.ID,
		State:      string(sess.State),
		RuntimeRef: sess.RuntimeRef,
		Attributes: sessionstore.ProjectNodeAttributes(sess),
		Children:   []TreeNode{},
	}
	if seen[sess.ID] {
		if wctx.observer != nil {
			wctx.observer.OnTreeCycle(wctx.ctx, TreeCycleEvent{
				TenantID:      wctx.tenantID,
				RootSessionID: wctx.rootSessionID,
				CycleNodeID:   sess.ID,
				Source:        "rest",
			})
		}
		return node
	}
	seen[sess.ID] = true
	for _, child := range childrenByParent[sess.ID] {
		// spec: §8.5 line 540 — under a narrowed treeVisibility the walker
		// descends only into the visible nodes; an out-of-scope child is
		// pruned. F-8.5.2 / F-8.9.2.
		if wctx.allowed != nil && !wctx.allowed[child.ID] {
			continue
		}
		node.Children = append(node.Children, buildTreeNode(wctx, child, childrenByParent, count, seen))
	}
	return node
}
