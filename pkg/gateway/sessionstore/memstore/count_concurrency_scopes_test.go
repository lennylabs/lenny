// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §11.1 line 8/9 — the global, per-user, per-runtime, and per-user
// active-delegated-children concurrent-session admission counts.

func seedFull(t *testing.T, s *memstore.Store, sess sessionstore.Session) {
	t.Helper()
	if sess.State == "" {
		sess.State = session.StateRunning
	}
	if err := s.Create(context.Background(), sess); err != nil {
		t.Fatalf("seed %s: %v", sess.ID, err)
	}
}

// TestCountActiveSessionsByUser_spec_11_1 verifies the per-user count
// includes every non-terminal session owned by the user, is scoped to
// the tenant, and excludes terminal and other-user rows.
func TestCountActiveSessionsByUser_spec_11_1(t *testing.T) {
	s := memstore.New()
	seedFull(t, s, sessionstore.Session{ID: "a", TenantID: "acme", UserID: "alice"})
	seedFull(t, s, sessionstore.Session{ID: "b", TenantID: "acme", UserID: "alice", State: session.StateCreated})
	seedFull(t, s, sessionstore.Session{ID: "c", TenantID: "acme", UserID: "bob"})
	seedFull(t, s, sessionstore.Session{ID: "d", TenantID: "acme", UserID: "alice", State: session.StateCompleted})
	seedFull(t, s, sessionstore.Session{ID: "e", TenantID: "globex", UserID: "alice"})

	got, err := s.CountActiveSessionsByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("CountActiveSessionsByUser: %v", err)
	}
	if got != 2 {
		t.Fatalf("alice active = %d, want 2 (a + b; completed, bob, and globex excluded)", got)
	}
	// Empty tenant or user short-circuits to zero.
	if got, err := s.CountActiveSessionsByUser(context.Background(), "", "alice"); err != nil || got != 0 {
		t.Fatalf("empty tenant = (%d, %v), want (0, nil)", got, err)
	}
	if got, err := s.CountActiveSessionsByUser(context.Background(), "acme", ""); err != nil || got != 0 {
		t.Fatalf("empty user = (%d, %v), want (0, nil)", got, err)
	}
}

// TestCountActiveSessionsByRuntime_spec_11_1 verifies the per-runtime
// count is scoped to (tenant, runtime) and excludes terminal rows.
func TestCountActiveSessionsByRuntime_spec_11_1(t *testing.T) {
	s := memstore.New()
	seedFull(t, s, sessionstore.Session{ID: "a", TenantID: "acme", RuntimeRef: "claude"})
	seedFull(t, s, sessionstore.Session{ID: "b", TenantID: "acme", RuntimeRef: "claude", State: session.StateStarting})
	seedFull(t, s, sessionstore.Session{ID: "c", TenantID: "acme", RuntimeRef: "gpt"})
	seedFull(t, s, sessionstore.Session{ID: "d", TenantID: "acme", RuntimeRef: "claude", State: session.StateFailed})

	got, err := s.CountActiveSessionsByRuntime(context.Background(), "acme", "claude")
	if err != nil {
		t.Fatalf("CountActiveSessionsByRuntime: %v", err)
	}
	if got != 2 {
		t.Fatalf("claude active = %d, want 2 (a + b; failed and gpt excluded)", got)
	}
	if got, err := s.CountActiveSessionsByRuntime(context.Background(), "acme", ""); err != nil || got != 0 {
		t.Fatalf("empty runtime = (%d, %v), want (0, nil)", got, err)
	}
}

// TestCountActiveSessionsGlobal_spec_11_1 verifies the global count spans
// every tenant and excludes terminal rows.
func TestCountActiveSessionsGlobal_spec_11_1(t *testing.T) {
	s := memstore.New()
	seedFull(t, s, sessionstore.Session{ID: "a", TenantID: "acme"})
	seedFull(t, s, sessionstore.Session{ID: "b", TenantID: "globex"})
	seedFull(t, s, sessionstore.Session{ID: "c", TenantID: "initech", State: session.StateCancelled})

	got, err := s.CountActiveSessionsGlobal(context.Background())
	if err != nil {
		t.Fatalf("CountActiveSessionsGlobal: %v", err)
	}
	if got != 2 {
		t.Fatalf("global active = %d, want 2 (acme + globex; cancelled excluded)", got)
	}
}

// TestCountActiveDelegatedChildrenByUser_spec_11_1 verifies only
// non-terminal delegated children (non-empty ParentSessionID) owned by
// the user within the tenant are counted.
func TestCountActiveDelegatedChildrenByUser_spec_11_1(t *testing.T) {
	s := memstore.New()
	// Two live delegated children for alice.
	seedFull(t, s, sessionstore.Session{ID: "c1", TenantID: "acme", UserID: "alice", ParentSessionID: "root"})
	seedFull(t, s, sessionstore.Session{ID: "c2", TenantID: "acme", UserID: "alice", ParentSessionID: "root2", State: session.StateCreated})
	// A root (non-delegated) session for alice — excluded.
	seedFull(t, s, sessionstore.Session{ID: "root", TenantID: "acme", UserID: "alice"})
	// A terminal delegated child — excluded.
	seedFull(t, s, sessionstore.Session{ID: "c3", TenantID: "acme", UserID: "alice", ParentSessionID: "root", State: session.StateCompleted})
	// A delegated child owned by bob — excluded.
	seedFull(t, s, sessionstore.Session{ID: "c4", TenantID: "acme", UserID: "bob", ParentSessionID: "root"})

	got, err := s.CountActiveDelegatedChildrenByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("CountActiveDelegatedChildrenByUser: %v", err)
	}
	if got != 2 {
		t.Fatalf("alice active children = %d, want 2 (c1 + c2; root, terminal, and bob excluded)", got)
	}
	if got, err := s.CountActiveDelegatedChildrenByUser(context.Background(), "acme", ""); err != nil || got != 0 {
		t.Fatalf("empty user = (%d, %v), want (0, nil)", got, err)
	}
}
