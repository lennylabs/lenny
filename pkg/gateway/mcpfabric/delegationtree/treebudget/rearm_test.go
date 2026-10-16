// SPDX-License-Identifier: MIT

package treebudget_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
)

// spec: §8.3 (Delegation Policy and Lease)
// The budget-key TTL default outlives the 7-day suspended-session lifetime.
func TestDefaultBudgetKeyTTLOutlivesTheSuspendedLifetime_spec_8_3(t *testing.T) {
	if treebudget.DefaultTTL != 8*24*time.Hour {
		t.Fatalf("DefaultTTL = %v, want 8 days", treebudget.DefaultTTL)
	}
	if treebudget.DefaultTTL <= 604800*time.Second {
		t.Fatal("DefaultTTL does not exceed the default suspended-session lifetime")
	}
}

// spec: §8.3 (Delegation Policy and Lease)
// The Reserver arms created keys with the configured TTL, and RearmTree sets
// the TTL of the root's tree-wide, high-watermark, grant, and named parents'
// keys without creating absent keys or touching another root's keys.
func TestRearmTreeSetsTheTreesKeyTTLs_spec_8_3(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	r := treebudget.New(cl, 3*time.Hour)

	if _, err := r.Reserve(ctx, treebudget.Reservation{
		RootSessionID: "root", ParentSessionID: "p1",
		TreeSizeDelta: 1, TreeMemoryDelta: 1, ParallelChildrenDelta: 1, ChildrenTotalDelta: 1, TokenDelta: 1,
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got := mr.TTL("{root}:dlg:tokens"); got != 3*time.Hour {
		t.Fatalf("a created key's TTL = %v, want the configured 3h", got)
	}
	// Shorten every key so the re-arm is observable, and seed another root.
	for _, k := range mr.Keys() {
		mr.SetTTL(k, time.Minute)
	}
	if err := mr.Set("{other}:dlg:tokens", "1"); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL("{other}:dlg:tokens", time.Minute)

	if err := r.RearmTree(ctx, "root", []string{"p1", "p2"}); err != nil {
		t.Fatalf("RearmTree: %v", err)
	}
	for _, k := range []string{
		"{root}:dlg:tree_size", "{root}:dlg:tree_memory", "{root}:dlg:tokens",
		"{root}:dlg:parallel_children_hwm", "{root}:dlg:parallel_children:p1", "{root}:dlg:children_total:p1",
	} {
		if got := mr.TTL(k); got != 3*time.Hour {
			t.Errorf("TTL(%s) = %v, want 3h after the re-arm", k, got)
		}
	}
	for _, k := range []string{"{root}:dlg:parallel_children:p2", "{root}:dlg:token_grant"} {
		if mr.Exists(k) {
			t.Errorf("RearmTree created the absent key %s", k)
		}
	}
	if got := mr.TTL("{other}:dlg:tokens"); got != time.Minute {
		t.Errorf("another root's key TTL = %v, want it untouched", got)
	}
	if err := r.RearmTree(ctx, "", nil); err == nil {
		t.Error("RearmTree with no root returned no error")
	}
}

// spec: §8.3 (Delegation Policy and Lease), §12.8 (Compliance Interfaces)
// PurgeRoot on a Redis cluster deletes every {root}:dlg:* key and no other
// key, scanning only the node that owns the {root} hash slot. A keyless SCAN
// would reach an arbitrary shard, so each round plants a decoy that matches
// the pattern on the other shard; a purge that scanned it deletes the decoy.
func TestPurgeRootScansTheOwningClusterNode_spec_8_3(t *testing.T) {
	ctx := context.Background()
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	cc := redis.NewClusterClient(&redis.ClusterOptions{
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			return []redis.ClusterSlot{
				{Start: 0, End: 8191, Nodes: []redis.ClusterNode{{Addr: a.Addr()}}},
				{Start: 8192, End: 16383, Nodes: []redis.ClusterNode{{Addr: b.Addr()}}},
			}, nil
		},
	})
	t.Cleanup(func() { _ = cc.Close() })
	r := treebudget.New(cc, 0)
	for round := 0; round < 8; round++ {
		root := fmt.Sprintf("root-%d", round)
		owner, err := cc.MasterForKey(ctx, "{"+root+"}:dlg:tokens")
		if err != nil {
			t.Fatalf("MasterForKey: %v", err)
		}
		// The node client's address drops the host, so the owner is found
		// by the shard a write through it lands on.
		probe := "{" + root + "}:dlg:probe"
		if err := owner.Set(ctx, probe, "1", 0).Err(); err != nil {
			t.Fatal(err)
		}
		own, other := a, b
		if b.Exists(probe) {
			own, other = b, a
		}
		own.Del(probe)
		keys := []string{"{" + root + "}:dlg:tokens", "{" + root + "}:dlg:tree_size", "{" + root + "}:dlg:children_total:p1"}
		for _, k := range keys {
			if err := own.Set(k, "1"); err != nil {
				t.Fatal(err)
			}
		}
		decoy := "{" + root + "}:dlg:decoy"
		if err := other.Set(decoy, "1"); err != nil {
			t.Fatal(err)
		}
		bystander := "{bystander}:dlg:tokens"
		if err := own.Set(bystander, "1"); err != nil {
			t.Fatal(err)
		}
		n, err := r.PurgeRoot(ctx, root)
		if err != nil {
			t.Fatalf("round %d PurgeRoot: %v", round, err)
		}
		if n != len(keys) {
			t.Fatalf("round %d PurgeRoot deleted %d keys, want %d", round, n, len(keys))
		}
		for _, k := range keys {
			if own.Exists(k) {
				t.Fatalf("round %d: %s survived the purge", round, k)
			}
		}
		if !other.Exists(decoy) {
			t.Fatalf("round %d: the purge scanned the shard that does not own the slot", round)
		}
		if !own.Exists(bystander) {
			t.Fatalf("round %d: the purge deleted another root's key", round)
		}
		other.Del(decoy)
	}
}
