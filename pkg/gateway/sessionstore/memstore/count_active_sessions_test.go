// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §11.2 per-tenant concurrent-session quota count.

func seedSession(t *testing.T, s *memstore.Store, id, tenant string, st session.State) {
	t.Helper()
	if err := s.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: tenant, State: st,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestCountActiveSessions_CountsOnlyNonTerminal_spec_11_2 verifies the
// count includes every non-terminal state and excludes the four
// terminal states named by session.TerminalStates().
func TestCountActiveSessions_CountsOnlyNonTerminal_spec_11_2(t *testing.T) {
	s := memstore.New()
	seedSession(t, s, "a", "acme", session.StateCreated)
	seedSession(t, s, "b", "acme", session.StateRunning)
	for i, st := range session.TerminalStates() {
		seedSession(t, s, string(rune('t'+i)), "acme", st)
	}
	got, err := s.CountActiveSessions(context.Background(), "acme")
	if err != nil {
		t.Fatalf("CountActiveSessions: %v", err)
	}
	if got != 2 {
		t.Fatalf("active count = %d, want 2 (created + running; terminals excluded)", got)
	}
}

// TestCountActiveSessions_TenantIsolation_spec_11_2 verifies the count
// is scoped to the requested tenant and never bleeds another tenant's
// sessions into the quota.
func TestCountActiveSessions_TenantIsolation_spec_11_2(t *testing.T) {
	s := memstore.New()
	seedSession(t, s, "a", "acme", session.StateRunning)
	seedSession(t, s, "b", "acme", session.StateRunning)
	seedSession(t, s, "c", "globex", session.StateRunning)
	got, err := s.CountActiveSessions(context.Background(), "acme")
	if err != nil {
		t.Fatalf("CountActiveSessions: %v", err)
	}
	if got != 2 {
		t.Fatalf("acme active count = %d, want 2 (globex excluded)", got)
	}
}

// TestCountActiveSessions_EmptyTenant_spec_11_2 verifies an empty
// tenant id and an unknown tenant both return (0, nil).
func TestCountActiveSessions_EmptyTenant_spec_11_2(t *testing.T) {
	s := memstore.New()
	seedSession(t, s, "a", "acme", session.StateRunning)
	if got, err := s.CountActiveSessions(context.Background(), ""); err != nil || got != 0 {
		t.Fatalf("empty tenant = (%d, %v), want (0, nil)", got, err)
	}
	if got, err := s.CountActiveSessions(context.Background(), "unknown"); err != nil || got != 0 {
		t.Fatalf("unknown tenant = (%d, %v), want (0, nil)", got, err)
	}
}
