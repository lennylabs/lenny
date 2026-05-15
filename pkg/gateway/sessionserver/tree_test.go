// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if resp.NodeCount != 1 || resp.Root.SessionID != "sess_root" {
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
		if resp.Root.Children[i].SessionID == "sess_child_a" {
			childA = &resp.Root.Children[i]
		}
	}
	if childA == nil || len(childA.Children) != 1 {
		t.Fatalf("child_a subtree: %+v", childA)
	}
	if childA.Children[0].SessionID != "sess_grandchild" {
		t.Errorf("grandchild: %+v", childA.Children[0])
	}
}

func TestTreeSubtreeFromMidNode(t *testing.T) {
	store := memstore.New()
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSession(t, store, "sess_mid", "sess_root")
	seedTreeSession(t, store, "sess_leaf", "sess_mid")
	srv := sessionserver.New(store, sessionserver.Options{})

	// Requesting the tree from sess_mid returns only mid + leaf.
	_, resp := getTree(t, srv.Handler(), "sess_mid")
	if resp.NodeCount != 2 || resp.Root.SessionID != "sess_mid" {
		t.Errorf("subtree from mid: %+v", resp)
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
