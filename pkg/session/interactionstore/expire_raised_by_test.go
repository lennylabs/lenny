// SPDX-License-Identifier: MIT

package interactionstore

import (
	"context"
	"errors"
	"testing"
)

// seedExpiryRows records the rows ExpireRaisedBy must tell apart and returns
// the id of the one it must delete.
func seedExpiryRows(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	rows := []Interaction{
		// The session's own pending elicitation: expired.
		{
			ID: "raised", Kind: KindElicitation, SessionID: "sess-a", TenantID: "acme", UserID: "alice",
			Phase: PhasePending, Detail: map[string]any{DetailOriginPod: "sess-a"},
		},
		// A child's elicitation the chain holds for sess-a: kept.
		{
			ID: "child", Kind: KindElicitation, SessionID: "sess-a", TenantID: "acme", UserID: "alice",
			Phase: PhasePending, Detail: map[string]any{DetailOriginPod: "sess-child"},
		},
		// sess-a raised it, but it already resolved: kept.
		{
			ID: "answered", Kind: KindElicitation, SessionID: "sess-a", TenantID: "acme", UserID: "alice",
			Phase: PhaseResponded, Detail: map[string]any{DetailOriginPod: "sess-a"},
		},
		// A lease-extension elicitation carries no originPod: kept.
		{
			ID: "lease-ext", Kind: KindElicitation, SessionID: "sess-a", TenantID: "acme", UserID: "alice",
			Phase: PhasePending, Detail: map[string]any{"message": "extend?"},
		},
		// A tool-use row naming the origin: kept.
		{
			ID: "tool", Kind: KindToolUse, SessionID: "sess-a", TenantID: "acme", UserID: "alice",
			Phase: PhasePending, Detail: map[string]any{DetailOriginPod: "sess-a"},
		},
		// Another tenant's row with the same origin: kept.
		{
			ID: "other-tenant", Kind: KindElicitation, SessionID: "sess-a", TenantID: "globex", UserID: "bob",
			Phase: PhasePending, Detail: map[string]any{DetailOriginPod: "sess-a"},
		},
	}
	for _, in := range rows {
		if err := s.Put(ctx, in); err != nil {
			t.Fatalf("Put %s: %v", in.ID, err)
		}
	}
}

// spec: §9.2 (Elicitation Chain), §8.2 (Delegation Mechanism)
// ExpireRaisedBy deletes only the pending elicitations the session's pod
// raised in the tenant, keeping one it resolves for a child, a responded one,
// a lease-extension elicitation, a tool-use row, and another tenant's row;
// a later Resolve of the deleted row returns ErrNotFound.
func TestExpireRaisedByDeletesOnlyTheSessionsPendingElicitations_spec_9_2(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	seedExpiryRows(t, m)
	n, err := m.ExpireRaisedBy(ctx, "acme", "sess-a")
	if err != nil || n != 1 {
		t.Fatalf("ExpireRaisedBy = %d, %v; want 1", n, err)
	}
	if _, err := m.Resolve(ctx, "acme", "sess-a", "alice", "raised", func(*Interaction) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve of the expired elicitation = %v, want ErrNotFound", err)
	}
	for _, id := range []string{"child", "answered", "lease-ext", "tool"} {
		if _, err := m.Get(ctx, "acme", "sess-a", "alice", id); err != nil {
			t.Errorf("row %s was removed: %v", id, err)
		}
	}
	if _, err := m.Get(ctx, "globex", "sess-a", "bob", "other-tenant"); err != nil {
		t.Errorf("another tenant's row was removed: %v", err)
	}
	if n, err := m.ExpireRaisedBy(ctx, "acme", "sess-a"); err != nil || n != 0 {
		t.Fatalf("repeat ExpireRaisedBy = %d, %v; want 0", n, err)
	}
	if n, err := m.ExpireRaisedBy(ctx, "acme", ""); err != nil || n != 0 {
		t.Fatalf("ExpireRaisedBy with no origin = %d, %v; want 0", n, err)
	}
}
