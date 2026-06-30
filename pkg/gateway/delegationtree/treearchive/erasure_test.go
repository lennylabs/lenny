// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-11 session_tree_archive erasure:
// DeleteBySession removes every archived node of one tree, scoped to the
// tenant and root, leaving sibling trees and other tenants intact.
//
// spec: §12.8 line 826 (step 11), lines 807-808 (FK precedence).
package treearchive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
)

func anode(tenant, root, node string) treearchive.ArchivedNode {
	return treearchive.ArchivedNode{
		TenantID:      tenant,
		RootSessionID: root,
		NodeSessionID: node,
		State:         "completed",
		Result:        `{"taskId":"` + node + `"}`,
		SettledAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestMemoryDeleteBySession_RemovesOneTreeOnly_spec_12_8_826(t *testing.T) {
	ctx := context.Background()
	m := treearchive.NewMemory()
	for _, n := range []treearchive.ArchivedNode{
		anode("acme", "r1", "n1"),
		anode("acme", "r1", "n2"),
		anode("acme", "r2", "n3"),
		anode("globex", "r1", "n4"),
	} {
		if err := m.Archive(ctx, n); err != nil {
			t.Fatalf("Archive: %v", err)
		}
	}

	n, err := m.DeleteBySession(ctx, "acme", "r1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}

	if _, err := m.Get(ctx, "acme", "r1", "n1"); !errors.Is(err, treearchive.ErrNotFound) {
		t.Fatalf("acme/r1/n1 survived: err = %v", err)
	}
	if _, err := m.Get(ctx, "acme", "r2", "n3"); err != nil {
		t.Fatalf("acme/r2/n3 should survive: %v", err)
	}
	if _, err := m.Get(ctx, "globex", "r1", "n4"); err != nil {
		t.Fatalf("globex/r1/n4 should survive (cross-tenant): %v", err)
	}
}

func TestMemoryDeleteBySession_NonRootIsNoOp_spec_12_8_826(t *testing.T) {
	ctx := context.Background()
	m := treearchive.NewMemory()
	if err := m.Archive(ctx, anode("acme", "r1", "n1")); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	// A node session id that is not a tree root matches no archive row.
	n, err := m.DeleteBySession(ctx, "acme", "n1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted = %d, want 0", n)
	}
}

func TestMemoryDeleteBySession_RejectsEmptyArgs_spec_12_8_826(t *testing.T) {
	m := treearchive.NewMemory()
	if _, err := m.DeleteBySession(context.Background(), "", "r1"); err == nil {
		t.Fatal("empty tenant should error")
	}
	if _, err := m.DeleteBySession(context.Background(), "acme", ""); err == nil {
		t.Fatal("empty root should error")
	}
}
