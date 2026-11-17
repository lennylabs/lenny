// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// This case pins that the digest is not written through a symbolic link.
// The digest is tooling for the change records rather than a platform
// behavior, so it carries no spec-section annotation.

func TestWriteDigestRefusesASymlinkedDigestFolder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := t.TempDir()
	digests := filepath.Join(f.root, "changes", "0200-fixture", "digests")
	if err := os.RemoveAll(digests); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, digests); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteDigest(context.Background(), f.root, DigestOptions{Step: "0200.S1"}, true); err == nil {
		t.Error("WriteDigest wrote into a digest folder that resolves outside the repository")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("a digest was written outside the repository: %v", entries)
	}
}

func TestWriteDigestRefusesASymlinkedDigestFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	victim := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(victim, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digests := filepath.Join(f.root, "changes", "0200-fixture", "digests")
	if err := os.MkdirAll(digests, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(digests, "0200.S1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteDigest(context.Background(), f.root, DigestOptions{Step: "0200.S1"}, true); err == nil {
		t.Error("WriteDigest wrote through a symlinked digest file")
	}
	if b, _ := os.ReadFile(victim); string(b) != "outside\n" {
		t.Errorf("the file outside the repository was overwritten: %q", b)
	}
}
