// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// seedAwaitingParent inserts a parent session in awaiting_client_action,
// the §15.1 precondition state for POST /resume.
func seedAwaitingParent(t *testing.T, store sessionstore.Store, id string) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateAwaitingClientAction,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed awaiting parent %s: %v", id, err)
	}
}

// childrenReattached returns the children_reattached event on a
// session's stream, or nil when none was emitted.
func childrenReattached(bus *sessionevents.Bus, sessionID string) *sessionevents.Event {
	for _, e := range bus.History(sessionID, 0) {
		if e.Type == "children_reattached" {
			ev := e
			return &ev
		}
	}
	return nil
}

// spec: §8.10 — a child session reaching a terminal state is archived
// to the session_tree_archive so a resumed parent can replay it. The
// seedTreeSession helper (tree_test.go) creates a running session.

// seedCascadeSession inserts a running session carrying an explicit
// §8.10 cascadeOnFailure policy.
func seedCascadeSession(t *testing.T, store sessionstore.Store, id, parent string, policy session.CascadePolicy) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning,
		ParentSessionID: parent, CascadeOnFailure: policy,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed cascade session %s: %v", id, err)
	}
}

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

// spec: §8.10 — the cascadeOnFailure policy governs the fate of a
// session's children when it reaches a terminal state.

func TestTerminateCascadeCancelsChildren(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	// The parent carries the default cascade policy (cancel_all).
	seedTreeSession(t, store, "sess_parent", "")
	seedTreeSession(t, store, "sess_c1", "sess_parent")
	seedTreeSession(t, store, "sess_c2", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	for _, id := range []string{"sess_c1", "sess_c2"} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateCancelled {
			t.Errorf("%s state = %q, want cancelled by the cascade", id, row.State)
		}
	}
}

func TestTerminateDetachLeavesChildrenRunning(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	seedCascadeSession(t, store, "sess_parent", "", session.CascadeDetach)
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("child state = %q, want running — detach leaves children alive", row.State)
	}
}

func TestCascadeRespectsPerNodePolicy(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})
	// root → mid_detach (detach) → leaf_a; root → mid_cancel (default) → leaf_b.
	seedTreeSession(t, store, "sess_root", "")
	seedCascadeSession(t, store, "sess_mid_detach", "sess_root", session.CascadeDetach)
	seedTreeSession(t, store, "sess_leaf_a", "sess_mid_detach")
	seedTreeSession(t, store, "sess_mid_cancel", "sess_root")
	seedTreeSession(t, store, "sess_leaf_b", "sess_mid_cancel")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_root/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	want := map[string]session.State{
		"sess_mid_detach": session.StateCancelled, // cancelled by the root's cascade
		"sess_leaf_a":     session.StateRunning,   // shielded — mid_detach detaches
		"sess_mid_cancel": session.StateCancelled,
		"sess_leaf_b":     session.StateCancelled, // mid_cancel re-cascades
	}
	for id, wantState := range want {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != wantState {
			t.Errorf("%s state = %q, want %q", id, row.State, wantState)
		}
	}
}

func TestDetachFallsBackToCancelAllOverOrphanCap(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{MaxOrphanTasksPerTenant: 1})
	now := time.Now()
	// A pre-existing orphan: a running child of an already-terminal root.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "old_root", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed old root: %v", err)
	}
	seedTreeSession(t, store, "old_child", "old_root")
	// A detach parent with a running child.
	seedCascadeSession(t, store, "sess_parent", "", session.CascadeDetach)
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	// The tenant is over the §8.10 orphan cap, so the detach cascade
	// falls back to cancel_all and the child is cancelled.
	row, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if row.State != session.StateCancelled {
		t.Errorf("child state = %q, want cancelled — a detach over the orphan cap falls back to cancel_all", row.State)
	}
}

func TestDetachProceedsUnderOrphanCap(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{MaxOrphanTasksPerTenant: 10})
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "old_root", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed old root: %v", err)
	}
	seedTreeSession(t, store, "old_child", "old_root")
	seedCascadeSession(t, store, "sess_parent", "", session.CascadeDetach)
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	// Well under the cap — the detach cascade leaves the child running.
	row, err := store.Get(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if row.State != session.StateRunning {
		t.Errorf("child state = %q, want running — a detach under the cap leaves children alive", row.State)
	}
}

func TestCascadeArchivesCancelledChildren(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{TreeArchive: archive})
	seedTreeSession(t, store, "sess_parent", "")
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := archive.GetByNode(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("cascade-cancelled child was not archived: %v", err)
	}
	if got.State != string(session.StateCancelled) {
		t.Errorf("archived state = %q, want cancelled", got.State)
	}
}

// spec: §7.1 / §8.10 — a resumed parent with active children receives
// a children_reattached event.

func TestResumeEmitsChildrenReattached(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_parent")
	seedTreeSession(t, store, "sess_child", "sess_parent") // running

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body %s", rr.Code, rr.Body.String())
	}
	ev := childrenReattached(bus, "sess_parent")
	if ev == nil {
		t.Fatal("no children_reattached event after resuming a parent with an active child")
	}
	if !strings.Contains(ev.Data, "sess_child") {
		t.Errorf("children_reattached data %q missing the child", ev.Data)
	}
}

func TestResumeSkipsChildrenReattachedWhenAllChildrenTerminal(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_parent")
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_child", TenantID: "acme", State: session.StateCompleted,
		ParentSessionID: "sess_parent", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body %s", rr.Code, rr.Body.String())
	}
	if ev := childrenReattached(bus, "sess_parent"); ev != nil {
		t.Error("children_reattached emitted when every child had already settled")
	}
}

func TestResumeSkipsChildrenReattachedWhenNoChildren(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_solo")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_solo/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body %s", rr.Code, rr.Body.String())
	}
	if ev := childrenReattached(bus, "sess_solo"); ev != nil {
		t.Error("children_reattached emitted for a session with no children")
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
