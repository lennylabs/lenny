// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The static tier's snapshot is contributor tooling (change 0172,
// decision 14), so these cases carry no specification annotation.

// snapshotFixture returns a repository on branch feature, cut from main,
// with an origin/main ref, an uncommitted edit, and an untracked file, and
// the commit HEAD names.
func snapshotFixture(t *testing.T) (lintRepo, string) {
	t.Helper()
	r := newLintRepo(t, "main")
	r.git("update-ref", "refs/remotes/origin/main", r.git("rev-parse", "HEAD"))
	r.git("checkout", "-q", "-b", "feature")
	head := r.commit("b.go", "package m\n\nvar B = 1\n", "feature work")
	r.write("b.go", "package m\n\nvar B = 2\n")
	r.write("planted.go", "package m\n")
	return r, head
}

// snapGit runs git in dir and returns its trimmed output, failing the test
// on an error.
func snapGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// TestStaticSnapshotHoldsTheCommitAndNothingElse checks that the snapshot
// holds the commit's tree, without the worktree's uncommitted edit or its
// untracked file, has a clean status, names the worktree's branch at the
// commit, and carries main and origin/main at the worktree's values, and
// that a later commit in the worktree does not change it.
func TestStaticSnapshotHoldsTheCommitAndNothingElse(t *testing.T) {
	r, head := snapshotFixture(t)
	dir := filepath.Join(t.TempDir(), "snap")
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, head, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "b.go"))
	if err != nil || string(b) != "package m\n\nvar B = 1\n" {
		t.Errorf("the snapshot's b.go = %q (%v); want the committed content", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "planted.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the snapshot holds the untracked planted.go (stat: %v)", err)
	}
	if st := snapGit(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("the snapshot's status is %q; want clean", st)
	}
	if got := snapGit(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/feature" {
		t.Errorf("the snapshot's HEAD names %s; want refs/heads/feature", got)
	}
	for _, ref := range []string{"refs/heads/main", "refs/remotes/origin/main"} {
		if got, want := snapGit(t, dir, "rev-parse", ref), r.git("rev-parse", ref); got != want {
			t.Errorf("the snapshot's %s = %s; want %s", ref, got, want)
		}
	}
	if mb := snapGit(t, dir, "merge-base", "HEAD", "main"); mb != r.git("rev-parse", "main") {
		t.Errorf("the merge base with main in the snapshot is %s", mb)
	}
	r.commit("c.go", "package m\n", "after the snapshot")
	if got := snapGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("the snapshot's HEAD moved to %s after a commit in the worktree; want %s", got, head)
	}
	if err := checkStaticSnapshot(context.Background(), gitOutput, dir, head); err != nil {
		t.Errorf("checkStaticSnapshot: %v", err)
	}
	if err := checkStaticSnapshot(context.Background(), gitOutput, dir, r.git("rev-parse", "HEAD")); err == nil {
		t.Error("checkStaticSnapshot accepted a snapshot of another commit")
	}
}

// TestStaticSnapshotOfADetachedHeadAndOfAnEarlierCommit checks that a
// worktree with a detached HEAD gives a snapshot whose HEAD is the commit,
// and that the snapshot holds the commit it is given rather than the
// worktree's HEAD, so HEAD moving after the gate resolved it does not
// change what the static tier reads.
func TestStaticSnapshotOfADetachedHeadAndOfAnEarlierCommit(t *testing.T) {
	r, head := snapshotFixture(t)
	earlier := r.git("rev-parse", "main")
	r.git("checkout", "-q", "--detach", head)
	dir := filepath.Join(t.TempDir(), "snap")
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, earlier, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(context.Background(), dir, "symbolic-ref", "-q", "HEAD"); err == nil {
		t.Error("the snapshot's HEAD names a branch; want it detached, as the worktree's is")
	}
	if got := snapGit(t, dir, "rev-parse", "HEAD"); got != earlier {
		t.Errorf("the snapshot's HEAD is %s; want the commit it was given, %s", got, earlier)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the snapshot of %s holds b.go, a file of a later commit (stat: %v)", earlier, err)
	}
}

// TestStaticSnapshotRefusesADirectoryThatHoldsFilesOrNoCommit checks the
// refusals: a directory that already holds a file, an empty commit, and a
// commit the repository does not hold.
func TestStaticSnapshotRefusesADirectoryThatHoldsFilesOrNoCommit(t *testing.T) {
	r, head := snapshotFixture(t)
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, head, full); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a directory holding a file: %v; want a refusal", err)
	}
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, "", filepath.Join(t.TempDir(), "s")); err == nil {
		t.Error("an empty commit was accepted")
	}
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, strings.Repeat("0", 40), filepath.Join(t.TempDir(), "s")); err == nil {
		t.Error("a commit the repository does not hold was accepted")
	}
}

// TestHereSnapshotIsReadOnlyAndRemovedAfterTheRun checks the snapshot a
// `--here` run creates: it lies under the temporary directory, replaces
// what an earlier run left there, has no write bits, and its cleanup
// removes it. A checkout the container gave is checked rather than
// created, and its cleanup leaves it alone.
func TestHereSnapshotIsReadOnlyAndRemovedAfterTheRun(t *testing.T) {
	r, head := snapshotFixture(t)
	t.Setenv("TMPDIR", t.TempDir())
	stale := hereSnapshotPath(r.root)
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "left-behind"), []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	snap, err := prepareStaticSnapshot(context.Background(), gitOutput, r.root, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if snap.dir != stale || !pathWithin(snap.dir, os.TempDir()) {
		t.Errorf("the snapshot is %s; want %s under the temporary directory", snap.dir, stale)
	}
	if _, err := os.Stat(filepath.Join(snap.dir, "left-behind")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an earlier run's file survived (stat: %v)", err)
	}
	if os.Geteuid() != 0 && snapshotWritable(snap.dir) {
		t.Error("the --here snapshot is writable by its own user")
	}
	if info, err := os.Stat(filepath.Join(snap.dir, "b.go")); err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Errorf("the snapshot's b.go has mode %v (%v); want no write bits", info.Mode(), err)
	}
	snap.cleanup()
	if _, err := os.Stat(snap.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the cleanup left %s (stat: %v)", snap.dir, err)
	}

	given := filepath.Join(t.TempDir(), "container")
	if err := createStaticSnapshot(context.Background(), gitOutput, r.root, head, given); err != nil {
		t.Fatal(err)
	}
	got, err := prepareStaticSnapshot(context.Background(), gitOutput, r.root, head, given)
	if err != nil || got.dir != given {
		t.Fatalf("a given snapshot: %+v, %v", got, err)
	}
	got.cleanup()
	if _, err := os.Stat(given); err != nil {
		t.Errorf("the cleanup removed the container's snapshot: %v", err)
	}
	if _, err := prepareStaticSnapshot(context.Background(), gitOutput, r.root, r.git("rev-parse", "main"), given); err == nil {
		t.Error("a given snapshot of another commit was accepted")
	}
}

// TestStaticSnapshotCommandUsage checks the exit codes of `lenny-test
// static-snapshot`: 2 for a usage error, 1 when the checkout cannot be
// made, and 0 when it is ready.
func TestStaticSnapshotCommandUsage(t *testing.T) {
	r, head := snapshotFixture(t)
	var stderr bytes.Buffer
	for _, args := range [][]string{nil, {head}, {head, ""}, {"", "x"}, {head, "a", "b"}} {
		if code := staticSnapshotCommand(args, gitOutput, r.root, &stderr); code != 2 {
			t.Errorf("args %q: exit %d; want 2", args, code)
		}
	}
	if code := staticSnapshotCommand([]string{strings.Repeat("0", 40), filepath.Join(t.TempDir(), "s")}, gitOutput, r.root, &stderr); code != 1 {
		t.Errorf("an unknown commit: exit %d; want 1", code)
	}
	if code := staticSnapshotCommand([]string{head, filepath.Join(t.TempDir(), "s")}, gitOutput, r.root, &stderr); code != 0 {
		t.Errorf("a good snapshot: exit %d; want 0\n%s", code, stderr.String())
	}
}

// TestGateRunsTheStaticTierOnTheSnapshotOfTheResolvedCommit checks the
// gate's side: the static tier gets the snapshot's directory and the
// commit the container resolved, the verdict records both, and a HEAD that
// differs at the start from the resolved commit keeps the verdict from
// PASS.
func TestGateRunsTheStaticTierOnTheSnapshotOfTheResolvedCommit(t *testing.T) {
	g := stubGateRunner()
	var gotDir, gotRev, gotCommit, gotGiven string
	g.snapshot = func(commit, given string) (staticSnapshot, error) {
		gotCommit, gotGiven = commit, given
		return staticSnapshot{dir: stubSnapshotDir, cleanup: func() {}}, nil
	}
	g.static = func(dir, _, rev string) (string, string, *tierResult) {
		gotDir, gotRev = dir, rev
		return verdictstatus.Pass, "", &tierResult{Total: 1, Passed: 1}
	}
	v := g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test", commit: "abc123", snapshot: "/var/lib/lenny-gate/static/x"})
	if gotCommit != "abc123" || gotGiven != "/var/lib/lenny-gate/static/x" || gotDir != stubSnapshotDir || gotRev != "abc123" {
		t.Errorf("snapshot(%q, %q), static tier in %q at %q", gotCommit, gotGiven, gotDir, gotRev)
	}
	if v.Verdict != verdictstatus.VerdictPass || v.Gate.StaticSnapshot != stubSnapshotDir || v.Trigger.GitRevision != "abc123" {
		t.Errorf("verdict %s, snapshot %q, revision %q; want PASS on the snapshot at abc123 (unverified %v)", v.Verdict, v.Gate.StaticSnapshot, v.Trigger.GitRevision, v.Gate.Unverified)
	}

	v = g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test", commit: "fff999"})
	if v.Verdict != verdictstatus.VerdictUnverified || !strings.Contains(strings.Join(v.Gate.Unverified, "\n"), "resolved it to fff999") {
		t.Errorf("a HEAD other than the resolved commit: verdict %s, unverified %v", v.Verdict, v.Gate.Unverified)
	}
}

// TestGateFailsTheStaticTierWhenItsSnapshotIsNotReady checks that a
// snapshot that cannot be made fails the static tier with the reason and
// runs no static check, and that every other tier still runs.
func TestGateFailsTheStaticTierWhenItsSnapshotIsNotReady(t *testing.T) {
	g := stubGateRunner()
	g.snapshot = func(string, string) (staticSnapshot, error) {
		return staticSnapshot{}, errors.New("the disk is full")
	}
	ran := false
	g.static = func(string, string, string) (string, string, *tierResult) {
		ran = true
		return verdictstatus.Pass, "", nil
	}
	v := runStubGate(g)
	if ran {
		t.Error("the static tier ran without its snapshot")
	}
	if st := v.Tiers[tierStatic]; st.Status != verdictstatus.Fail || !strings.Contains(st.Reason+st.Detail, "the disk is full") {
		t.Errorf("static tier %+v; want a failure naming the reason", st)
	}
	if v.Verdict != verdictstatus.VerdictFail || v.Tiers[tierUnit].Status != verdictstatus.Pass || v.Tiers[tierDocs].Status != verdictstatus.Pass {
		t.Errorf("verdict %s, tiers %+v; want FAIL with the other tiers run", v.Verdict, v.Tiers)
	}
}

// TestGateIsUnverifiedWhenItsSnapshotIsWritable checks that a snapshot the
// gate's own user can write keeps the verdict from PASS, and that a run
// restricted to tiers without the static tier makes no snapshot.
func TestGateIsUnverifiedWhenItsSnapshotIsWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("blocked: root can write any directory, so the check reads every directory as writable")
	}
	g := stubGateRunner()
	writable := t.TempDir()
	calls := 0
	g.snapshot = func(string, string) (staticSnapshot, error) {
		calls++
		return staticSnapshot{dir: writable, cleanup: func() {}}, nil
	}
	v := runStubGate(g)
	if v.Verdict != verdictstatus.VerdictUnverified || !strings.Contains(strings.Join(v.Gate.Unverified, "\n"), "writable by the gate's user") {
		t.Errorf("a writable snapshot: verdict %s, unverified %v", v.Verdict, v.Gate.Unverified)
	}
	g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test", tiers: []string{tierUnit}})
	if calls != 1 {
		t.Errorf("snapshot made %d time(s); want once, for the run that runs the static tier", calls)
	}
}

// TestGateContainerGetsTheSnapshotPath checks that the gate's container
// receives the snapshot's path, which is the same for every run of a
// worktree and differs between worktrees, and that `run --container`
// receives none.
func TestGateContainerGetsTheSnapshotPath(t *testing.T) {
	a, b := gateSnapshotPath("/w/a"), gateSnapshotPath("/w/b")
	if a == b || a != gateSnapshotPath("/w/a") || !pathWithin(a, gateSnapshotParent) {
		t.Errorf("snapshot paths %s and %s; want one stable path per worktree under %s", a, b, gateSnapshotParent)
	}
	args := strings.Join(gateDockerRunArgs(gateHost{root: "/w/a", image: "lenny-gate:x", snapshot: a}), " ")
	if !strings.Contains(args, "-e "+gateEnvSnapshot+"="+a) {
		t.Errorf("the gate container's args lack %s:\n%s", gateEnvSnapshot, args)
	}
	args = strings.Join(gateDockerRunArgs(gateHost{root: "/w/a", image: "lenny-gate:x"}), " ")
	if strings.Contains(args, "-e "+gateEnvSnapshot+"=") {
		t.Errorf("a container without a snapshot gets %s:\n%s", gateEnvSnapshot, args)
	}
}

// TestSecretScanReadsTheCommitItIsGiven checks that the secret scan's
// listing of the committed tree reads the revision it is given rather than
// HEAD, and refuses a revision git would read as an option.
func TestSecretScanReadsTheCommitItIsGiven(t *testing.T) {
	r, head := snapshotFixture(t)
	earlier := r.git("rev-parse", "main")
	for rev, want := range map[string]bool{earlier: false, head: true} {
		blobs, err := committedTree(r.root, rev)
		if err != nil {
			t.Fatal(err)
		}
		has := false
		for _, b := range blobs {
			has = has || b.path == "b.go"
		}
		if has != want {
			t.Errorf("the tree of %s lists b.go: %v; want %v", rev, has, want)
		}
	}
	if _, err := committedTree(r.root, "--output=x"); err == nil {
		t.Error("a revision that begins with a dash was accepted")
	}
}

// TestStaticChecksRunInTheCheckoutTheyAreGiven checks that the static
// tier's shell checks and Go test checks run in the checkout they are
// given rather than the current directory.
func TestStaticChecksRunInTheCheckoutTheyAreGiven(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("blocked: bash is not on PATH")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "pwd.sh")
	if err := os.WriteFile(script, []byte("pwd -P\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := bashScript(dir, script)
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Errorf("bashScript ran in %q (%v); want %s", out, err, want)
	}
	for _, c := range staticPackageChecks(dir) {
		if _, err := c.run(); err == nil {
			t.Errorf("%s passed in an empty directory, so it did not run there", c.name)
		}
	}
}
