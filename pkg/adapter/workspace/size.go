// SPDX-License-Identifier: MIT

package workspace

import (
	"os"
	"path/filepath"
)

// Size returns the total on-disk byte count of the regular files under
// root. The §4.4 line 254 pre-checkpoint workspace-size probe calls
// this against `/workspace/current` (or the slot-specific path under
// concurrent-workspace mode) before quiescing the runtime. A missing
// root returns (0, nil) so an unconfigured workspace is treated as
// empty rather than as a failure.
//
// Symlinks are counted by their own inode size (link target size),
// not by what they point to, so a runaway symlink chain cannot make
// the probe report inflated totals. Directory entries themselves
// contribute zero bytes (the spec measures workspace contents, not
// metadata overhead).
//
// spec: §4.4 line 254 — "it stats the total size of /workspace/current
// (or the slot-specific path)".
func Size(root string) (int64, error) {
	if root == "" {
		return 0, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	walkErr := filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		total += fi.Size()
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	return total, nil
}
