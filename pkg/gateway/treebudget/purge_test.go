// SPDX-License-Identifier: MIT

// Coverage for the §12.8 step-16 delegation-budget erasure: PurgeRoot
// deletes every `{root_session_id}:dlg:*` key for one tree (tree-wide
// counters, per-parent reservation keys, and the high-watermark) via a
// slot-local SCAN, leaving other trees' keys intact.
//
// spec: §12.8 line 831 (step 16 — slot-local SCAN of {root}:dlg:*).
package treebudget_test

import (
	"context"
	"sort"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestPurgeRoot_DeletesWholeTreeKeysOnly_spec_12_8_831(t *testing.T) {
	r, mr := newReserver(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	ctx := context.Background()

	root := "11111111-1111-1111-1111-111111111111"
	other := "22222222-2222-2222-2222-222222222222"
	treeKeys := []string{
		"{" + root + "}:dlg:tokens",
		"{" + root + "}:dlg:tree_size",
		"{" + root + "}:dlg:tree_memory",
		"{" + root + "}:dlg:parallel_children:p1",
		"{" + root + "}:dlg:children_total:p1",
		"{" + root + "}:dlg:parallel_children_hwm",
	}
	for _, k := range treeKeys {
		if err := cl.Set(ctx, k, "1", 0).Err(); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	otherKey := "{" + other + "}:dlg:tokens"
	if err := cl.Set(ctx, otherKey, "1", 0).Err(); err != nil {
		t.Fatalf("seed other: %v", err)
	}

	n, err := r.PurgeRoot(ctx, root)
	if err != nil {
		t.Fatalf("PurgeRoot: %v", err)
	}
	if n != len(treeKeys) {
		t.Fatalf("purged = %d, want %d", n, len(treeKeys))
	}

	remaining, err := cl.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	sort.Strings(remaining)
	if len(remaining) != 1 || remaining[0] != otherKey {
		t.Fatalf("remaining keys = %v, want only %s", remaining, otherKey)
	}
}

func TestPurgeRoot_NoKeys_spec_12_8_831(t *testing.T) {
	r, _ := newReserver(t)
	n, err := r.PurgeRoot(context.Background(), "33333333-3333-3333-3333-333333333333")
	if err != nil {
		t.Fatalf("PurgeRoot: %v", err)
	}
	if n != 0 {
		t.Fatalf("purged = %d, want 0", n)
	}
}

func TestPurgeRoot_RejectsEmptyRoot_spec_12_8_831(t *testing.T) {
	r, _ := newReserver(t)
	if _, err := r.PurgeRoot(context.Background(), ""); err == nil {
		t.Fatal("empty root should error")
	}
}
