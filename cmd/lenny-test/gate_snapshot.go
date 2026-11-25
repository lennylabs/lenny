// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// This file holds the snapshot the gate's static tier reads (change 0172,
// decision 14). The gate resolves HEAD once, to a commit, and the static
// tier reads that commit rather than the worktree: its checks run in a
// checkout of the commit that the gate user cannot write, and the secret
// scan reads the commit by its ID. A test in a lane that runs beside the
// static tier runs as the gate user, so it can edit the worktree, or move
// HEAD, and restore it before its run ends; the checkout holds what the
// commit holds whatever such a test does.
//
// The checkout is a git repository of its own, so the static tier's git
// reads (the changed-line lint, the specification annotation lint, `buf
// breaking`, and the formatters' file lists) work in it unchanged. It
// borrows the worktree's objects through git's alternates file rather than
// copying them, it carries the integration branch's refs at the values the
// worktree held when the snapshot was taken, and its HEAD names the
// worktree's branch, or the commit when the worktree's HEAD is detached.
//
// In the gate container, the container script creates the checkout as the
// gate user before any test runs, then root gives it to root, removes
// every write bit, and bind-mounts it read-only over itself
// (gateContainerScript). A `--here` run without the container creates its
// own checkout and removes its write bits, which keeps out an edit by
// mistake rather than a process of the same user. The harness is
// contributor tooling, so this file carries no specification annotation.

const (
	// gateEnvCommit carries the commit the container script resolved HEAD
	// to, once, before the build of lenny-test, into the gate run.
	gateEnvCommit = "LENNY_GATE_COMMIT"
	// gateEnvSnapshot names the checkout of that commit the static tier
	// reads. The host half sets it to gateSnapshotPath of the worktree.
	gateEnvSnapshot = "LENNY_GATE_SNAPSHOT"
	// gateSnapshotParent is the root-owned directory of the container's
	// own file system that holds the checkout. The path of the checkout
	// stays the same from one run of a worktree to the next, so the Go
	// build cache entries of the static tier's builds, which name the
	// package directory, and golangci-lint's cache directory, which is
	// named for the checkout's path (golangciLintCache), are warm after
	// the first run.
	gateSnapshotParent = "/var/lib/lenny-gate/static"
	// hereSnapshotPrefix names the checkout a `--here` run creates under
	// the temporary directory when the container gave it none.
	hereSnapshotPrefix = "lenny-static-"
)

// gateSnapshotPath returns the path of the checkout the gate container
// creates for the worktree at root: gateSnapshotParent and 8 hex digits
// of a digest of root, as the shared fixture directory is named
// (hostTmpDir), so two worktrees keep apart caches as they did when the
// static tier read the worktree itself.
func gateSnapshotPath(root string) string {
	return filepath.Join(gateSnapshotParent, rootDigest(root))
}

// rootDigest returns 8 hex digits of the SHA-256 of a worktree path.
func rootDigest(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:8]
}

// staticSnapshot is the checkout one gate run's static tier reads.
type staticSnapshot struct {
	// dir is the checkout's root.
	dir string
	// cleanup removes a checkout the run created itself; it is a no-op
	// for the container's.
	cleanup func()
}

// gitRunner runs git with args in dir and returns its trimmed standard
// output. Tests replace it to read a fixture repository.
type gitRunner func(ctx context.Context, dir string, args ...string) (string, error)

// createStaticSnapshot makes dir, which must be absent or an empty
// directory, a checkout of commit borrowing the objects of the repository
// at root. The refs of gateIntegrationRefs that root carries are copied
// at their current values, HEAD names root's branch at commit, or commit
// itself when root's HEAD is detached, and the working tree and index
// hold commit's tree. It refuses a dir that holds a file, and it checks
// that the checkout's HEAD resolves to commit when it is done.
func createStaticSnapshot(ctx context.Context, git gitRunner, root, commit, dir string) error {
	if commit == "" {
		return errors.New("no commit to take the static tier's snapshot of")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the snapshot directory %s: %w", dir, err)
	}
	if entries, err := os.ReadDir(dir); err != nil {
		return fmt.Errorf("read the snapshot directory %s: %w", dir, err)
	} else if len(entries) > 0 {
		return fmt.Errorf("the snapshot directory %s is not empty", dir)
	}
	common, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("locate the repository's objects: %w", err)
	}
	if _, err := git(ctx, dir, "init", "-q"); err != nil {
		return fmt.Errorf("initialize the snapshot: %w", err)
	}
	alternates := filepath.Join(dir, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(filepath.Join(common, "objects")+"\n"), 0o644); err != nil {
		return fmt.Errorf("point the snapshot at the repository's objects: %w", err)
	}
	if err := copyIntegrationRefs(ctx, git, root, dir); err != nil {
		return err
	}
	if err := setSnapshotHead(ctx, git, root, dir, commit); err != nil {
		return err
	}
	if _, err := git(ctx, dir, "reset", "-q", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("check out %s in the snapshot: %w", shortCommit(commit), err)
	}
	got, err := git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("read the snapshot's HEAD: %w", err)
	}
	if got != commit {
		return fmt.Errorf("the snapshot's HEAD is %s, not %s", shortCommit(got), shortCommit(commit))
	}
	return nil
}

// copyIntegrationRefs copies each ref of gateIntegrationRefs that the
// repository at root carries into the snapshot at dir, at its current
// value, so the static tier's checks that compare with the integration
// branch find it in the snapshot.
func copyIntegrationRefs(ctx context.Context, git gitRunner, root, dir string) error {
	for _, r := range gateIntegrationRefs {
		value, err := git(ctx, root, "for-each-ref", "--format=%(objectname)", r.full)
		if err != nil {
			return fmt.Errorf("read %s: %w", r.full, err)
		}
		if value == "" {
			continue
		}
		if _, err := git(ctx, dir, "update-ref", r.full, value); err != nil {
			return fmt.Errorf("copy %s into the snapshot: %w", r.full, err)
		}
	}
	return nil
}

// setSnapshotHead points the snapshot's HEAD at commit: through the branch
// root's HEAD names, set to commit, so a check that reads the branch's name
// (`buf breaking` hard-fails only on main) reads the same name, or
// directly at commit when root's HEAD is detached.
func setSnapshotHead(ctx context.Context, git gitRunner, root, dir, commit string) error {
	branch, err := git(ctx, root, "symbolic-ref", "-q", "HEAD")
	if err != nil || !strings.HasPrefix(branch, "refs/heads/") {
		if _, err := git(ctx, dir, "update-ref", "--no-deref", "HEAD", commit); err != nil {
			return fmt.Errorf("set the snapshot's HEAD to %s: %w", shortCommit(commit), err)
		}
		return nil
	}
	if _, err := git(ctx, dir, "update-ref", branch, commit); err != nil {
		return fmt.Errorf("set %s to %s in the snapshot: %w", branch, shortCommit(commit), err)
	}
	if _, err := git(ctx, dir, "symbolic-ref", "HEAD", branch); err != nil {
		return fmt.Errorf("point the snapshot's HEAD at %s: %w", branch, err)
	}
	return nil
}

// checkStaticSnapshot returns why the checkout at dir cannot stand as the
// snapshot of commit, or nil: its HEAD must resolve to commit.
func checkStaticSnapshot(ctx context.Context, git gitRunner, dir, commit string) error {
	got, err := git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("read the HEAD of the static tier's snapshot %s: %w", dir, err)
	}
	if got != commit {
		return fmt.Errorf("the static tier's snapshot %s holds %s, and the gate resolved HEAD to %s", dir, shortCommit(got), shortCommit(commit))
	}
	return nil
}

// snapshotWritable reports whether the gate's own user can write the
// snapshot's root directory. The container's snapshot belongs to root,
// has no write bits, and lies on a read-only bind mount, so the check
// fails for it whoever runs the gate.
func snapshotWritable(dir string) bool {
	return syscall.Access(dir, 0o2) == nil
}

// setTreeWritable adds or removes the write bits of every file and
// directory under dir. A `--here` run removes them from the checkout it
// creates, and adds them back before it removes the checkout. Symbolic
// links are left as they are.
func setTreeWritable(dir string, writable bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if writable {
			mode |= 0o200
		} else {
			mode &^= 0o222
		}
		return os.Chmod(p, mode)
	})
}

// removeSnapshot removes a checkout a `--here` run created, giving its
// directories their write bits back first.
func removeSnapshot(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := setTreeWritable(dir, true); err != nil {
		return fmt.Errorf("make the snapshot %s writable to remove it: %w", dir, err)
	}
	return os.RemoveAll(dir)
}

// hereSnapshotPath returns the path of the checkout a `--here` run
// creates for the worktree at root, under the temporary directory, so a
// later run of the same worktree reuses its warm caches.
func hereSnapshotPath(root string) string {
	return filepath.Join(os.TempDir(), hereSnapshotPrefix+rootDigest(root))
}

// prepareStaticSnapshot returns the checkout the static tier reads for
// commit. A checkout the container named in given is checked; otherwise
// the run creates one at hereSnapshotPath, replacing what an earlier run
// left there, removes its write bits, and removes it in cleanup.
func prepareStaticSnapshot(ctx context.Context, git gitRunner, root, commit, given string) (staticSnapshot, error) {
	if given != "" {
		if err := checkStaticSnapshot(ctx, git, given, commit); err != nil {
			return staticSnapshot{}, err
		}
		return staticSnapshot{dir: given, cleanup: func() {}}, nil
	}
	dir := hereSnapshotPath(root)
	if err := removeSnapshot(dir); err != nil {
		return staticSnapshot{}, err
	}
	if err := createStaticSnapshot(ctx, git, root, commit, dir); err != nil {
		_ = removeSnapshot(dir)
		return staticSnapshot{}, err
	}
	if err := setTreeWritable(dir, false); err != nil {
		_ = removeSnapshot(dir)
		return staticSnapshot{}, fmt.Errorf("remove the write bits of the snapshot %s: %w", dir, err)
	}
	return staticSnapshot{dir: dir, cleanup: func() { _ = removeSnapshot(dir) }}, nil
}

// runStaticSnapshot handles `lenny-test static-snapshot <commit> <dir>`,
// which the gate container's script runs as the gate user: it makes dir a
// checkout of commit borrowing the objects of the repository that holds
// the current directory.
func runStaticSnapshot(args []string) int {
	return staticSnapshotCommand(args, gitOutput, repoRoot(), os.Stderr)
}

// staticSnapshotCommand runs the static-snapshot command with git and
// root, and reports to stderr. It returns 0 when the checkout is ready, 1
// when it could not be made, and 2 on a usage error.
func staticSnapshotCommand(args []string, git gitRunner, root string, stderr io.Writer) int {
	if len(args) != 2 || args[0] == "" || args[1] == "" {
		fmt.Fprintln(stderr, "usage: lenny-test static-snapshot <commit> <dir>")
		return 2
	}
	if err := createStaticSnapshot(context.Background(), git, root, args[0], args[1]); err != nil {
		fmt.Fprintf(stderr, "lenny-test static-snapshot: %v\n", err)
		return 1
	}
	return 0
}
