// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// spec: TESTING.md §12.0 (Tier 0 — Static: the gofumpt and goimports
// checks judge the checkout under test)

// TestScopeToRepoFilesDropsOutOfTreeGoPaths pins the line filter: a
// listed or parse-error line naming a Go file outside the checkout is
// dropped, one naming a checkout file is kept, and a line that names no
// Go file is kept so a tool failure is never hidden.
func TestScopeToRepoFilesDropsOutOfTreeGoPaths(t *testing.T) {
	files := map[string]bool{
		"pkg/a/a.go":        true,
		"tests/t/x_test.go": true,
	}
	cases := []struct {
		name  string
		out   string
		files map[string]bool
		want  string
	}{
		{name: "empty output", out: "", files: files, want: ""},
		{name: "blank lines only", out: "\n  \n", files: files, want: ""},
		{
			name:  "nested worktree and scratch paths dropped",
			out:   ".claude/worktrees/w1/pkg/a/a.go\nscratchpad/spike/main.go\n",
			files: files,
			want:  "",
		},
		{
			name:  "checkout file kept",
			out:   ".claude/worktrees/w1/pkg/a/a.go\npkg/a/a.go\n",
			files: files,
			want:  "pkg/a/a.go",
		},
		{
			name:  "dot-slash prefix normalized",
			out:   "./tests/t/x_test.go",
			files: files,
			want:  "./tests/t/x_test.go",
		},
		{
			name:  "parse error in checkout kept, out of tree dropped",
			out:   "pkg/a/a.go:3:1: expected declaration\nscratchpad/s.go:1:1: expected 'package'",
			files: files,
			want:  "pkg/a/a.go:3:1: expected declaration",
		},
		{
			name:  "error naming no Go file kept",
			out:   "open pkg/a: permission denied\nscratchpad/s.go",
			files: files,
			want:  "open pkg/a: permission denied",
		},
		{
			name:  "nil set keeps everything",
			out:   "scratchpad/s.go\npkg/a/a.go",
			files: nil,
			want:  "scratchpad/s.go\npkg/a/a.go",
		},
		{
			name:  "empty set drops every Go path",
			out:   "pkg/a/a.go",
			files: map[string]bool{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeToRepoFiles(tc.out, tc.files); got != tc.want {
				t.Fatalf("scopeToRepoFiles(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}

// gitIn runs git in dir with a fixed identity and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=alice", "-c", "user.email=alice@acme.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeScopeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unformattedGo is valid Go that gofumpt and gofmt both rewrite.
const unformattedGo = "package p\n\nfunc F( ) {\n}\n"

// newScopeFixtureRepo builds a git checkout holding a tracked formatted
// file, an untracked-but-not-ignored unformatted file, a git-ignored
// scratch tree, and a nested git worktree under .claude/worktrees/ whose
// copy of the tracked file is unformatted. The worktree directory is left
// out of .gitignore on purpose: git skips a nested worktree whether or
// not an ignore rule names it, and the test proves that. It returns the
// checkout root.
func newScopeFixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: precondition not met: git not on PATH")
	}
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	writeScopeFile(t, filepath.Join(root, "go.mod"), "module example.com/scope\n\ngo 1.21\n")
	writeScopeFile(t, filepath.Join(root, ".gitignore"), "scratchpad/\n")
	writeScopeFile(t, filepath.Join(root, "pkg", "p", "p.go"), "package p\n\nfunc F() {}\n")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "seed")

	writeScopeFile(t, filepath.Join(root, "pkg", "p", "new.go"), unformattedGo)
	writeScopeFile(t, filepath.Join(root, "scratchpad", "spike", "main.go"), unformattedGo)
	wt := filepath.Join(root, ".claude", "worktrees", "w1")
	gitIn(t, root, "worktree", "add", "-q", "-b", "w1", wt)
	writeScopeFile(t, filepath.Join(wt, "pkg", "p", "p.go"), unformattedGo)
	return root
}

// TestRepoGoFilesListsOnlyTheCheckout pins the scope: the tracked file and
// the untracked file .gitignore leaves in are listed, while the ignored
// scratch tree and the nested worktree are not, so another agent's
// in-progress edit cannot fail this checkout's gate.
func TestRepoGoFilesListsOnlyTheCheckout(t *testing.T) {
	root := newScopeFixtureRepo(t)
	files := repoGoFiles(root)
	var got []string
	for p := range files {
		got = append(got, p)
	}
	sort.Strings(got)
	want := []string{"pkg/p/new.go", "pkg/p/p.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repoGoFiles = %v, want %v", got, want)
	}
}

// TestRepoGoFilesOutsideGitKeepsEverything pins the fail-closed fallback:
// outside a git checkout no scope is known, so the filter keeps every line
// and the check stays as strict as the unscoped walk.
func TestRepoGoFilesOutsideGitKeepsEverything(t *testing.T) {
	dir := t.TempDir()
	// Stop git's upward search at the temp dir's parent, so a TMPDIR that
	// happens to sit inside some checkout does not supply a scope.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if files := repoGoFiles(dir); files != nil {
		t.Fatalf("repoGoFiles outside git = %v, want nil", files)
	}
	out := "scratchpad/s.go\npkg/a/a.go"
	if got := scopeFormatterOutput(out, dir); got != out {
		t.Fatalf("scopeFormatterOutput outside git = %q, want %q", got, out)
	}
}

// TestFormatterWalkScopedToCheckout runs the real gofumpt and goimports
// walks over the fixture, the way the static tier does, and asserts the
// scoped report names only the checkout's unformatted file. Without the
// scope both walks also report the nested worktree's copy and the
// scratch file, which is the failure the main checkout hit.
func TestFormatterWalkScopedToCheckout(t *testing.T) {
	root := newScopeFixtureRepo(t)
	for _, tool := range []struct {
		name string
		args []string
	}{
		{name: "gofumpt", args: []string{"-l", "."}},
		{name: "goimports", args: []string{"-l", "-local", "example.com/scope", "."}},
	} {
		t.Run(tool.name, func(t *testing.T) {
			path := resolveGoBin(tool.name)
			if path == "" {
				t.Skipf("blocked: precondition not met: %s not installed", tool.name)
			}
			cmd := exec.Command(path, tool.args...)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", tool.name, err, out)
			}
			raw := strings.Fields(string(out))
			sort.Strings(raw)
			if len(raw) < 2 {
				t.Fatalf("%s unscoped walk reported %v; the fixture expects it to reach the out-of-tree files", tool.name, raw)
			}
			if got := scopeFormatterOutput(string(out), root); got != "pkg/p/new.go" {
				t.Fatalf("%s scoped report = %q, want %q (unscoped: %v)", tool.name, got, "pkg/p/new.go", raw)
			}
		})
	}
}
