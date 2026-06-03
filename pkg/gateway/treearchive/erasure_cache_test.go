// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-11 erasure path through the per-replica
// read cache: DeleteBySession writes through to the inner store and
// evicts the erased tree's cached nodes so a post-erasure read cannot
// serve a stale settled-child result from memory.
//
// spec: §12.8 line 826 (step 11).
package treearchive

import (
	"context"
	"errors"
	"testing"
)

func TestCachedDeleteBySessionWritesThroughAndEvicts(t *testing.T) {
	ctx := context.Background()
	inner := newCountingStore()
	c := NewCached(inner, 16)

	for _, n := range []ArchivedNode{cnode("r1", "n1"), cnode("r1", "n2"), cnode("r2", "n3")} {
		if err := c.Archive(ctx, n); err != nil {
			t.Fatalf("Archive: %v", err)
		}
	}

	n, err := c.DeleteBySession(ctx, "acme", "r1")
	if err != nil {
		t.Fatalf("DeleteBySession: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted = %d, want 2", n)
	}
	if inner.deletes != 1 {
		t.Fatalf("inner deletes = %d, want 1 (write-through)", inner.deletes)
	}

	// The erased tree's node is gone from both cache and inner store: a
	// follow-up read misses the cache (inner getByNode increments) and the
	// inner store reports ErrNotFound.
	before := inner.getByNodes
	if _, err := c.GetByNode(ctx, "acme", "n1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("erased node served from cache: err = %v", err)
	}
	if inner.getByNodes != before+1 {
		t.Fatalf("erased-node read did not fall through to inner store")
	}

	// A sibling tree's node still serves from the cache (no inner call).
	beforeR2 := inner.getByNodes
	if _, err := c.GetByNode(ctx, "acme", "n3"); err != nil {
		t.Fatalf("sibling tree node should still be cached: %v", err)
	}
	if inner.getByNodes != beforeR2 {
		t.Fatalf("sibling-tree read fell through to inner store; eviction over-reached")
	}
}
