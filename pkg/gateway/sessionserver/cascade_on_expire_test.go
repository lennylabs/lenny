// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
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

// spec: §7.3 line 423 — "After expiry the session transitions to
// `expired` ... The gateway applies the session's cascadeOnFailure policy
// to all active children (same behavior as terminal failure after retry
// exhaustion)." F-7.3.11.
//
// The integration test wires the watchdog's TerminalHook to the Server
// (the same wiring cmd/lenny-gateway does at boot) and drives an
// awaiting_client_action row past the §11.3 maxAwaitingClientAction
// deadline. The terminal hook is the §7.3 line 423 cascade entry point:
// recordSessionCompleted → cascadeToChildren, which walks active
// descendants and cancels them per the parent's CascadeOnFailure.
func TestAwaitingExpiredAppliesCascadeViaTerminalHook_spec_7_3_11(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

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

	w := watchdog.New(store, staticTenantLister{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(srv)

	// Drive past the default 900s awaiting deadline.
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
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

// spec: §7.3 line 423 — a detach parent must NOT cancel its children
// on expiry; only the cancel_all cascade walks descendants. The
// terminal hook applies the parent's own CascadeOnFailure, so a detach
// parent leaves its children alive. F-7.3.11.
func TestAwaitingExpiredDetachLeavesChildrenRunning_spec_7_3_11(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{})

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

	w := watchdog.New(store, staticTenantLister{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(srv)
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	child, err := store.Get(context.Background(), "acme", "c_detach")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.State != session.StateRunning {
		t.Errorf("F-7.3.11: detach cascade should leave child running; state = %q", child.State)
	}
}
