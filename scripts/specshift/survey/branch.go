// SPDX-License-Identifier: MIT

package survey

import (
	"context"
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
	return CompareSpecRevisions(ctx, root, base, "")
}

// ChangedFiles lists the files the working tree at root adds, modifies, or
// deletes relative to a base commit under the given pathspecs, with the
// untracked files under them.
func ChangedFiles(ctx context.Context, root, base string, pathspecs ...string) ([]string, error) {
	return repo{root: root}.changedFiles(ctx, base, pathspecs...)
}
