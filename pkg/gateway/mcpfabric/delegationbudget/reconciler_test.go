// SPDX-License-Identifier: MIT

package delegationbudget

import (
	"context"
	"testing"
	"time"
)

type fakeCounters struct {
	snap     map[string]TreeCounters
	restored map[string]TreeCounters
	snapErr  error
}

func (f *fakeCounters) Snapshot(_ context.Context, root string) (TreeCounters, error) {
	if f.snapErr != nil {
		return TreeCounters{}, f.snapErr
	}
	return f.snap[root], nil
}

func (f *fakeCounters) Restore(_ context.Context, root string, c TreeCounters) error {
	if f.restored == nil {
		f.restored = map[string]TreeCounters{}
	}
	f.restored[root] = c
	return nil
}

type fakeTreeLister struct {
	refs []TreeRef
	err  error
}

func (f fakeTreeLister) ListActiveTrees(context.Context) ([]TreeRef, error) { return f.refs, f.err }

type fakeStore struct {
	written []Checkpoint
	active  []Checkpoint
}

func (f *fakeStore) Write(_ context.Context, rows []Checkpoint) error {
	f.written = append(f.written, rows...)
	return nil
}
func (f *fakeStore) ListActive(context.Context) ([]Checkpoint, error)    { return f.active, nil }
func (f *fakeStore) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }

type fakeLive struct {
	m   map[string]LiveTree
	err error
}

func (f fakeLive) LiveTree(_ context.Context, _, root string) (LiveTree, error) {
	if f.err != nil {
		return LiveTree{}, f.err
	}
	return f.m[root], nil
}

type fakeMarker struct{ marked []string }

func (f *fakeMarker) MarkBudgetUnrecoverable(_ context.Context, _, root, _ string) error {
	f.marked = append(f.marked, root)
	return nil
}

type fakeMetrics struct{ outcomes map[string]int }

func (f *fakeMetrics) IncDelegationBudgetReconstruction(o string) {
	if f.outcomes == nil {
		f.outcomes = map[string]int{}
	}
	f.outcomes[o]++
}

// spec: §11.2 line 44 — the checkpoint persists only trees with real
// budget state; a tree whose Redis counters are all zero (no delegation
// admitted) is skipped so the table does not accumulate empty rows.
func TestCheckpointPersistsNonZeroTreesOnly_spec_11_2_44(t *testing.T) {
	t.Parallel()
	counters := &fakeCounters{snap: map[string]TreeCounters{
		"rootA": {TreeSize: 2, Tokens: 1500, TreeMemory: 24576},
		"rootB": {}, // standalone session, no delegation
	}}
	store := &fakeStore{}
	r := &Reconciler{
		Counters: counters,
		Trees:    fakeTreeLister{refs: []TreeRef{{TenantID: "acme", RootSessionID: "rootA"}, {TenantID: "acme", RootSessionID: "rootB"}}},
		Store:    store,
	}
	r.Checkpoint(context.Background())
	if len(store.written) != 1 {
		t.Fatalf("wrote %d rows, want 1 (zero-counter tree skipped): %+v", len(store.written), store.written)
	}
	got := store.written[0]
	if got.RootSessionID != "rootA" || got.TreeSize != 2 || got.TokenBudgetConsumed != 1500 || got.TreeMemoryBytes != 24576 {
		t.Fatalf("checkpoint row = %+v, want rootA {2,1500,24576}", got)
	}
}

// spec: §11.2 line 48 — reconstruction restores each axis to
// max(postgres_checkpoint, live). liveMemory is the alive node count
// times the per-node footprint.
func TestReconcileRestoresMaxRule_spec_11_2_48(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	counters := &fakeCounters{}
	store := &fakeStore{active: []Checkpoint{{
		TenantID: "acme", RootSessionID: "rootM",
		TreeSize: 3, TokenBudgetConsumed: 1000, TreeMemoryBytes: 36864,
		CheckpointAt: now.Add(-1 * time.Second),
	}}}
	live := fakeLive{m: map[string]LiveTree{"rootM": {RootExists: true, NodeCount: 5, TokenAllocations: 800}}}
	metrics := &fakeMetrics{}
	r := &Reconciler{
		Counters: counters, Store: store, Live: live, Metrics: metrics,
		Interval: 30 * time.Second, NodeMemoryBytes: 12288,
		Now: func() time.Time { return now },
	}
	r.Reconcile(context.Background())
	got := counters.restored["rootM"]
	// treeSize max(3,5)=5; tokens max(1000,800)=1000; memory max(36864, 5*12288=61440)=61440.
	if got.TreeSize != 5 || got.Tokens != 1000 || got.TreeMemory != 61440 {
		t.Fatalf("restored = %+v, want {TreeSize:5 Tokens:1000 TreeMemory:61440}", got)
	}
	if metrics.outcomes[OutcomeSuccess] != 1 {
		t.Fatalf("success outcome = %d, want 1", metrics.outcomes[OutcomeSuccess])
	}
}

// spec: §11.2 line 48 — a tree whose checkpoint is older than
// 2 x interval AND whose live state cannot be enumerated is
// irrecoverable: the root is moved to awaiting_client_action and no
// counters are restored.
func TestReconcileIrrecoverableMarksRoot_spec_11_2_48(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	counters := &fakeCounters{}
	store := &fakeStore{active: []Checkpoint{{
		TenantID: "acme", RootSessionID: "rootU",
		TreeSize: 4, TokenBudgetConsumed: 2000, TreeMemoryBytes: 49152,
		CheckpointAt: now.Add(-5 * time.Minute), // > 2 x 30s
	}}}
	live := fakeLive{m: map[string]LiveTree{"rootU": {RootExists: false}}} // unenumerable
	marker := &fakeMarker{}
	metrics := &fakeMetrics{}
	r := &Reconciler{
		Counters: counters, Store: store, Live: live, Marker: marker, Metrics: metrics,
		Interval: 30 * time.Second, Now: func() time.Time { return now },
	}
	r.Reconcile(context.Background())
	if len(marker.marked) != 1 || marker.marked[0] != "rootU" {
		t.Fatalf("marked = %v, want [rootU]", marker.marked)
	}
	if _, restored := counters.restored["rootU"]; restored {
		t.Fatalf("irrecoverable tree must not restore counters, got %+v", counters.restored["rootU"])
	}
	if metrics.outcomes[OutcomeIrrecoverable] != 1 {
		t.Fatalf("irrecoverable outcome = %d, want 1", metrics.outcomes[OutcomeIrrecoverable])
	}
}

// spec: §11.2 line 48 — when live enumeration is not possible but the
// checkpoint is still fresh (within 2 x interval), the tree is
// recoverable from the checkpoint alone: the live estimate is zero, so
// the checkpoint values are restored unchanged and the root is not
// paused.
func TestReconcileFreshUnenumerableRestoresFromCheckpoint_spec_11_2_48(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	counters := &fakeCounters{}
	store := &fakeStore{active: []Checkpoint{{
		TenantID: "acme", RootSessionID: "rootF",
		TreeSize: 6, TokenBudgetConsumed: 3000, TreeMemoryBytes: 73728,
		CheckpointAt: now.Add(-2 * time.Second), // fresh
	}}}
	live := fakeLive{m: map[string]LiveTree{"rootF": {RootExists: false}}}
	marker := &fakeMarker{}
	metrics := &fakeMetrics{}
	r := &Reconciler{
		Counters: counters, Store: store, Live: live, Marker: marker, Metrics: metrics,
		Interval: 30 * time.Second, NodeMemoryBytes: 12288, Now: func() time.Time { return now },
	}
	r.Reconcile(context.Background())
	if len(marker.marked) != 0 {
		t.Fatalf("fresh checkpoint must not be marked irrecoverable, marked=%v", marker.marked)
	}
	got := counters.restored["rootF"]
	if got.TreeSize != 6 || got.Tokens != 3000 || got.TreeMemory != 73728 {
		t.Fatalf("restored = %+v, want checkpoint values {6,3000,73728}", got)
	}
	if metrics.outcomes[OutcomeSuccess] != 1 {
		t.Fatalf("success outcome = %d, want 1", metrics.outcomes[OutcomeSuccess])
	}
}

// spec: §11.2 lines 44, 48 — the probe loop reconstructs on the
// Redis-down-to-up edge and then checkpoints; a steady-reachable tick
// only checkpoints (no reconstruction).
func TestTickRecoveryEdgeReconcilesThenCheckpoints_spec_12_4_218(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	counters := &fakeCounters{snap: map[string]TreeCounters{"rootE": {TreeSize: 1, Tokens: 100}}}
	store := &fakeStore{active: []Checkpoint{{
		TenantID: "acme", RootSessionID: "rootE", TreeSize: 1, TokenBudgetConsumed: 100,
		CheckpointAt: now.Add(-1 * time.Second),
	}}}
	live := fakeLive{m: map[string]LiveTree{"rootE": {RootExists: true, NodeCount: 1}}}
	metrics := &fakeMetrics{}
	r := &Reconciler{
		Probe:    func(context.Context) bool { return true },
		Counters: counters, Store: store, Live: live, Metrics: metrics,
		Trees:    fakeTreeLister{refs: []TreeRef{{TenantID: "acme", RootSessionID: "rootE"}}},
		Interval: 30 * time.Second, Now: func() time.Time { return now },
	}
	ctx := context.Background()

	// Steady-reachable tick (was reachable): checkpoint only, no reconstruction.
	r.tick(ctx, true)
	if metrics.outcomes[OutcomeSuccess] != 0 {
		t.Fatalf("steady tick must not reconstruct, success=%d", metrics.outcomes[OutcomeSuccess])
	}
	if len(store.written) == 0 {
		t.Fatalf("steady reachable tick must checkpoint")
	}

	// Recovery edge (was unreachable, now reachable): reconstruct then checkpoint.
	r.tick(ctx, false)
	if metrics.outcomes[OutcomeSuccess] != 1 {
		t.Fatalf("recovery edge must reconstruct exactly once, success=%d", metrics.outcomes[OutcomeSuccess])
	}
	if _, ok := counters.restored["rootE"]; !ok {
		t.Fatalf("recovery edge must restore the tree's counters")
	}
}

// A reconciler missing a required seam is a no-op: Run returns
// immediately rather than panicking.
func TestRunNoOpWithoutRequiredSeams(t *testing.T) {
	t.Parallel()
	r := &Reconciler{} // no Probe/Counters/Trees/Store
	done := make(chan struct{})
	go func() { r.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return on a misconfigured reconciler")
	}
}
