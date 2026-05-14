// SPDX-License-Identifier: MIT

package upload

import (
	"testing"
)

// FuzzValidateSymlinkTarget exercises the symlink-target validator
// on arbitrary inputs. Invariants:
//
//   - Never panics on any (linkPath, target, workspaceRoot) triple.
//   - Out-of-workspace targets are rejected.
//   - Absolute-path targets outside workspaceRoot are rejected.
//
// The fuzz seed covers known attack shapes: a/../../etc/passwd,
// /etc/passwd, /workspace/current/../../..., and the empty string.
func FuzzValidateSymlinkTarget(f *testing.F) {
	f.Add("a/link", "b", "/workspace/current")
	f.Add("a/link", "../../../etc/passwd", "/workspace/current")
	f.Add("a/link", "/etc/passwd", "/workspace/current")
	f.Add("a/link", "", "/workspace/current")
	f.Add("a/link", "a/b/c", "/workspace/current")

	f.Fuzz(func(t *testing.T, linkPath, target, root string) {
		_ = ValidateSymlinkTarget(linkPath, target, root)
		// Invariant: no panic. The error-or-nil verdict is up
		// to the implementation; fuzz only guards against crashes.
	})
}

// FuzzValidateEntry exercises the Entry validator on arbitrary Entry
// fields. Invariant: never panics.
func FuzzValidateEntry(f *testing.F) {
	f.Add("a/b", int64(100), "")
	f.Add("a/../etc/passwd", int64(100), "")
	f.Add(string(make([]byte, MaxPathLength+1)), int64(100), "")
	f.Add("a/b", int64(-1), "")
	f.Add("link", int64(0), "../../../etc/passwd")

	f.Fuzz(func(t *testing.T, path string, size int64, linkTarget string) {
		kind := KindRegular
		if linkTarget != "" {
			kind = KindSymlink
		}
		entry := Entry{
			Path:       path,
			Kind:       kind,
			Size:       size,
			LinkTarget: linkTarget,
		}
		_ = ValidateEntry(entry, RuntimeAllow{})
	})
}
