// SPDX-License-Identifier: MIT

package treebudget_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
)

// spec: §11.2 line 29, line 44 — Snapshot reads the tree-wide counters a
// reserve advanced so the periodic checkpoint can persist them.
func TestSnapshotReadsReservedCounters_spec_11_2_29(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	ctx := context.Background()
	if _, err := r.Reserve(ctx, treebudget.Reservation{
		RootSessionID:   "rootS",
		ParentSessionID: "rootS",
		TreeSizeCap:     50,
		TreeSizeDelta:   1,
		TreeMemoryCap:   2097152,
		TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
		TokenCap:        10000,
		TokenDelta:      1500,
	}); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	got, err := r.Snapshot(ctx, "rootS")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got.TreeSize != 1 || got.TreeMemory != treebudget.PerNodeMemoryBytes || got.Tokens != 1500 {
		t.Fatalf("snapshot = %+v, want {TreeSize:1 TreeMemory:%d Tokens:1500}", got, treebudget.PerNodeMemoryBytes)
	}
}

// spec: §11.2 line 29 — a tree that admitted no delegation has no keys;
// Snapshot reads each absent counter as zero rather than erroring.
func TestSnapshotAbsentTreeIsZero_spec_11_2_29(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	got, err := r.Snapshot(context.Background(), "never-delegated")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got != (treebudget.TreeCounters{}) {
		t.Fatalf("snapshot of absent tree = %+v, want zero", got)
	}
}

// spec: §11.2 line 48; §12.4 line 218 — Restore writes the reconstructed
// counters back so the fast-path reserve enforces against the restored
// value. A reserve after Restore advances from the restored base.
func TestRestoreSeedsCountersForFastPath_spec_11_2_48(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	if err := r.Restore(ctx, "rootR", treebudget.TreeCounters{TreeSize: 7, Tokens: 9000, TreeMemory: 84 * 1024}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := r.Snapshot(ctx, "rootR")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got.TreeSize != 7 || got.Tokens != 9000 || got.TreeMemory != 84*1024 {
		t.Fatalf("after restore snapshot = %+v, want {7 9000 86016}", got)
	}
	// Restore refreshes the GC TTL on each key so a restored long-lived
	// tree's counters do not immediately lapse.
	if ttl := mr.TTL("{rootR}:dlg:tree_size"); ttl <= 0 {
		t.Fatalf("restore did not set a TTL on the tree_size key, got %s", ttl)
	}

	// A reserve after Restore must continue from the restored base: a
	// tree_size of 7 + 1 = 8, proving Restore seeded the live counter the
	// reserve script reads.
	res, err := r.Reserve(ctx, treebudget.Reservation{
		RootSessionID:   "rootR",
		ParentSessionID: "rootR",
		TreeSizeCap:     50,
		TreeSizeDelta:   1,
		TreeMemoryCap:   2097152,
		TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
		TokenCap:        20000,
		TokenDelta:      500,
	})
	if err != nil {
		t.Fatalf("reserve after restore: %v", err)
	}
	if res.TreeSize != 8 || res.Tokens != 9500 {
		t.Fatalf("reserve after restore advanced to %+v, want TreeSize 8 Tokens 9500", res)
	}
}
