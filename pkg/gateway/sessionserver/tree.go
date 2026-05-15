// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// TreeNode is one node in the §8 delegation task tree.
type TreeNode struct {
	SessionID  string     `json:"sessionId"`
	State      string     `json:"state"`
	RuntimeRef string     `json:"runtimeRef,omitempty"`
	Children   []TreeNode `json:"children"`
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
// pointers on the session rows: every session whose parent chain
// leads back to the requested session is a descendant. The minimal
// gateway returns the full subtree (the §8.3 `treeVisibility`
// `parent-and-self` / `self-only` scoping is applied by the
// delegation-policy layer that ships with `lenny/delegate_task`).
//
// Returns 404 RESOURCE_NOT_FOUND when the session does not exist or
// belongs to another tenant.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	root, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Pull every session in the tenant and index by parent.
	all, err := s.store.List(r.Context(), tenantID, sessionstore.ListFilter{})
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

	count := 0
	node := buildTreeNode(root, childrenByParent, &count, map[string]bool{})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TreeResponse{Root: node, NodeCount: count})
}

// buildTreeNode recursively assembles the subtree rooted at sess.
// The `seen` set guards against a cycle in the ParentSessionID
// graph (which the §8.2 cycle detector prevents at delegation time,
// but the tree walker stays defensive in case of a corrupt store).
func buildTreeNode(sess sessionstore.Session, childrenByParent map[string][]sessionstore.Session, count *int, seen map[string]bool) TreeNode {
	*count++
	node := TreeNode{
		SessionID:  sess.ID,
		State:      string(sess.State),
		RuntimeRef: sess.RuntimeRef,
		Children:   []TreeNode{},
	}
	if seen[sess.ID] {
		return node
	}
	seen[sess.ID] = true
	for _, child := range childrenByParent[sess.ID] {
		node.Children = append(node.Children, buildTreeNode(child, childrenByParent, count, seen))
	}
	return node
}
