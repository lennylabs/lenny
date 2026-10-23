// SPDX-License-Identifier: MIT

package treearchive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
)

// seedArchive archives two acme trees and one globex tree in s.
func seedArchive(t *testing.T, s treearchive.Store) {
	t.Helper()
	for _, n := range []treearchive.ArchivedNode{
		anode("acme", "r1", "n1"),
		anode("acme", "r1", "n2"),
		anode("acme", "r2", "n3"),
		anode("globex", "r9", "n9"),
	} {
		if err := s.Archive(context.Background(), n); err != nil {
			t.Fatalf("Archive: %v", err)
		}
	}
}

// TestDeleteByTenantRemovesEveryTreeOfTheTenant_spec_12_8 asserts the
// tenant Phase 4 primitive on both the in-memory store and the cached
// wrapper: every archived node of the tenant is removed across all its
// trees, the count is returned, another tenant's nodes survive, a second
// call is an idempotent zero, and an empty tenant is rejected.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestDeleteByTenantRemovesEveryTreeOfTheTenant_spec_12_8(t *testing.T) {
	stores := map[string]func() treearchive.Store{
		"memory": func() treearchive.Store { return treearchive.NewMemory() },
		"cached": func() treearchive.Store { return treearchive.NewCached(treearchive.NewMemory(), 8) },
	}
	for name, mk := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := mk()
			seedArchive(t, s)
			// Warm the cache so a stale read would be served from memory.
			if _, err := s.GetByNode(ctx, "acme", "n1"); err != nil {
				t.Fatalf("warm read: %v", err)
			}

			n, err := s.DeleteByTenant(ctx, "acme")
			if err != nil {
				t.Fatalf("DeleteByTenant: %v", err)
			}
			if n != 3 {
				t.Fatalf("deleted = %d, want 3", n)
			}
			for _, id := range []string{"n1", "n2", "n3"} {
				if _, err := s.GetByNode(ctx, "acme", id); !errors.Is(err, treearchive.ErrNotFound) {
					t.Fatalf("acme node %s survived tenant deletion: err = %v", id, err)
				}
			}
			if _, err := s.GetByNode(ctx, "globex", "n9"); err != nil {
				t.Fatalf("globex node must survive acme deletion: %v", err)
			}
			if n, err := s.DeleteByTenant(ctx, "acme"); err != nil || n != 0 {
				t.Fatalf("second DeleteByTenant = (%d, %v), want (0, nil)", n, err)
			}
			if _, err := s.DeleteByTenant(ctx, ""); err == nil {
				t.Fatal("DeleteByTenant(\"\") succeeded; an empty tenant must be rejected")
			}
		})
	}
}

// failingDeleteStore fails DeleteByTenant so the cached wrapper's error
// path can be exercised.
type failingDeleteStore struct {
	treearchive.Store
}

func (failingDeleteStore) DeleteByTenant(context.Context, string) (int, error) {
	return 0, errors.New("postgres down")
}

// TestCachedDeleteByTenantFailureKeepsCache_spec_12_8 asserts that a
// failed write-through leaves the cache untouched and returns the error,
// so a retried Phase 4 sees a consistent cache.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4).
func TestCachedDeleteByTenantFailureKeepsCache_spec_12_8(t *testing.T) {
	ctx := context.Background()
	inner := treearchive.NewMemory()
	seedArchive(t, inner)
	c := treearchive.NewCached(failingDeleteStore{Store: inner}, 8)
	if _, err := c.GetByNode(ctx, "acme", "n1"); err != nil {
		t.Fatalf("warm read: %v", err)
	}
	if _, err := c.DeleteByTenant(ctx, "acme"); err == nil {
		t.Fatal("DeleteByTenant succeeded over a failing inner store")
	}
	if c.Len() != 1 {
		t.Fatalf("cache len = %d after a failed delete, want 1", c.Len())
	}
}
