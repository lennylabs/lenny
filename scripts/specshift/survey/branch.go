// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"os"
)

// SpecChanges is what the working tree changes in the specification
// relative to a base commit: the requirements, operations, and definition
// blocks it adds, modifies, and retires, with the tree on either side.
type SpecChanges struct {
	// Changes holds every changed item, requirements first, then
	// operations, then definition blocks.
	Changes []ItemChange
	// Before is the specification at the base commit, and After the
	// specification of the working tree.
	Before, After *Snapshot
}

// CompareSpec compares the specification tree at a base commit with the
// working tree at root, the comparison the digest reports. The
// documentation-impact gate reads it to find the specification pages a
// branch touches.
func CompareSpec(ctx context.Context, root, base string) (*SpecChanges, error) {
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
	after, err := worktreeSnapshot(ctx, r)
	if err != nil {
		return nil, err
	}
	sc := &SpecChanges{Before: before, After: after}
	for _, kind := range []string{KindRequirement, KindOperation, KindBlock} {
		sc.Changes = append(sc.Changes, compare(before, after, kind)...)
	}
	return sc, nil
}

// ChangedFiles lists the files the working tree at root adds, modifies, or
// deletes relative to a base commit under the given pathspecs, with the
// untracked files under them.
func ChangedFiles(ctx context.Context, root, base string, pathspecs ...string) ([]string, error) {
	return repo{root: root}.changedFiles(ctx, base, pathspecs...)
}
