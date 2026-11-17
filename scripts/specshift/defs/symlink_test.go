// SPDX-License-Identifier: MIT

package defs

import (
	"os"
	"path/filepath"
	"testing"
)

// This case pins that the generator neither reads nor writes a generated
// file through a symbolic link. The generator is tooling for the
// specification rather than a platform behavior, so it carries no
// spec-section annotation.

func TestWriteAndStaleRefuseASymlinkedGeneratedFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(victim, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "spec", "catalogs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "spec", "catalogs", "settings.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "spec", "components")); err != nil {
		t.Fatal(err)
	}
	for _, generated := range []map[string]string{
		{"spec/catalogs/settings.md": "generated\n"},
		{"spec/components/index.md": "generated\n"},
	} {
		if _, err := Stale(root, generated); err == nil {
			t.Errorf("Stale(%v) read through a symbolic link", generated)
		}
		if _, err := Write(root, generated); err == nil {
			t.Errorf("Write(%v) returned no error", generated)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "outside\n" {
		t.Errorf("the file outside the repository was overwritten: %q", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "index.md")); err == nil {
		t.Error("a generated index was written outside the repository")
	}
}
