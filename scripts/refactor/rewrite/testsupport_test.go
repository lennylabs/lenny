// SPDX-License-Identifier: MIT

package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

// readCommittedManifest returns the contents of the committed C1 move manifest
// (scripts/refactor/manifest). The test package lives at
// scripts/refactor/rewrite, so the manifest is one directory up.
func readCommittedManifest(t *testing.T) string {
	t.Helper()
	// The test's working directory is the package directory
	// (scripts/refactor/rewrite); the manifest is its parent's sibling file.
	path := filepath.Join("..", "manifest")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read committed manifest %q: %v", path, err)
	}
	return string(data)
}
