// SPDX-License-Identifier: MIT

package platformtools_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// recordingCounters records the calls the tree budget operations make.
type recordingCounters struct {
	returned []treebudget.Reservation
	rearmed  []string
	parents  []string
	hwm      int64
	err      error
}

func (r *recordingCounters) Return(_ context.Context, res treebudget.Reservation) error {
	r.returned = append(r.returned, res)
	return r.err
}

func (r *recordingCounters) RearmTree(_ context.Context, root string, parents []string) error {
	r.rearmed = append(r.rearmed, root)
	r.parents = parents
	return r.err
}

func (r *recordingCounters) ObserveHighWatermark(_ context.Context, _ string) (int64, bool, error) {
	return r.hwm, r.hwm > 0, r.err
}

// TestTreeBudgetsHandTheCountersEachDeltaAndCap checks that a budget
// return reaches the counters with every cap and delta it carries, and that
// the re-arm and the high-watermark read reach the counters unchanged.
//
// spec: GWSEAM-ReturnBudget, GWSEAM-RearmBudget, GWSEAM-ObserveHighWatermark
func TestTreeBudgetsHandTheCountersEachDeltaAndCap(t *testing.T) {
	c := &recordingCounters{hwm: 4}
	b := platformtools.TreeBudgetsOver(c)
	ret := seam.BudgetReturn{
		RootSessionID: "root", ParentSessionID: "parent",
		TreeSizeCap: 1, TreeSizeDelta: 2, TreeMemoryCap: 3, TreeMemoryDelta: 4,
		ParallelChildrenCap: 5, ParallelChildrenDelta: 6, ChildrenTotalCap: 7, ChildrenTotalDelta: 8,
		TokenCap: 9, TokenDelta: 10,
	}
	if err := b.ReturnBudget(context.Background(), ret); err != nil {
		t.Fatal(err)
	}
	want := treebudget.Reservation{
		RootSessionID: "root", ParentSessionID: "parent",
		TreeSizeCap: 1, TreeSizeDelta: 2, TreeMemoryCap: 3, TreeMemoryDelta: 4,
		ParallelChildrenCap: 5, ParallelChildrenDelta: 6, ChildrenTotalCap: 7, ChildrenTotalDelta: 8,
		TokenCap: 9, TokenDelta: 10,
	}
	if len(c.returned) != 1 || c.returned[0] != want {
		t.Fatalf("returned = %+v, want %+v", c.returned, want)
	}
	if err := b.RearmBudget(context.Background(), "root", []string{"root", "c1"}); err != nil || len(c.rearmed) != 1 || len(c.parents) != 2 {
		t.Fatalf("rearm: err=%v rearmed=%v parents=%v", err, c.rearmed, c.parents)
	}
	if v, found, err := b.ObserveHighWatermark(context.Background(), "root"); err != nil || !found || v != 4 {
		t.Fatalf("high-watermark = %d %t %v, want 4 true nil", v, found, err)
	}
	c.err = errors.New("redis down")
	if err := b.ReturnBudget(context.Background(), ret); !errors.Is(err, c.err) {
		t.Errorf("a counter failure = %v, want it returned", err)
	}
}

// TestNilBackendsHandTheCoreNoOperations checks that each constructor
// returns an untyped nil for a nil backend, so the session core's nil guard
// skips the part.
//
// spec: GWSEAM-ReturnBudget, GWSEAM-ArchiveNode, GWSEAM-NodeUsage, GWSEAM-PendingInputWaits, GWSEAM-RegisterApproval
func TestNilBackendsHandTheCoreNoOperations(t *testing.T) {
	if platformtools.NewTreeBudgets(nil) != nil {
		t.Error("NewTreeBudgets(nil) is not nil")
	}
	if platformtools.NewTreeArchive(nil) != nil {
		t.Error("NewTreeArchive(nil) is not nil")
	}
	if platformtools.NewTreeRollups(nil) != nil {
		t.Error("NewTreeRollups(nil) is not nil")
	}
	if platformtools.NewInputWaits(nil) != nil {
		t.Error("NewInputWaits(nil) is not nil")
	}
	if platformtools.NewToolApprovals(nil) != nil {
		t.Error("NewToolApprovals(nil) is not nil")
	}
}

// TestTreeArchiveRecordsReplaysAndReportsAMissingNode checks that an
// archived node is replayed in settle order with every field, read back by
// node, and that a node the archive does not hold is ErrSessionNotFound.
//
// spec: GWSEAM-ArchiveNode, GWSEAM-ArchivedNodes, GWSEAM-ArchivedNodeFor
func TestTreeArchiveRecordsReplaysAndReportsAMissingNode(t *testing.T) {
	a := platformtools.NewTreeArchive(treearchive.NewMemory())
	ctx := context.Background()
	settled := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"c2", "c1"} {
		if err := a.ArchiveNode(ctx, seam.ArchivedNode{
			TenantID: "acme", RootSessionID: "root", NodeSessionID: id, ParentSessionID: "root",
			State: "completed", Result: `{"taskId":"` + id + `"}`, SettledAt: settled.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := a.ArchivedNodes(ctx, "acme", "root")
	if err != nil || len(nodes) != 2 || nodes[0].NodeSessionID != "c2" || nodes[1].NodeSessionID != "c1" {
		t.Fatalf("replay = %+v, %v; want c2 then c1", nodes, err)
	}
	if nodes[0].ParentSessionID != "root" || nodes[0].State != "completed" || nodes[0].Result == "" {
		t.Errorf("replayed node lost fields: %+v", nodes[0])
	}
	got, err := a.ArchivedNodeFor(ctx, "acme", "c1")
	if err != nil || got.NodeSessionID != "c1" {
		t.Fatalf("by node = %+v, %v", got, err)
	}
	if _, err := a.ArchivedNodeFor(ctx, "acme", "absent"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("missing node = %v, want ErrSessionNotFound", err)
	}
	if _, err := a.ArchivedNodeFor(ctx, "globex", "c1"); !errors.Is(err, seam.ErrSessionNotFound) {
		t.Errorf("another tenant's node = %v, want ErrSessionNotFound", err)
	}
}

// TestTreeRollupsReadTheRecordsTimesAndPod checks that a node's usage is
// derived from the session record's lifetime and pod, as the rollup builder
// derives it from the stored row.
//
// spec: GWSEAM-NodeUsage, GWSEAM-TreeUsage, GWSEAM-SubtreeTokens
func TestTreeRollupsReadTheRecordsTimesAndPod(t *testing.T) {
	store := memstore.New()
	tokens := sessionusage.NewMemory()
	archive := treearchive.NewMemory()
	r := platformtools.NewTreeRollups(resultrollup.New(store, tokens, archive, nil))
	created := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	node := seam.Session{
		ID: "leaf", TenantID: "acme", RootSessionID: "leaf", State: state.State("completed"),
		PodAssignment: "pod-1", CreatedAt: created, UpdatedAt: created.Add(120 * time.Second),
	}
	u := r.NodeUsage(context.Background(), node)
	if u == nil || u.WallClockSeconds != 120 || u.PodMinutes != 2 {
		t.Fatalf("usage = %+v, want 120 s wall clock and 2 pod minutes", u)
	}
	node.PodAssignment = ""
	if u := r.NodeUsage(context.Background(), node); u.PodMinutes != 0 {
		t.Errorf("a podless node's pod minutes = %v, want 0", u.PodMinutes)
	}
	if tu := r.TreeUsage(context.Background(), node, u); tu == nil || tu.TotalTasks != 1 {
		t.Errorf("tree usage = %+v, want one task", tu)
	}
	if _, err := r.SubtreeTokens(context.Background(), node); err != nil {
		t.Errorf("subtree tokens: %v", err)
	}
}

// TestTreeOrphansClassifyBySessionRecords checks the orphan rule over the
// seam's session records: an absent parent, a terminal ancestor, a broken
// chain, and a live chain.
//
// spec: GWSEAM-Orphaned
func TestTreeOrphansClassifyBySessionRecords(t *testing.T) {
	ended := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	tree := map[string]seam.Session{
		"root":    {ID: "root", State: "running"},
		"dead":    {ID: "dead", State: "completed", TerminatedAt: ended, ParentSessionID: "root", DelegationDepth: 1},
		"live":    {ID: "live", State: "running", ParentSessionID: "root", DelegationDepth: 1},
		"under":   {ID: "under", State: "running", ParentSessionID: "dead", DelegationDepth: 2},
		"healthy": {ID: "healthy", State: "running", ParentSessionID: "live", DelegationDepth: 2},
		"lost":    {ID: "lost", State: "running", ParentSessionID: "gone", DelegationDepth: 1},
		"broken":  {ID: "broken", State: "running", ParentSessionID: "lost", DelegationDepth: 2},
	}
	var o platformtools.TreeOrphans
	for id, want := range map[string]bool{
		"root": false, "dead": false, "live": false, "healthy": false,
		"under": true, "lost": true, "broken": true,
	} {
		if got := o.Orphaned(tree[id], tree); got != want {
			t.Errorf("Orphaned(%s) = %t, want %t", id, got, want)
		}
	}
}
