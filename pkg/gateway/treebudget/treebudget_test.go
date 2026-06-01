// SPDX-License-Identifier: MIT

package treebudget_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
)

func newReserver(t *testing.T) (*treebudget.Reserver, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return treebudget.New(cl, 0), mr
}

// spec: §8.2 line 57 — a delegation within every cap is admitted and
// the tree-wide counters advance by the reserved deltas.
func TestReserveAdmitsWithinCaps_spec_8_2_57(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	got, err := r.Reserve(context.Background(), treebudget.Reservation{
		RootSessionID:         "root1",
		ParentSessionID:       "root1",
		TreeSizeCap:           50,
		TreeSizeDelta:         1,
		TreeMemoryCap:         2097152,
		TreeMemoryDelta:       treebudget.PerNodeMemoryBytes,
		ParallelChildrenCap:   5,
		ParallelChildrenDelta: 1,
		ChildrenTotalCap:      10,
		ChildrenTotalDelta:    1,
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got.TreeSize != 1 || got.ParallelChildren != 1 || got.ChildrenTotal != 1 {
		t.Fatalf("Totals = %+v, want tree_size/parallel/children = 1", got)
	}
	if got.TreeMemory != treebudget.PerNodeMemoryBytes {
		t.Fatalf("TreeMemory = %d, want %d", got.TreeMemory, treebudget.PerNodeMemoryBytes)
	}
}

// spec: §8.2 line 57 — a zero cap means no limit on that axis; the
// reservation is admitted regardless of accumulated value.
func TestReserveZeroCapUnbounded_spec_8_2_57(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	for i := 0; i < 100; i++ {
		if _, err := r.Reserve(context.Background(), treebudget.Reservation{
			RootSessionID: "rootZ", ParentSessionID: "rootZ",
			TreeSizeCap: 0, TreeSizeDelta: 1,
		}); err != nil {
			t.Fatalf("Reserve %d under zero cap: %v", i, err)
		}
	}
}

// spec: §8.2 line 127 — a reservation that would push tree_size over
// maxTreeSize is rejected with BUDGET_EXHAUSTED and no counter mutates.
func TestReserveTreeSizeExhausted_spec_8_2_127(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	res := treebudget.Reservation{
		RootSessionID: "root2", ParentSessionID: "root2",
		TreeSizeCap: 2, TreeSizeDelta: 1,
	}
	for i := 0; i < 2; i++ {
		if _, err := r.Reserve(ctx, res); err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
	}
	_, err := r.Reserve(ctx, res)
	var bx *treebudget.BudgetExhaustedError
	if !errors.As(err, &bx) {
		t.Fatalf("third Reserve err = %v, want BudgetExhaustedError", err)
	}
	if bx.Axis != "maxTreeSize" || bx.Current != 2 || bx.Cap != 2 {
		t.Fatalf("BudgetExhaustedError = %+v, want maxTreeSize 2/2", bx)
	}
	// The rejected reservation must not have advanced the counter.
	if v, _ := mr.Get("{root2}:dlg:tree_size"); v != "2" {
		t.Fatalf("tree_size after rejection = %q, want 2", v)
	}
}

// spec: §8.2 line 127 — every capped axis rejects independently. Memory
// is the binding axis here even though tree_size has headroom.
func TestReserveMemoryExhausted_spec_8_2_127(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	ctx := context.Background()
	res := treebudget.Reservation{
		RootSessionID: "root3", ParentSessionID: "root3",
		TreeSizeCap: 100, TreeSizeDelta: 1,
		TreeMemoryCap: treebudget.PerNodeMemoryBytes, TreeMemoryDelta: treebudget.PerNodeMemoryBytes,
	}
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	_, err := r.Reserve(ctx, res)
	var bx *treebudget.BudgetExhaustedError
	if !errors.As(err, &bx) || bx.Axis != "maxTreeMemoryBytes" {
		t.Fatalf("second Reserve err = %v, want maxTreeMemoryBytes exhaustion", err)
	}
}

// spec: §12.4 line 193 — parallel_children is per-parent. Two distinct
// parents in the same tree each get their own ceiling.
func TestReserveParallelChildrenPerParent_spec_12_4_193(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	ctx := context.Background()
	mk := func(parent string) treebudget.Reservation {
		return treebudget.Reservation{
			RootSessionID: "root4", ParentSessionID: parent,
			ParallelChildrenCap: 1, ParallelChildrenDelta: 1,
		}
	}
	if _, err := r.Reserve(ctx, mk("pA")); err != nil {
		t.Fatalf("pA reserve: %v", err)
	}
	// pB shares the tree but has its own per-parent counter, so it is
	// admitted even though pA is at its cap.
	if _, err := r.Reserve(ctx, mk("pB")); err != nil {
		t.Fatalf("pB reserve: %v", err)
	}
	// pA over its own ceiling is rejected.
	_, err := r.Reserve(ctx, mk("pA"))
	var bx *treebudget.BudgetExhaustedError
	if !errors.As(err, &bx) || bx.Axis != "maxParallelChildren" {
		t.Fatalf("pA second reserve err = %v, want maxParallelChildren exhaustion", err)
	}
}

// spec: §8.2 line 130 — Return decrements the reserved axes; a released
// parallel-children slot frees capacity for a new child.
func TestReturnFreesParallelSlot_spec_8_2_130(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	ctx := context.Background()
	res := treebudget.Reservation{
		RootSessionID: "root5", ParentSessionID: "root5",
		ParallelChildrenCap: 1, ParallelChildrenDelta: 1,
	}
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := r.Reserve(ctx, res); !errors.As(err, new(*treebudget.BudgetExhaustedError)) {
		t.Fatalf("second reserve err = %v, want exhaustion", err)
	}
	if err := r.Return(ctx, res); err != nil {
		t.Fatalf("return: %v", err)
	}
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("reserve after return: %v", err)
	}
}

// spec: §8.2 line 130 — Return clamps at zero so a double-return cannot
// drive a counter negative and inflate available budget.
func TestReturnClampsAtZero_spec_8_2_130(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	ctx := context.Background()
	res := treebudget.Reservation{
		RootSessionID: "root6", ParentSessionID: "root6",
		TreeSizeCap: 5, TreeSizeDelta: 1,
	}
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.Return(ctx, res); err != nil {
		t.Fatalf("return 1: %v", err)
	}
	if err := r.Return(ctx, res); err != nil {
		t.Fatalf("return 2: %v", err)
	}
	if v, _ := mr.Get("{root6}:dlg:tree_size"); v != "0" {
		t.Fatalf("tree_size after double return = %q, want clamped 0", v)
	}
}

// spec: §12.4 line 213 — a Redis outage fails closed; Reserve returns
// ErrBudgetUnavailable rather than admitting an unbudgeted delegation.
func TestReserveFailsClosedOnRedisOutage_spec_12_4_213(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	mr.Close()
	_, err := r.Reserve(context.Background(), treebudget.Reservation{
		RootSessionID: "root7", ParentSessionID: "root7",
		TreeSizeCap: 10, TreeSizeDelta: 1,
	})
	if !errors.Is(err, treebudget.ErrBudgetUnavailable) {
		t.Fatalf("Reserve after outage err = %v, want ErrBudgetUnavailable", err)
	}
}

// spec: §8.2 line 57 — an empty root session id is rejected before any
// Redis interaction.
func TestReserveEmptyRootRejected_spec_8_2_57(t *testing.T) {
	t.Parallel()
	r, _ := newReserver(t)
	if _, err := r.Reserve(context.Background(), treebudget.Reservation{TreeSizeDelta: 1}); err == nil {
		t.Fatal("Reserve with empty root = nil err, want error")
	}
}

// spec: §12.4 line 193 — all five keys share the {root} hash tag so a
// Redis Cluster co-locates them on one slot for atomic multi-key Lua.
func TestReserveKeysShareHashTag_spec_12_4_193(t *testing.T) {
	t.Parallel()
	r, mr := newReserver(t)
	if _, err := r.Reserve(context.Background(), treebudget.Reservation{
		RootSessionID: "rootH", ParentSessionID: "pX",
		TreeSizeCap: 10, TreeSizeDelta: 1,
		TreeMemoryCap: 1 << 20, TreeMemoryDelta: 1,
		ParallelChildrenCap: 5, ParallelChildrenDelta: 1,
		ChildrenTotalCap: 5, ChildrenTotalDelta: 1,
		TokenCap: 1000, TokenDelta: 10,
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	for _, k := range []string{
		"{rootH}:dlg:tree_size", "{rootH}:dlg:tree_memory",
		"{rootH}:dlg:parallel_children:pX", "{rootH}:dlg:children_total:pX",
		"{rootH}:dlg:tokens",
	} {
		if !mr.Exists(k) {
			t.Errorf("expected key %q to exist", k)
		}
	}
}
