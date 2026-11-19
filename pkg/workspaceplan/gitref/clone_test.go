// SPDX-License-Identifier: MIT

package gitref

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// headSHA returns the checked-out commit SHA of the repo at dir.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestClonePinsCommit(t *testing.T) {
	src, want := tempRepo(t)
	dest := filepath.Join(t.TempDir(), "workspace")
	if err := Clone(context.Background(), src, want, dest, CloneOptions{}); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); err != nil {
		t.Errorf("cloned workspace is missing README.md: %v", err)
	}
	if got := headSHA(t, dest); got != want {
		t.Errorf("checked-out HEAD = %q, want the pinned commit %q", got, want)
	}
}

func TestCloneShallow(t *testing.T) {
	src, want := tempRepo(t)
	dest := filepath.Join(t.TempDir(), "workspace")
	if err := Clone(context.Background(), src, want, dest, CloneOptions{Depth: 1}); err != nil {
		t.Fatalf("Clone with Depth=1: %v", err)
	}
	if got := headSHA(t, dest); got != want {
		t.Errorf("checked-out HEAD = %q, want %q", got, want)
	}
}

func TestCloneRejectsNonSHA(t *testing.T) {
	src, _ := tempRepo(t)
	dest := filepath.Join(t.TempDir(), "workspace")
	// Clone requires a pinned commit SHA, not a branch name.
	if err := Clone(context.Background(), src, "main", dest, CloneOptions{}); err == nil {
		t.Error("Clone must reject a non-SHA ref")
	}
}

func TestCloneMissingRepoFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dest := filepath.Join(t.TempDir(), "workspace")
	err := Clone(context.Background(), filepath.Join(t.TempDir(), "no-such-repo"),
		"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678", dest, CloneOptions{})
	if err == nil {
		t.Error("Clone against a nonexistent repo should fail")
	}
}
