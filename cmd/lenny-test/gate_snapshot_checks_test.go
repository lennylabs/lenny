// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The static tier's snapshot is contributor tooling (change 0172,
// decision 14), so these cases carry no specification annotation.

// stubCommand writes an executable stand-in under dir that appends its
// name, its working directory, and its arguments to log.
func stubCommand(t *testing.T, dir, name, log string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := "#!/bin/sh\necho \"" + name + " $(pwd -P) $*\" >> '" + log + "'\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEveryStaticCheckRunsInTheCheckoutItIsGiven runs each check of the
// static tier's table, other than golangci-lint and the secret scan, which
// their own tests cover, with stand-ins for the commands it runs, and
// checks that each ran in the checkout the table was built for rather than
// in the current directory (change 0172, decision 14).
func TestEveryStaticCheckRunsInTheCheckoutItIsGiven(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go", "buf", "bash", "gofumpt", "goimports", "git"} {
		stubCommand(t, bin, name, log)
	}
	self := stubCommand(t, bin, "lenny-test", log)
	prev := staticSelfFn
	t.Cleanup(func() { staticSelfFn = prev })
	staticSelfFn = func() (string, error) { return self, nil }
	t.Setenv("PATH", bin)
	checks := staticChecks(root, "main", "HEAD")
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range checks {
		if strings.HasPrefix(c.name, "scripts/") {
			if err := os.WriteFile(filepath.Join(root, strings.Fields(c.name)[0]), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ran := 0
	for _, c := range checks {
		if c.name == "golangci-lint run" || strings.HasPrefix(c.name, "gitleaks") {
			continue
		}
		_, _ = c.run()
		ran++
	}
	body, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		name, rest, _ := strings.Cut(line, " ")
		dir, args, _ := strings.Cut(rest, " ")
		seen[name] = true
		// Locating golangci-lint asks the go command for GOPATH, which
		// reads no file of the tree.
		if name == "go" && args == "env GOPATH" {
			continue
		}
		if dir != want {
			t.Errorf("%s %s ran in %s; want the checkout %s", name, args, dir, want)
		}
	}
	for _, name := range []string{"go", "buf", "bash", "gofumpt", "goimports", "lenny-test"} {
		if !seen[name] {
			t.Errorf("no check ran %s (ran %d checks): %s", name, ran, body)
		}
	}
}

// TestStaticSnapshotStopsAtEachGitFailure fails each git command of the
// snapshot in turn and checks that the snapshot reports the failure rather
// than leaving a checkout of something else. The one exception is the read
// of the worktree's branch, whose failure reads as a detached HEAD and
// still yields a checkout of the commit.
func TestStaticSnapshotStopsAtEachGitFailure(t *testing.T) {
	r, head := snapshotFixture(t)
	var calls [][]string
	counting := func(ctx context.Context, dir string, args ...string) (string, error) {
		calls = append(calls, args)
		return gitOutput(ctx, dir, args...)
	}
	if err := createStaticSnapshot(context.Background(), counting, r.root, head, filepath.Join(t.TempDir(), "s")); err != nil {
		t.Fatal(err)
	}
	for n := range calls {
		i := -1
		failing := func(ctx context.Context, dir string, args ...string) (string, error) {
			i++
			if i == n {
				return "", errors.New("git failed")
			}
			return gitOutput(ctx, dir, args...)
		}
		err := createStaticSnapshot(context.Background(), failing, r.root, head, filepath.Join(t.TempDir(), "s"))
		if err == nil && calls[n][0] != "symbolic-ref" {
			t.Errorf("git %v failed and the snapshot was made", calls[n])
		}
	}
	if err := checkStaticSnapshot(context.Background(), func(context.Context, string, ...string) (string, error) {
		return "", errors.New("git failed")
	}, t.TempDir(), head); err == nil {
		t.Error("checkStaticSnapshot accepted a HEAD it could not read")
	}
}

// TestHereSnapshotHelpersHandleTheirEdgeCases checks the helpers of a
// `--here` snapshot: write bits are cleared on a tree with a symbolic link,
// removing an absent snapshot succeeds, and a temporary directory that
// does not exist, or an empty commit, fails the snapshot.
func TestHereSnapshotHelpersHandleTheirEdgeCases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := setTreeWritable(dir, false); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "f")); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Errorf("f keeps write bits: %v (%v)", info, err)
	}
	if err := removeSnapshot(dir); err != nil {
		t.Fatal(err)
	}
	if err := removeSnapshot(dir); err != nil {
		t.Errorf("removing an absent snapshot: %v", err)
	}
	r, head := snapshotFixture(t)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, err := prepareStaticSnapshot(context.Background(), gitOutput, r.root, head, ""); err == nil {
		t.Error("a snapshot under a temporary directory that does not exist was made")
	}
	if _, err := prepareStaticSnapshot(context.Background(), gitOutput, r.root, "", ""); err == nil {
		t.Error("a snapshot of no commit was made")
	}
}
