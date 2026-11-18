// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"os"
)

// CompareSpecRevisions compares the specification tree at a base commit
// with the tree at a head commit, or with the working tree at root when
// head is empty. CompareSpec is this comparison against the working tree.
// The test selector of `lenny-test run --changed` reads it to find the
// requirements, operations, and definition blocks a diff touches, and a
// diff the selector replays (`--since A..B`) names a head that is not the
// working tree.
func CompareSpecRevisions(ctx context.Context, root, base, head string) (*SpecChanges, error) {
	r := repo{root: root}
	scratch, err := os.MkdirTemp("", "specshift-compare-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	before, err := revSnapshot(ctx, r, base, scratch)
	if err != nil {
		return nil, err
	}
	var after *Snapshot
	if head == "" {
		after, err = worktreeSnapshot(ctx, r)
	} else {
		after, err = revSnapshot(ctx, r, head, scratch)
	}
	if err != nil {
		return nil, err
	}
	sc := &SpecChanges{Before: before, After: after}
	for _, kind := range []string{KindRequirement, KindOperation, KindBlock} {
		sc.Changes = append(sc.Changes, compare(before, after, kind)...)
	}
	return sc, nil
}
