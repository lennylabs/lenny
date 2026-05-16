// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// spec: §8.10 — a child session reaching a terminal state is archived
// to the session_tree_archive so a resumed parent can replay it. The
// seedTreeSession helper (tree_test.go) creates a running session.

func TestTerminateArchivesChildToTreeArchive(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{TreeArchive: archive})
	seedTreeSession(t, store, "sess_parent", "")
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_child/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := archive.Get(context.Background(), "acme", "sess_parent", "sess_child")
	if err != nil {
		t.Fatalf("terminated child was not archived: %v", err)
	}
	if got.State != string(session.StateCompleted) {
		t.Errorf("archived state = %q, want completed", got.State)
	}
}

func TestCancelArchivesChildToTreeArchive(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{TreeArchive: archive})
	seedTreeSession(t, store, "sess_parent", "")
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodDelete, "/v1/sessions/sess_child")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", rr.Code, rr.Body.String())
	}

	got, err := archive.Get(context.Background(), "acme", "sess_parent", "sess_child")
	if err != nil {
		t.Fatalf("cancelled child was not archived: %v", err)
	}
	if got.State != string(session.StateCancelled) {
		t.Errorf("archived state = %q, want cancelled", got.State)
	}
}

func TestTerminateDoesNotArchiveRootSession(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{TreeArchive: archive})
	seedTreeSession(t, store, "sess_root", "")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_root/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}

	// A session with no parent is the delegation tree root — it is not
	// itself a child and is not archived.
	nodes, err := archive.Replay(context.Background(), "acme", "sess_root")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("root session archived %d nodes, want 0", len(nodes))
	}
}

func TestTreeArchiveResolvesTheTreeRoot(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{TreeArchive: archive})
	// A three-level tree: root → mid → leaf.
	seedTreeSession(t, store, "sess_root", "")
	seedTreeSession(t, store, "sess_mid", "sess_root")
	seedTreeSession(t, store, "sess_leaf", "sess_mid")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_leaf/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}

	// The leaf is archived under the tree root, not its direct parent.
	if _, err := archive.Get(context.Background(), "acme", "sess_root", "sess_leaf"); err != nil {
		t.Errorf("leaf was not archived under the tree root: %v", err)
	}
}
