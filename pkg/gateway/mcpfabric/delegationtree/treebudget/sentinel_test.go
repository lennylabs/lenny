// SPDX-License-Identifier: MIT

package treebudget_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
)

// fullReservation reserves one unit on every axis under parent p of root.
func fullReservation(root, p string, tokens int64) treebudget.Reservation {
	return treebudget.Reservation{
		RootSessionID: root, ParentSessionID: p,
		TreeSizeCap: 10, TreeSizeDelta: 1,
		TreeMemoryCap: 1 << 20, TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
		ParallelChildrenCap: 5, ParallelChildrenDelta: 1,
		ChildrenTotalCap: 5, ChildrenTotalDelta: 1,
		TokenCap: 10_000, TokenDelta: tokens,
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// An unmarked Reserve and a Return on a root whose sentinel is absent return
// ErrBudgetKeysExpired and write no key, even when another tree-wide key of
// the root still exists.
func TestAbsentSentinelWithoutFlagWritesNoKey_spec_8_3(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	if _, err := r.Reserve(ctx, fullReservation("rootA", "rootA", 100)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("unmarked Reserve on an empty root err = %v, want ErrBudgetKeysExpired", err)
	}
	if err := r.Return(ctx, fullReservation("rootA", "rootA", 100)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("Return on an empty root err = %v, want ErrBudgetKeysExpired", err)
	}
	if keys := mr.Keys(); len(keys) != 0 {
		t.Fatalf("scripts wrote %v on an absent sentinel", keys)
	}

	// A surviving tree_size key without the sentinel is still expiry.
	if err := mr.Set("{rootA}:dlg:tree_size", "3"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reserve(ctx, fullReservation("rootA", "rootA", 0)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("unmarked Reserve with tree_size only err = %v, want ErrBudgetKeysExpired", err)
	}
	if err := r.Return(ctx, fullReservation("rootA", "rootA", 0)); !errors.Is(err, treebudget.ErrBudgetKeysExpired) {
		t.Fatalf("Return with tree_size only err = %v, want ErrBudgetKeysExpired", err)
	}
	if v, _ := mr.Get("{rootA}:dlg:tree_size"); v != "3" || len(mr.Keys()) != 1 {
		t.Fatalf("keys after expired runs = %v (tree_size %q), want only tree_size=3", mr.Keys(), v)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), §8.3
// (Defense-in-depth TTL)
// A marked Reserve with a zero token delta creates tree_size, tree_memory, and
// the tokens sentinel with the TTL, so a later unmarked Reserve proceeds.
func TestCreateTreeCreatesSentinelForZeroTokenDelta_spec_8_3(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	res := fullReservation("rootB", "rootB", 0)
	res.CreateTree = true
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("marked Reserve: %v", err)
	}
	for _, k := range []string{"{rootB}:dlg:tree_size", "{rootB}:dlg:tree_memory", "{rootB}:dlg:tokens"} {
		if !mr.Exists(k) {
			t.Fatalf("marked Reserve did not create %s", k)
		}
		if ttl := mr.TTL(k); ttl != treebudget.DefaultTTL {
			t.Fatalf("TTL(%s) = %v, want %v", k, ttl, treebudget.DefaultTTL)
		}
	}
	if v, _ := mr.Get("{rootB}:dlg:tokens"); v != "0" {
		t.Fatalf("tokens = %q, want 0 for a zero token delta", v)
	}
	got, err := r.Reserve(ctx, fullReservation("rootB", "rootB", 0))
	if err != nil {
		t.Fatalf("unmarked Reserve after the first: %v", err)
	}
	if got.TreeSize != 2 {
		t.Fatalf("TreeSize = %d, want 2", got.TreeSize)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// A marked Reserve on a tree whose sentinel exists does not reset the
// counters: the create is SET NX.
func TestCreateTreeDoesNotResetALiveTree_spec_8_3(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	ctx := context.Background()
	res := fullReservation("rootC", "rootC", 100)
	res.CreateTree = true
	for i := 0; i < 2; i++ {
		if _, err := r.Reserve(ctx, res); err != nil {
			t.Fatalf("marked Reserve %d: %v", i, err)
		}
	}
	got, err := r.Snapshot(ctx, "rootC")
	if err != nil {
		t.Fatal(err)
	}
	if got.TreeSize != 2 || got.Tokens != 200 {
		t.Fatalf("snapshot = %+v, want TreeSize 2 Tokens 200", got)
	}
}

// spec: §8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup)
// A Return skips a per-parent key that does not exist rather than
// recreating it, and still decrements the keys that exist.
func TestReturnSkipsMissingPerParentKey_spec_8_3(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	res := fullReservation("rootD", "pD", 100)
	res.CreateTree = true
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatal(err)
	}
	mr.Del("{rootD}:dlg:parallel_children:pD")
	if err := r.Return(ctx, res); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if mr.Exists("{rootD}:dlg:parallel_children:pD") {
		t.Fatal("Return recreated the missing parallel_children key")
	}
	for k, want := range map[string]string{
		"{rootD}:dlg:tree_size": "0", "{rootD}:dlg:children_total:pD": "0", "{rootD}:dlg:tokens": "0",
	} {
		if v, _ := mr.Get(k); v != want {
			t.Fatalf("%s = %q, want %q", k, v, want)
		}
	}
}

// spec: §8.3 (Defense-in-depth TTL)
// A Return that clamps a counter at zero keeps the key's TTL; a clamp that
// dropped it would leave a key that never expires.
func TestClampingReturnKeepsTTL_spec_8_3(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	res := fullReservation("rootE", "rootE", 100)
	res.CreateTree = true
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatal(err)
	}
	mr.SetTTL("{rootE}:dlg:tokens", time.Hour)
	over := res
	over.TokenDelta = 500
	if err := r.Return(ctx, over); err != nil {
		t.Fatalf("over-Return: %v", err)
	}
	if v, _ := mr.Get("{rootE}:dlg:tokens"); v != "0" {
		t.Fatalf("tokens after over-return = %q, want clamped 0", v)
	}
	if ttl := mr.TTL("{rootE}:dlg:tokens"); ttl <= 0 {
		t.Fatalf("TTL after clamp = %v, want > 0", ttl)
	}
	if ttl := mr.TTL("{rootE}:dlg:tree_size"); ttl <= 0 {
		t.Fatalf("tree_size TTL after a non-clamping return = %v, want > 0", ttl)
	}
}
