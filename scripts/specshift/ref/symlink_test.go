// SPDX-License-Identifier: MIT

package ref

import (
	"os"
	"path/filepath"
	"testing"
)

// This case pins that fix neither reads nor rewrites a file through a
// symbolic link. The reference pass is migration tooling rather than a
// platform behavior, so it carries no spec-section annotation.

func TestFixRefusesAFileThatIsASymlink(t *testing.T) {
	dir := copyFixture(t, cleanFixture)
	victim := filepath.Join(t.TempDir(), "victim.md")
	// The victim holds a link that a page move would make fix rewrite.
	const content = "[Warm pools](../../spec/capabilities/pools/warm-pools.md#pool-r1)\n"
	if err := os.WriteFile(victim, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "docs", "guide", "linked.md")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	move(t, dir, "spec/capabilities/pools/warm-pools.md", "spec/capabilities/runtimes/pools/warm-pools.md")
	if _, err := run(t, fixtureTree(dir, nil), "fix"); err == nil {
		t.Error("fix succeeded over a symlinked file")
	}
	if b, _ := os.ReadFile(victim); string(b) != content {
		t.Errorf("fix rewrote the file outside the repository:\n%s", b)
	}
}
