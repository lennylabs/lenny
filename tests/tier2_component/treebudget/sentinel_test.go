//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §8.3 delegation budget scripts against a
// real Redis server. miniredis implements its own Lua host and SET options,
// so the sentinel check, the first-reservation SET NX EX create, the
// skip-missing return, and the KEEPTTL clamp are pinned here against the
// Redis the gateway runs.
package treebudget_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

func reservation(root, parent string, tokens int64) treebudget.Reservation {
	return treebudget.Reservation{
		RootSessionID: root, ParentSessionID: parent,
		TreeSizeCap: 10, TreeSizeDelta: 1,
		TreeMemoryCap: 1 << 20, TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
		ParallelChildrenCap: 5, ParallelChildrenDelta: 1,
		ChildrenTotalCap: 5, ChildrenTotalDelta: 1,
		TokenCap: 10_000, TokenDelta: tokens,
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.3
// (Defense-in-depth TTL)
// diagnosis: a delegation budget script misbehaved on real Redis. Either an
// unmarked reserve or a return wrote a key while the tokens sentinel was
// absent (expired keys are silently recreated), the first-reservation create
// left a tree-wide key without the TTL or skipped the sentinel for a zero
// token slice, a return recreated a missing per-parent key, or a clamping
// return dropped the key's TTL or left it negative.
func TestBudgetScriptsSentinelAndTTLRulesOnRealRedis_spec_8_3(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	const ttl = 2 * time.Hour
	r := treebudget.New(rd.Client, ttl)

	// Absent sentinel without the flag: both scripts write nothing.
	if _, err := r.Reserve(ctx, reservation("root", "p1", 100)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("unmarked Reserve err = %v, want ErrBudgetKeysExpired", err)
	}
	if err := r.Return(ctx, reservation("root", "p1", 100)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("Return err = %v, want ErrBudgetKeysExpired", err)
	}
	if n, err := rd.Client.DBSize(ctx).Result(); err != nil || n != 0 {
		t.Fatalf("keys after expired runs = %d (%v), want 0", n, err)
	}

	// The flag creates the tree-wide keys with the TTL for a zero token delta.
	first := reservation("root", "p1", 0)
	first.CreateTree = true
	if _, err := r.Reserve(ctx, first); err != nil {
		t.Fatalf("marked Reserve: %v", err)
	}
	for _, k := range []string{"{root}:dlg:tree_size", "{root}:dlg:tree_memory", "{root}:dlg:tokens"} {
		got, err := rd.Client.TTL(ctx, k).Result()
		if err != nil || got <= 0 || got > ttl {
			t.Fatalf("TTL(%s) = %v (%v), want in (0, %v]", k, got, err, ttl)
		}
	}

	// A return skips a missing per-parent key.
	if err := rd.Client.Del(ctx, "{root}:dlg:parallel_children:p1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := r.Return(ctx, first); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if n, _ := rd.Client.Exists(ctx, "{root}:dlg:parallel_children:p1").Result(); n != 0 {
		t.Fatal("Return recreated the missing parallel_children key")
	}

	// An over-return clamps at zero and keeps the TTL.
	if _, err := r.Reserve(ctx, reservation("root", "p1", 100)); err != nil {
		t.Fatalf("unmarked Reserve on a live tree: %v", err)
	}
	if err := r.Return(ctx, reservation("root", "p1", 900)); err != nil {
		t.Fatalf("over-Return: %v", err)
	}
	if v, _ := rd.Client.Get(ctx, "{root}:dlg:tokens").Int64(); v != 0 {
		t.Fatalf("tokens after over-return = %d, want 0", v)
	}
	if got, _ := rd.Client.TTL(ctx, "{root}:dlg:tokens").Result(); got <= 0 {
		t.Fatalf("tokens TTL after the clamp = %v, want > 0", got)
	}
}
