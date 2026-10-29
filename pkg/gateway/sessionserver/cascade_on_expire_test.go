// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// staticTenantLister adapts a fixed tenant list to the watchdog's
// TenantLister contract for the integration test below. The real
// gateway wires the tenantstore-backed lister; the in-memory test uses
// the same shape with a single tenant.
type staticTenantLister []string

func (s staticTenantLister) ListTenants(_ context.Context) ([]string, error) {
	return []string(s), nil
}

// spec: §7.3 — "After expiry the session transitions to
// `expired` ... The gateway applies the session's cascadeOnFailure policy
// to all active children (same behavior as terminal failure after retry
// exhaustion)." F-7.3.11.
//
// The integration test wires the watchdog's TerminalHook to the Server
// (the same wiring cmd/lenny-gateway does at boot) and drives an
// awaiting_client_action row past the §11.3 maxAwaitingClientAction
// deadline. The terminal hook is the §7.3 cascade entry point:
// recordSessionCompleted → cascadeToChildren, which walks active
// descendants and cancels them per the parent's CascadeOnFailure.
func TestAwaitingExpiredAppliesCascadeViaTerminalHook_spec_7_3_11(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	born := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	// Parent in awaiting_client_action with default cascade=cancel_all.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "p_aw", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateAwaitingClientAction, CreatedAt: born, UpdatedAt: born,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	// Active child that should be cancelled by the cascade on expiry.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "c_running", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, ParentSessionID: "p_aw",
		CreatedAt: born, UpdatedAt: born,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	// The idle bound is raised past the tick so the child's own idle firing
	// (900s by default) does not end it: the parent's terminal hook runs on a
	// dispatched goroutine, so the idle sweep can reach the child before the
	// cascade does. The test isolates the cascade.
	w := watchdog.New(store, staticTenantLister{"acme"}, watchdog.Config{MaxIdleSeconds: 7200}, nil).
		WithTerminalHook(srv)

	// Drive past the default 900s awaiting deadline.
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The terminal hook runs on a goroutine that holds the parent's terminal
	// claim; the cascade has run once the claims close.
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}

	parent, err := store.Get(context.Background(), "acme", "p_aw")
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if parent.State != session.StateExpired {
		t.Fatalf("F-7.3.11: parent state = %q, want expired", parent.State)
	}
	child, err := store.Get(context.Background(), "acme", "c_running")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.State != session.StateCancelled {
		t.Errorf("F-7.3.11: cascade did not cancel running child; state = %q, want cancelled", child.State)
	}
}

// spec: §7.3 — a detach parent must NOT cancel its children
// on expiry; only the cancel_all cascade walks descendants. The
// terminal hook applies the parent's own CascadeOnFailure, so a detach
// parent leaves its children alive. F-7.3.11.
func TestAwaitingExpiredDetachLeavesChildrenRunning_spec_7_3_11(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})

	born := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "p_detach", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateAwaitingClientAction, CreatedAt: born, UpdatedAt: born,
		CascadeOnFailure: session.CascadeDetach,
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "c_detach", TenantID: "acme", RuntimeRef: "claude-code",
		State: session.StateRunning, ParentSessionID: "p_detach",
		CreatedAt: born, UpdatedAt: born,
	}); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	// The idle bound is raised past the tick so the child's own idle firing
	// (900s by default) does not end it; the test isolates the cascade.
	w := watchdog.New(store, staticTenantLister{"acme"}, watchdog.Config{MaxIdleSeconds: 7200}, nil).
		WithTerminalHook(srv)
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The terminal hook runs on a goroutine that holds the parent's terminal
	// claim; the cascade has run once the claims close.
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}

	child, err := store.Get(context.Background(), "acme", "c_detach")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.State != session.StateRunning {
		t.Errorf("F-7.3.11: detach cascade should leave child running; state = %q", child.State)
	}
}

// capNode seeds one row of a delegation tree for the orphan-cap fallback
// cases: depth 0 is a root, and a delegated node carries the depth the
// delegation service writes, so the §8.10 orphan predicate selects it once an
// ancestor is terminal.
func capNode(t *testing.T, store sessionstore.Store, id, parent string, depth uint32, policy session.CascadePolicy) {
	t.Helper()
	now := time.Now()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, RuntimeRef: "echo",
		ParentSessionID: parent, DelegationDepth: depth, CascadeOnFailure: policy,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// terminateAndSettle terminates id over the session API and waits for every
// terminal claim, so the cascade and each cascaded descendant's hook have run.
func terminateAndSettle(t *testing.T, srv *sessionserver.Server, id string) {
	t.Helper()
	rr := sessionRequest(t, srv.Handler(), http.MethodPost, "/v1/sessions/"+id+"/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate %s: status %d, body %s", id, rr.Code, rr.Body.String())
	}
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("WaitTerminalWork: %v", err)
	}
}

// fallbackAuditSessions returns the ids of the sessions a
// `session.cascade_applied` row with reason `orphan_cap_fallback` names.
func fallbackAuditSessions(t *testing.T, sink *orphanCapAuditSink) []string {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var ids []string
	for _, ev := range sink.events {
		if ev.EventType != "session.cascade_applied" {
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(ev.Detail), &detail); err != nil {
			t.Fatalf("cascade_applied detail not JSON: %v", err)
		}
		if detail["reason"] == "orphan_cap_fallback" {
			ids = append(ids, ev.SessionID)
		}
	}
	return ids
}

func assertStates(t *testing.T, store sessionstore.Store, want map[string]session.State) {
	t.Helper()
	for id, st := range want {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != st {
			t.Errorf("%s state = %q, want %q", id, row.State, st)
		}
	}
}

func assertFallbackAudit(t *testing.T, sink *orphanCapAuditSink, want []string) {
	t.Helper()
	got := fallbackAuditSessions(t, sink)
	if len(got) != len(want) {
		t.Fatalf("orphan_cap_fallback audit rows name %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("orphan_cap_fallback audit row %d names %q, want %q", i, got[i], want[i])
		}
	}
}

// spec: §8.10 (maxOrphanTasksPerTenant fallback), §8.10 (Orphan cleanup)
//
// Every node the cascade cancels resolves its policy with the orphan-cap
// fallback, the top session and each cancelled descendant alike. A `detach`
// descendant whose cancel leaves the tenant over the cap has its children
// cancelled and gets its own `cascade_applied` row; within the cap its
// children keep running. The mid-tree case starts the cascade under a live
// root, where only a count over the orphan-cleanup selection (every live
// delegated session with a terminal ancestor) sees the descendant's children
// as orphans: a count keyed on the tree root sees none and lets the detach
// stand. F-8.10.26.
func TestCascadeAppliesOrphanCapFallbackToEveryCancelledNode_spec_8_10(t *testing.T) {
	cases := []struct {
		name      string
		cap       int
		seed      func(t *testing.T, store sessionstore.Store)
		terminate string
		want      map[string]session.State
		wantAudit []string
	}{
		{
			name: "top-level detach over the cap cancels its children",
			cap:  1,
			seed: func(t *testing.T, store sessionstore.Store) {
				capNode(t, store, "top", "", 0, session.CascadeDetach)
				capNode(t, store, "kid_a", "top", 1, "")
				capNode(t, store, "kid_b", "top", 1, "")
			},
			terminate: "top",
			want:      map[string]session.State{"kid_a": session.StateCancelled, "kid_b": session.StateCancelled},
			wantAudit: []string{"top"},
		},
		{
			name: "cancelled detach descendant over the cap has its children cancelled",
			cap:  1,
			seed: func(t *testing.T, store sessionstore.Store) {
				capNode(t, store, "top", "", 0, session.CascadeCancelAll)
				capNode(t, store, "mid", "top", 1, session.CascadeDetach)
				capNode(t, store, "leaf_a", "mid", 2, "")
				capNode(t, store, "leaf_b", "mid", 2, "")
			},
			terminate: "top",
			want: map[string]session.State{
				"mid": session.StateCancelled, "leaf_a": session.StateCancelled, "leaf_b": session.StateCancelled,
			},
			wantAudit: []string{"mid"},
		},
		{
			name: "cancelled detach descendant within the cap leaves its children running",
			cap:  2,
			seed: func(t *testing.T, store sessionstore.Store) {
				capNode(t, store, "top", "", 0, session.CascadeCancelAll)
				capNode(t, store, "mid", "top", 1, session.CascadeDetach)
				capNode(t, store, "leaf_a", "mid", 2, "")
				capNode(t, store, "leaf_b", "mid", 2, "")
			},
			terminate: "top",
			want: map[string]session.State{
				"mid": session.StateCancelled, "leaf_a": session.StateRunning, "leaf_b": session.StateRunning,
			},
		},
		{
			name: "cascade from a mid-tree session under a live root counts the descendant's children",
			cap:  1,
			seed: func(t *testing.T, store sessionstore.Store) {
				capNode(t, store, "live_root", "", 0, session.CascadeCancelAll)
				capNode(t, store, "start", "live_root", 1, session.CascadeCancelAll)
				capNode(t, store, "mid", "start", 2, session.CascadeDetach)
				capNode(t, store, "leaf_a", "mid", 3, "")
				capNode(t, store, "leaf_b", "mid", 3, "")
			},
			terminate: "start",
			want: map[string]session.State{
				"live_root": session.StateRunning,
				"mid":       session.StateCancelled, "leaf_a": session.StateCancelled, "leaf_b": session.StateCancelled,
			},
			wantAudit: []string{"mid"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			sink := &orphanCapAuditSink{}
			srv := sessionserver.New(store, sessionserver.Options{
				DevTenantHeader: true, MaxOrphanTasksPerTenant: tc.cap, LifecycleAuditSink: sink,
			})
			tc.seed(t, store)
			terminateAndSettle(t, srv, tc.terminate)
			assertStates(t, store, tc.want)
			assertFallbackAudit(t, sink, tc.wantAudit)
		})
	}
}

// capCountErrorStore fails the replica listing the orphan-cap count reads,
// while the primary listing the cascade walk reads still succeeds.
type capCountErrorStore struct {
	sessionstore.Store
}

func (s capCountErrorStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	if !f.Primary {
		return nil, errors.New("replica unavailable")
	}
	return s.Store.List(ctx, tenantID, f)
}

// spec: §8.10 (maxOrphanTasksPerTenant fallback)
//
// A store error while counting orphans reports "not over cap", so a transient
// failure never escalates `detach` to `cancel_all`: the cancelled `detach`
// descendant's children keep running and no fallback row is written, though
// the tenant is over the cap.
func TestOrphanCapCountErrorReportsNotOverCap_spec_8_10(t *testing.T) {
	inner := memstore.New()
	sink := &orphanCapAuditSink{}
	srv := sessionserver.New(capCountErrorStore{Store: inner}, sessionserver.Options{
		DevTenantHeader: true, MaxOrphanTasksPerTenant: 1, LifecycleAuditSink: sink,
	})
	capNode(t, inner, "top", "", 0, session.CascadeCancelAll)
	capNode(t, inner, "mid", "top", 1, session.CascadeDetach)
	capNode(t, inner, "leaf_a", "mid", 2, "")
	capNode(t, inner, "leaf_b", "mid", 2, "")

	terminateAndSettle(t, srv, "top")
	assertStates(t, inner, map[string]session.State{
		"mid": session.StateCancelled, "leaf_a": session.StateRunning, "leaf_b": session.StateRunning,
	})
	assertFallbackAudit(t, sink, nil)
}

// preemptingStore commits the target session `cancelled` directly, as a
// concurrent terminal writer would, immediately before the first Update the
// cascade makes to it, so the cascade's own write is refused.
type preemptingStore struct {
	sessionstore.Store

	target string
	once   sync.Once
}

func (s *preemptingStore) Update(ctx context.Context, tenantID, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if id == s.target {
		var preemptErr error
		s.once.Do(func() {
			_, preemptErr = s.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
				r.State, r.TerminatedAt = session.StateCancelled, time.Now()
				return nil
			})
		})
		if preemptErr != nil {
			return sessionstore.Session{}, preemptErr
		}
	}
	return s.Store.Update(ctx, tenantID, id, fn)
}

// spec: §8.10 (maxOrphanTasksPerTenant fallback), §7.2
//
// The cascade evaluates a descendant's effective policy only on the row its
// own terminal write committed. When another writer ends an over-cap `detach`
// descendant first, the cascade's write is refused, so the walk records no
// fallback row for that descendant and queues none of its children: the
// children stay running for that writer's own cascade or the orphan sweeper.
func TestCascadeSkipsFallbackForDescendantAnotherWriterEnded_spec_8_10(t *testing.T) {
	inner := memstore.New()
	store := &preemptingStore{Store: inner, target: "mid"}
	sink := &orphanCapAuditSink{}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true, MaxOrphanTasksPerTenant: 1, LifecycleAuditSink: sink,
	})
	capNode(t, inner, "top", "", 0, session.CascadeCancelAll)
	capNode(t, inner, "mid", "top", 1, session.CascadeDetach)
	capNode(t, inner, "leaf_a", "mid", 2, "")
	capNode(t, inner, "leaf_b", "mid", 2, "")

	terminateAndSettle(t, srv, "top")
	assertStates(t, inner, map[string]session.State{
		"mid": session.StateCancelled, "leaf_a": session.StateRunning, "leaf_b": session.StateRunning,
	})
	assertFallbackAudit(t, sink, nil)
}
