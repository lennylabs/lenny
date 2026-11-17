// SPDX-License-Identifier: MIT

package scope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These cases pin the checked writer and reader the passes, the
// generators, and the digest use. They are tooling for the specification
// rather than a platform behavior, so they carry no spec-section
// annotation.

// outsideVictim returns a file outside the repository root with known
// content.
func outsideVictim(t *testing.T) (string, string) {
	t.Helper()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	const content = "outside\n"
	if err := os.WriteFile(victim, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return victim, content
}

func TestCheckedWritesRefuseASymlinkedTarget(t *testing.T) {
	root := t.TempDir()
	victim, content := outsideVictim(t)
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "spec", "registry.json")); err != nil {
		t.Fatal(err)
	}
	writers := map[string]func() error{
		"WriteChecked": func() error { return WriteChecked(root, "spec/registry.json", []byte("x"), 0o644) },
		"DirWriter":    func() error { return DirWriter(root)("spec/registry.json", []byte("x")) },
	}
	for name, write := range writers {
		err := write()
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("%s through a symlink returned %v; want a refusal", name, err)
		}
	}
	if _, err := ReadChecked(root, "spec/registry.json"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("ReadChecked through a symlink returned %v; want a refusal", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != content {
		t.Errorf("the file outside the repository was overwritten: %q", b)
	}
}

func TestCheckedWritesRefuseADirectoryThatResolvesOutsideTheRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "catalogs")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"catalogs/new.md", "catalogs/deeper/new.md"} {
		if err := WriteChecked(root, target, []byte("x"), 0o644); err == nil || !strings.Contains(err.Error(), "outside the repository") {
			t.Errorf("WriteChecked(%s) returned %v; want a refusal", target, err)
		}
	}
	if err := DirWriter(root)("catalogs/new.md", []byte("x")); err == nil {
		t.Error("DirWriter wrote into a directory that resolves outside the root")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Errorf("files were written outside the repository: %v", entries)
	}
	for _, target := range []string{"../escape.md", "/abs.md"} {
		if _, err := CheckedPath(root, target); err == nil {
			t.Errorf("CheckedPath(%s) accepted a path outside the repository", target)
		}
	}
}

func TestCheckedWritesAcceptAPathInsideALinkedRoot(t *testing.T) {
	// A repository reached through a symlink, as /tmp is on macOS, is
	// still the repository: the check compares resolved paths.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteChecked(link, "changes/0001-x/digests/0001.S1.md", []byte("digest\n"), 0o644); err != nil {
		t.Fatalf("WriteChecked under a linked root: %v", err)
	}
	b, err := ReadChecked(link, "changes/0001-x/digests/0001.S1.md")
	if err != nil || string(b) != "digest\n" {
		t.Errorf("ReadChecked = %q, %v", b, err)
	}
	if _, err := ReadChecked(link, "absent.md"); !os.IsNotExist(err) {
		t.Errorf("ReadChecked of an absent file returned %v; want a not-exist error", err)
	}
}
