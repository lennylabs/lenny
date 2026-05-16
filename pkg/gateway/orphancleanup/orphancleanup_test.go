// SPDX-License-Identifier: MIT

package orphancleanup_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
)

// seed inserts a session row with the given state and parent.
func seed(t *testing.T, store sessionstore.Store, id, parent string, state session.State, updatedAt time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: state, ParentSessionID: parent,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestTickTerminatesOrphanPastCascadeTimeout(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The root terminated at base; the child is still running.
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	// Sweep well past the default 3600s cascade timeout.
	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 1 {
		t.Errorf("terminated count = %d, want 1", n)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_child")
	if row.State != session.StateExpired {
		t.Errorf("orphan state = %q, want expired", row.State)
	}
}

func TestTickLeavesOrphanWithinCascadeWindow(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	// Ten minutes after the root terminated — well inside the 3600s window.
	n, err := sw.Tick(context.Background(), base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("terminated count = %d, want 0 — still inside the cascade window", n)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_child")
	if row.State != session.StateRunning {
		t.Errorf("orphan state = %q, want running", row.State)
	}
}

func TestTickLeavesChildOfActiveRoot(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// The root is still running — its children are not orphans.
	seed(t, store, "sess_root", "", session.StateRunning, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	n, err := sw.Tick(context.Background(), base.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("terminated count = %d, want 0 — the root is still active", n)
	}
}

func TestTickTerminatesDeepOrphanSubtree(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// root (terminal) → mid (running) → leaf (running): both mid and
	// leaf are orphans of the terminated root.
	seed(t, store, "sess_root", "", session.StateFailed, base)
	seed(t, store, "sess_mid", "sess_root", session.StateRunning, base)
	seed(t, store, "sess_leaf", "sess_mid", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	n, err := sw.Tick(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if n != 2 {
		t.Errorf("terminated count = %d, want 2 (mid + leaf)", n)
	}
	for _, id := range []string{"sess_mid", "sess_leaf"} {
		row, _ := store.Get(context.Background(), "acme", id)
		if row.State != session.StateExpired {
			t.Errorf("%s state = %q, want expired", id, row.State)
		}
	}
}

func TestTickArchivesTerminatedOrphan(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"},
		orphancleanup.Options{Archive: archive})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := archive.GetByNode(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("terminated orphan was not archived: %v", err)
	}
	if got.State != string(session.StateExpired) {
		t.Errorf("archived state = %q, want expired", got.State)
	}
	if got.RootSessionID != "sess_root" {
		t.Errorf("archived RootSessionID = %q, want sess_root", got.RootSessionID)
	}
}

func TestTickIsIdempotent(t *testing.T) {
	store := memstore.New()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, store, "sess_root", "", session.StateCompleted, base)
	seed(t, store, "sess_child", "sess_root", session.StateRunning, base)
	sw := orphancleanup.New(store, orphancleanup.StaticTenants{"acme"}, orphancleanup.Options{})

	if _, err := sw.Tick(context.Background(), base.Add(2*time.Hour)); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	// A second sweep finds nothing new — the orphan is already terminal.
	n, err := sw.Tick(context.Background(), base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if n != 0 {
		t.Errorf("second sweep terminated %d, want 0", n)
	}
}
