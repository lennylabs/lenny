// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// changeSummary renders each change as `<change> <id>`, sorted.
func changeSummary(sc *SpecChanges) []string {
	out := []string{}
	for _, c := range sc.Changes {
		out = append(out, c.Change+" "+c.Item().ID)
	}
	sort.Strings(out)
	return out
}

// TestCompareSpecRevisionsReadsTheHeadCommitRatherThanTheWorktree pins the
// comparison of two commits: it reads the head commit's tree, so an
// uncommitted edit does not count, and an empty head reads the working
// tree as CompareSpec does.
func TestCompareSpecRevisionsReadsTheHeadCommitRatherThanTheWorktree(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	committed, err := CompareSpecRevisions(ctx, f.root, f.base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := changeSummary(committed)
	if len(want) == 0 {
		t.Fatalf("the fixture branch changes no item")
	}
	// An uncommitted edit that retires every requirement of the page.
	page := filepath.Join(f.root, "spec", "capabilities", "pools", "warm-pools.md")
	if err := os.WriteFile(page, []byte("---\ncode: POOL\ntype: capability\narea: pools\nsummary: >-\n  Keeps warm pods.\n---\n\n# Warm pools\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := CompareSpecRevisions(ctx, f.root, f.base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := changeSummary(again); !equalStrings(got, want) {
		t.Errorf("a commit comparison read the worktree: %v, want %v", got, want)
	}
	worktree, err := CompareSpecRevisions(ctx, f.root, f.base, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := changeSummary(worktree); equalStrings(got, want) {
		t.Errorf("an empty head must read the worktree, which retires more; got %v", got)
	}
	if _, err := CompareSpecRevisions(ctx, f.root, f.base, "no-such-commit"); err == nil {
		t.Errorf("an unknown head must fail")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
