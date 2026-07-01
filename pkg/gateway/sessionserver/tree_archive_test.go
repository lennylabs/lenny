// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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

// orphanCapAuditSink captures session-lifecycle audit rows so the
// §8.10 line 1103 fallback case can assert on the emitted event.
type orphanCapAuditSink struct {
	events []sessionserver.SessionLifecycleEvent
}

func (s *orphanCapAuditSink) EmitSessionLifecycle(_ context.Context, ev sessionserver.SessionLifecycleEvent) {
	s.events = append(s.events, ev)
}

// TestDetachFallbackEmitsCascadeAppliedAudit_spec_8_10_1103 covers the
// orphan-cap fallback audit row — the §11.7 / §16.7
// `session.cascade_applied` event must fire with the downgrade reason
// and the original/effective cascade policies in Detail so the
// orchestrator that configured `detach` deliberately sees why the
// gateway cancelled its subtree. F-8.10.8.
func TestDetachFallbackEmitsCascadeAppliedAudit_spec_8_10_1103(t *testing.T) {
	store := memstore.New()
	sink := &orphanCapAuditSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		MaxOrphanTasksPerTenant: 1,
		LifecycleAuditSink:      sink,
	})
	now := time.Now()
	// Pre-existing orphan to push the tenant over the cap.
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
	var found *sessionserver.SessionLifecycleEvent
	for i := range sink.events {
		if sink.events[i].EventType == "session.cascade_applied" {
			found = &sink.events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("session.cascade_applied audit not emitted; events = %+v", sink.events)
	}
	if found.SessionID != "sess_parent" {
		t.Errorf("audit SessionID = %q, want sess_parent", found.SessionID)
	}
	if found.TenantID != "acme" {
		t.Errorf("audit TenantID = %q, want acme", found.TenantID)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(found.Detail), &detail); err != nil {
		t.Fatalf("audit Detail not JSON: %v (%q)", err, found.Detail)
	}
	if detail["reason"] != "orphan_cap_fallback" {
		t.Errorf("audit reason = %v, want orphan_cap_fallback", detail["reason"])
	}
	if detail["originalPolicy"] != string(session.CascadeDetach) {
		t.Errorf("audit originalPolicy = %v, want detach", detail["originalPolicy"])
	}
	if detail["effectivePolicy"] != string(session.CascadeCancelAll) {
		t.Errorf("audit effectivePolicy = %v, want cancel_all", detail["effectivePolicy"])
	}
}

// TestDetachUnderCapDoesNotEmitFallbackAudit_spec_8_10_1103 covers the
// negative case — when the tenant is under the orphan cap, the §8.10
// detach proceeds without emitting a `session.cascade_applied` row.
// F-8.10.8.
func TestDetachUnderCapDoesNotEmitFallbackAudit_spec_8_10_1103(t *testing.T) {
	store := memstore.New()
	sink := &orphanCapAuditSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		MaxOrphanTasksPerTenant: 100,
		LifecycleAuditSink:      sink,
	})
	seedCascadeSession(t, store, "sess_parent", "", session.CascadeDetach)
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}
	for _, ev := range sink.events {
		if ev.EventType == "session.cascade_applied" {
			t.Errorf("session.cascade_applied audit must not fire under the orphan cap: %+v", ev)
		}
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

// reattachedChildren parses the children array out of the
// children_reattached event payload so tests can inspect individual
// child fields (pending_request_id, etc.).
func reattachedChildren(t *testing.T, ev *sessionevents.Event) []map[string]any {
	t.Helper()
	var payload struct {
		Children []map[string]any `json:"children"`
	}
	if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
		t.Fatalf("decode children_reattached: %v", err)
	}
	return payload.Children
}

// TestResumeChildrenReattachedCarriesPendingRequestIDFromInputWait —
// when a child registered an outstanding `lenny/request_input`, the
// parent's children_reattached event surfaces the request id so the
// parent knows what to answer via lenny/send_message / inReplyTo.
// spec: §7.2 line 153; F-7.2.16.
func TestResumeChildrenReattachedCarriesPendingRequestIDFromInputWait(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	reg := inputwait.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		Events:     bus,
		InputWaits: reg,
	})
	seedAwaitingParent(t, store, "sess_parent")
	seedTreeSession(t, store, "sess_child", "sess_parent")
	if _, err := reg.Register("sess_child", "req_42", nil); err != nil {
		t.Fatalf("Register: %v", err)
	}

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body %s", rr.Code, rr.Body.String())
	}
	ev := childrenReattached(bus, "sess_parent")
	if ev == nil {
		t.Fatal("no children_reattached event")
	}
	kids := reattachedChildren(t, ev)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if got := kids[0]["pending_request_id"]; got != "req_42" {
		t.Errorf("pending_request_id = %v, want req_42", got)
	}
}

// TestResumeChildrenReattachedCarriesPendingRequestIDFromInteractionStore —
// when the child has a pending tool-use or elicitation interaction
// (and no request_input registration), the parent surfaces the
// interaction id as the pending_request_id.
// spec: §7.2 line 153; F-7.2.16.
func TestResumeChildrenReattachedCarriesPendingRequestIDFromInteractionStore(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	ints := interactionstore.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{
		Events:       bus,
		Interactions: ints,
	})
	seedAwaitingParent(t, store, "sess_parent")
	seedTreeSession(t, store, "sess_child", "sess_parent")
	now := time.Now().UTC()
	if err := ints.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_99", Kind: interactionstore.KindElicitation,
		SessionID: "sess_child", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending, CreatedAt: now,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d, body %s", rr.Code, rr.Body.String())
	}
	ev := childrenReattached(bus, "sess_parent")
	if ev == nil {
		t.Fatal("no children_reattached event")
	}
	kids := reattachedChildren(t, ev)
	if got := kids[0]["pending_request_id"]; got != "elic_99" {
		t.Errorf("pending_request_id = %v, want elic_99", got)
	}
}

// TestResumeChildrenReattachedPendingRequestIDOldestFirst — when a
// child has several pending interactions, the oldest one wins so the
// resumed parent unblocks the longest-waiting request first.
// spec: §7.2 line 153; F-7.2.16.
func TestResumeChildrenReattachedPendingRequestIDOldestFirst(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	ints := interactionstore.NewMemory()
	srv := sessionserver.New(store, sessionserver.Options{
		Events:       bus,
		Interactions: ints,
	})
	seedAwaitingParent(t, store, "sess_parent")
	seedTreeSession(t, store, "sess_child", "sess_parent")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Insert the newer first so the natural map order does not coincide
	// with the expected oldest-first order.
	if err := ints.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_new", Kind: interactionstore.KindElicitation,
		SessionID: "sess_child", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending, CreatedAt: base.Add(time.Minute),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := ints.Put(context.Background(), interactionstore.Interaction{
		ID: "elic_old", Kind: interactionstore.KindElicitation,
		SessionID: "sess_child", TenantID: "acme", UserID: "alice",
		Phase: interactionstore.PhasePending, CreatedAt: base,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d", rr.Code)
	}
	ev := childrenReattached(bus, "sess_parent")
	kids := reattachedChildren(t, ev)
	if got := kids[0]["pending_request_id"]; got != "elic_old" {
		t.Errorf("pending_request_id = %v, want elic_old (oldest first)", got)
	}
}

// TestResumeChildrenReattachedPendingRequestIDAbsentWhenIdle — a
// non-terminal child with no outstanding request omits the field
// (the JSON encoder drops the empty string).
// spec: §7.2 line 153 ("`null` if none"); F-7.2.16.
func TestResumeChildrenReattachedPendingRequestIDAbsentWhenIdle(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	srv := sessionserver.New(store, sessionserver.Options{Events: bus})
	seedAwaitingParent(t, store, "sess_parent")
	seedTreeSession(t, store, "sess_child", "sess_parent")

	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/sess_parent/resume")
	if rr.Code != http.StatusOK {
		t.Fatalf("resume: status %d", rr.Code)
	}
	ev := childrenReattached(bus, "sess_parent")
	if ev == nil {
		t.Fatal("no children_reattached event")
	}
	if strings.Contains(ev.Data, "pending_request_id") {
		t.Errorf("idle child should omit pending_request_id: %s", ev.Data)
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
