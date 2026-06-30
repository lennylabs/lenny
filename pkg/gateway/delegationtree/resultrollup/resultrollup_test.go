// SPDX-License-Identifier: MIT

package resultrollup_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionusage"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

func mustCreate(t *testing.T, store *memstore.Store, s sessionstore.Session) {
	t.Helper()
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("create %s: %v", s.ID, err)
	}
}

// fixedNow returns a stable clock so wall-clock arithmetic is deterministic.
func fixedNow(ts time.Time) func() time.Time { return func() time.Time { return ts } }

// TestUsageDerivesTimeDimensions_spec_8_8_897 confirms a terminal
// session's usage carries the accumulated tokens plus the wallClock /
// podMinutes / credentialLeaseMinutes derived from the row.
func TestUsageDerivesTimeDimensions_spec_8_8_897(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	archive := treearchive.NewMemory()

	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	// 120s wall-clock → 2.0 pod-minutes and 2.0 lease-minutes.
	terminal := created.Add(120 * time.Second)
	sess := sessionstore.Session{
		ID: "child1", TenantID: "acme", RootSessionID: "child1",
		State: session.StateCompleted, PodAssignment: "pod-xyz",
		CreatedAt: created, UpdatedAt: terminal,
	}
	mustCreate(t, sessions, sess)
	_ = tokens.Add(ctx, "acme", "child1", 15000, 8000)

	b := resultrollup.New(sessions, tokens, archive, fixedNow(terminal))
	u := b.Usage(ctx, sess)
	if u == nil {
		t.Fatal("Usage returned nil")
	}
	if u.InputTokens != 15000 || u.OutputTokens != 8000 {
		t.Fatalf("tokens = %d/%d, want 15000/8000", u.InputTokens, u.OutputTokens)
	}
	if u.WallClockSeconds != 120 {
		t.Fatalf("wallClock = %v, want 120", u.WallClockSeconds)
	}
	if u.PodMinutes != 2.0 || u.CredentialLeaseMinutes != 2.0 {
		t.Fatalf("podMin/leaseMin = %v/%v, want 2.0/2.0", u.PodMinutes, u.CredentialLeaseMinutes)
	}
}

// TestUsageNoPodLeavesPodAndLeaseZero confirms a session that never bound
// a pod (e.g. a create-time failure) reports zero pod / lease minutes
// rather than fabricating them from wall-clock.
func TestUsageNoPodLeavesPodAndLeaseZero(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	sess := sessionstore.Session{
		ID: "c", TenantID: "acme", RootSessionID: "c",
		State: session.StateFailed, PodAssignment: "",
		CreatedAt: created, UpdatedAt: created.Add(60 * time.Second),
	}
	mustCreate(t, sessions, sess)
	b := resultrollup.New(sessions, tokens, treearchive.NewMemory(), fixedNow(created))
	u := b.Usage(ctx, sess)
	if u.WallClockSeconds != 60 {
		t.Fatalf("wallClock = %v, want 60", u.WallClockSeconds)
	}
	if u.PodMinutes != 0 || u.CredentialLeaseMinutes != 0 {
		t.Fatalf("podMin/leaseMin = %v/%v, want 0/0", u.PodMinutes, u.CredentialLeaseMinutes)
	}
}

// TestTreeUsageLeaf_spec_8_8_904 confirms a leaf task's treeUsage equals
// its own usage with totalTasks=1 (all-descendants-settled is trivially
// satisfied for a leaf).
func TestTreeUsageLeaf_spec_8_8_904(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	sess := sessionstore.Session{
		ID: "leaf", TenantID: "acme", RootSessionID: "leaf",
		State: session.StateCompleted, PodAssignment: "pod",
		CreatedAt: created, UpdatedAt: created.Add(60 * time.Second),
	}
	mustCreate(t, sessions, sess)
	_ = tokens.Add(ctx, "acme", "leaf", 100, 50)
	b := resultrollup.New(sessions, tokens, treearchive.NewMemory(), fixedNow(created))
	u := b.Usage(ctx, sess)
	tu := b.TreeUsage(ctx, sess, u)
	if tu == nil {
		t.Fatal("treeUsage nil for a settled leaf")
	}
	if tu.TotalTasks != 1 {
		t.Fatalf("totalTasks = %d, want 1", tu.TotalTasks)
	}
	if tu.InputTokens != 100 || tu.OutputTokens != 50 {
		t.Fatalf("tokens = %d/%d, want 100/50", tu.InputTokens, tu.OutputTokens)
	}
}

// TestTreeUsageSumsSettledSubtree_spec_8_8_904 confirms treeUsage sums a
// parent's own usage plus each live-terminal descendant's, counting the
// whole subtree in totalTasks.
func TestTreeUsageSumsSettledSubtree_spec_8_8_904(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)

	root := sessionstore.Session{
		ID: "root", TenantID: "acme", RootSessionID: "root",
		State: session.StateCompleted, PodAssignment: "p0",
		CreatedAt: created, UpdatedAt: created.Add(60 * time.Second),
	}
	childA := sessionstore.Session{
		ID: "a", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root",
		State: session.StateCompleted, PodAssignment: "p1",
		CreatedAt: created, UpdatedAt: created.Add(120 * time.Second),
	}
	childB := sessionstore.Session{
		ID: "b", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root",
		State: session.StateCompleted, PodAssignment: "p2",
		CreatedAt: created, UpdatedAt: created.Add(30 * time.Second),
	}
	mustCreate(t, sessions, root)
	mustCreate(t, sessions, childA)
	mustCreate(t, sessions, childB)
	_ = tokens.Add(ctx, "acme", "root", 1000, 100)
	_ = tokens.Add(ctx, "acme", "a", 2000, 200)
	_ = tokens.Add(ctx, "acme", "b", 3000, 300)

	b := resultrollup.New(sessions, tokens, treearchive.NewMemory(), fixedNow(created))
	u := b.Usage(ctx, root)
	tu := b.TreeUsage(ctx, root, u)
	if tu == nil {
		t.Fatal("treeUsage nil for a fully-settled subtree")
	}
	if tu.TotalTasks != 3 {
		t.Fatalf("totalTasks = %d, want 3", tu.TotalTasks)
	}
	if tu.InputTokens != 6000 || tu.OutputTokens != 600 {
		t.Fatalf("tokens = %d/%d, want 6000/600", tu.InputTokens, tu.OutputTokens)
	}
	// wallClock: root 60 + a 120 + b 30 = 210; pod/lease minutes 3.5 each.
	if tu.WallClockSeconds != 210 {
		t.Fatalf("wallClock = %v, want 210", tu.WallClockSeconds)
	}
	if tu.PodMinutes != 3.5 || tu.CredentialLeaseMinutes != 3.5 {
		t.Fatalf("podMin/leaseMin = %v/%v, want 3.5/3.5", tu.PodMinutes, tu.CredentialLeaseMinutes)
	}
}

// TestTreeUsageNullWhenDescendantUnsettled_spec_8_8_917 confirms a parent
// whose child is still running has a null treeUsage even though the parent
// itself is terminal.
func TestTreeUsageNullWhenDescendantUnsettled_spec_8_8_917(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	root := sessionstore.Session{
		ID: "root", TenantID: "acme", RootSessionID: "root",
		State: session.StateCompleted, PodAssignment: "p0",
		CreatedAt: created, UpdatedAt: created.Add(60 * time.Second),
	}
	running := sessionstore.Session{
		ID: "kid", TenantID: "acme", RootSessionID: "root", ParentSessionID: "root",
		State: session.StateRunning, PodAssignment: "p1",
		CreatedAt: created, UpdatedAt: created.Add(10 * time.Second),
	}
	mustCreate(t, sessions, root)
	mustCreate(t, sessions, running)
	b := resultrollup.New(sessions, tokens, treearchive.NewMemory(), fixedNow(created.Add(90*time.Second)))
	u := b.Usage(ctx, root)
	if tu := b.TreeUsage(ctx, root, u); tu != nil {
		t.Fatalf("treeUsage = %+v, want nil (descendant unsettled)", tu)
	}
}

// TestTreeUsageReadsArchivedDescendant_spec_8_8_904 confirms a reclaimed
// descendant (gone from live rows, present only in the §8.10 archive)
// contributes its baked-in usage to the parent's treeUsage rollup.
func TestTreeUsageReadsArchivedDescendant_spec_8_8_904(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	tokens := sessionusage.NewMemory()
	archive := treearchive.NewMemory()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)

	root := sessionstore.Session{
		ID: "root", TenantID: "acme", RootSessionID: "root",
		State: session.StateCompleted, PodAssignment: "p0",
		CreatedAt: created, UpdatedAt: created.Add(60 * time.Second),
	}
	mustCreate(t, sessions, root)
	_ = tokens.Add(ctx, "acme", "root", 1000, 100)

	// The child's live row is gone (reclaimed); only its archived
	// TaskResult survives, with usage baked in.
	childResult, _ := json.Marshal(sessionrecord.Result{
		SchemaVersion: sessionrecord.SchemaVersion, TaskID: "kid", State: "completed",
		Usage: &sessionrecord.Usage{InputTokens: 500, OutputTokens: 50, WallClockSeconds: 30, PodMinutes: 0.5, CredentialLeaseMinutes: 0.5},
	})
	if err := archive.Archive(ctx, treearchive.ArchivedNode{
		TenantID: "acme", RootSessionID: "root", NodeSessionID: "kid",
		ParentSessionID: "root", State: "completed", Result: string(childResult),
		SettledAt: created.Add(40 * time.Second),
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	b := resultrollup.New(sessions, tokens, archive, fixedNow(created))
	u := b.Usage(ctx, root)
	tu := b.TreeUsage(ctx, root, u)
	if tu == nil {
		t.Fatal("treeUsage nil; archived descendant should settle the subtree")
	}
	if tu.TotalTasks != 2 {
		t.Fatalf("totalTasks = %d, want 2 (root + archived kid)", tu.TotalTasks)
	}
	if tu.InputTokens != 1500 || tu.OutputTokens != 150 {
		t.Fatalf("tokens = %d/%d, want 1500/150", tu.InputTokens, tu.OutputTokens)
	}
}

// TestTreeUsageNilWhenRootNonTerminal confirms an in-progress task has a
// null treeUsage per §8.8 line 917, regardless of descendants.
func TestTreeUsageNilWhenRootNonTerminal_spec_8_8_917(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	created := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	root := sessionstore.Session{
		ID: "root", TenantID: "acme", RootSessionID: "root",
		State: session.StateRunning, PodAssignment: "p0",
		CreatedAt: created, UpdatedAt: created.Add(5 * time.Second),
	}
	mustCreate(t, sessions, root)
	b := resultrollup.New(sessions, sessionusage.NewMemory(), treearchive.NewMemory(), fixedNow(created.Add(10*time.Second)))
	u := b.Usage(ctx, root)
	if tu := b.TreeUsage(ctx, root, u); tu != nil {
		t.Fatalf("treeUsage = %+v, want nil (root non-terminal)", tu)
	}
}

// TestNilBuilderSafe confirms a nil *Builder returns nil rollups rather
// than panicking, so a gateway wired without the accumulator degrades
// cleanly.
func TestNilBuilderSafe(t *testing.T) {
	var b *resultrollup.Builder
	sess := sessionstore.Session{ID: "x", State: session.StateCompleted}
	if u := b.Usage(context.Background(), sess); u != nil {
		t.Fatalf("nil builder Usage = %+v, want nil", u)
	}
	if tu := b.TreeUsage(context.Background(), sess, &sessionrecord.Usage{}); tu != nil {
		t.Fatalf("nil builder TreeUsage = %+v, want nil", tu)
	}
}
