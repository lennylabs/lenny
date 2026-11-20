// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// The golangci-lint check is contributor tooling (the merge gate), so
// these cases carry no specification annotation.

// lintRepo is a git repository in a temporary directory.
type lintRepo struct {
	t    *testing.T
	root string
}

// newLintRepo initializes a repository whose first commit is on the
// branch named initial.
func newLintRepo(t *testing.T, initial string) lintRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("blocked: git is not on PATH")
	}
	r := lintRepo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", initial)
	r.commit("a.go", "package m\n", "base")
	return r
}

// git runs git in the repository and returns its trimmed output.
func (r lintRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=alice@acme.com", "-c", "user.name=alice", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = r.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// write writes path in the working tree without committing it.
func (r lintRepo) write(path, body string) {
	r.t.Helper()
	full := filepath.Join(r.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// commit writes path and commits it.
func (r lintRepo) commit(path, body, msg string) string {
	r.t.Helper()
	r.write(path, body)
	r.git("add", path)
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// TestLintBaseIsTheMergeBaseWithMainOnABranch checks that on a branch the
// changed-line lint compares with the merge base with main rather than
// with main's tip, so a commit that lands on main after the branch point
// does not count as the branch's change.
func TestLintBaseIsTheMergeBaseWithMainOnABranch(t *testing.T) {
	r := newLintRepo(t, "main")
	fork := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "-b", "feature")
	r.commit("b.go", "package m\n", "branch")
	r.git("checkout", "-q", "main")
	r.commit("c.go", "package m\n", "main moves on")
	r.git("checkout", "-q", "feature")

	ctx := context.Background()
	for _, base := range []string{"", "main"} {
		b, err := resolveLintBase(ctx, r.root, base)
		if err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
		if b.commit != fork || b.ref != "main" || b.atHead {
			t.Errorf("base %q resolved to %+v; want the fork point %s against main, before HEAD", base, b, fork)
		}
	}
}

// TestLintBaseFallsBackToOriginMain checks that a clone that carries
// origin/main and no local main resolves the merge base against
// origin/main, as the static tier's other changed-line lints do.
func TestLintBaseFallsBackToOriginMain(t *testing.T) {
	r := newLintRepo(t, "trunk")
	fork := r.git("rev-parse", "HEAD")
	r.git("update-ref", "refs/remotes/origin/main", fork)
	r.commit("b.go", "package m\n", "branch")

	b, err := resolveLintBase(context.Background(), r.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.commit != fork || b.ref != "origin/main" {
		t.Errorf("resolved %+v; want %s against origin/main", b, fork)
	}
}

// TestLintBaseOnMainIsHead checks that on main itself the merge base is
// HEAD, so only uncommitted and untracked lines count as changed.
func TestLintBaseOnMainIsHead(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.commit("b.go", "package m\n", "second")

	b, err := resolveLintBase(context.Background(), r.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if b.commit != head || !b.atHead {
		t.Errorf("resolved %+v; want HEAD %s marked atHead", b, head)
	}
}

// TestLintBaseFailsWithoutAnIntegrationRef checks that a clone that
// carries neither main nor origin/main, or a --base that names no
// commit, reports an error rather than an empty base.
func TestLintBaseFailsWithoutAnIntegrationRef(t *testing.T) {
	r := newLintRepo(t, "trunk")
	ctx := context.Background()
	if _, err := resolveLintBase(ctx, r.root, ""); !errors.Is(err, annotation.ErrNoBase) {
		t.Errorf("no integration ref: error %v; want ErrNoBase", err)
	}
	if _, err := resolveLintBase(ctx, r.root, "main"); err == nil {
		t.Error("--base main with no main ref resolved")
	}
	if _, err := resolveLintBase(ctx, r.root, "--output=x"); err == nil {
		t.Error("a --base that begins with '-' resolved")
	}
}

// TestLintBaseAcceptsARangeOnlyWhenItEndsAtHead checks that a range
// --base resolves when its head side is HEAD and fails otherwise, because
// the linter reads the working tree and cannot compare with another
// commit.
func TestLintBaseAcceptsARangeOnlyWhenItEndsAtHead(t *testing.T) {
	r := newLintRepo(t, "main")
	first := r.git("rev-parse", "HEAD")
	r.commit("b.go", "package m\n", "second")
	r.commit("c.go", "package m\n", "third")
	ctx := context.Background()

	b, err := resolveLintBase(ctx, r.root, first+"..HEAD")
	if err != nil || b.commit != first || b.atHead {
		t.Errorf("range ending at HEAD resolved to %+v, %v; want %s", b, err, first)
	}
	if _, err := resolveLintBase(ctx, r.root, first+"..HEAD~1"); err == nil {
		t.Error("a range ending before HEAD resolved")
	}
}

// TestLintPatchCoversCommittedUncommittedAndUntrackedLines checks that the
// diff the changed-line run reads holds a committed line, an uncommitted
// line, and every line of an untracked file, under the `+++ b/<path>`
// headers golangci-lint matches, even when the repository sets
// diff.noprefix and a .gitattributes entry marks Go files -diff.
func TestLintPatchCoversCommittedUncommittedAndUntrackedLines(t *testing.T) {
	r := newLintRepo(t, "main")
	base := r.git("rev-parse", "HEAD")
	r.git("config", "diff.noprefix", "true")
	r.git("config", "diff.mnemonicPrefix", "true")
	r.commit(".gitattributes", "*.go -diff\n", "hide Go diffs")
	r.commit("p/committed.go", "package p\n\nvar Committed = 1\n", "committed")
	r.write("a.go", "package m\n\nvar Uncommitted = 1\n")
	r.write("q/untracked.go", "package q\n\nvar Untracked = 1\n")

	patch, err := buildLintPatch(context.Background(), r.root, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"+++ b/p/committed.go\n", "+var Committed = 1\n",
		"+++ b/a.go\n", "+var Uncommitted = 1\n",
		"+++ b/q/untracked.go\n", "@@ -0,0 +1,3 @@\n", "+package q\n", "+var Untracked = 1\n",
	} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch lacks %q:\n%s", want, patch)
		}
	}
	if strings.Contains(patch, "Binary files") {
		t.Errorf("patch reads a Go file as binary:\n%s", patch)
	}
}

// TestLintPatchRefusesPathsTheDiffCannotName checks that a changed Go
// file with a space in its name, or one that is a symbolic link, fails
// the patch rather than dropping out of the changed-line run.
func TestLintPatchRefusesPathsTheDiffCannotName(t *testing.T) {
	r := newLintRepo(t, "main")
	base := r.git("rev-parse", "HEAD")
	r.write("sp ace.go", "package m\n")
	if _, err := buildLintPatch(context.Background(), r.root, base); err == nil || !strings.Contains(err.Error(), "sp ace.go") {
		t.Errorf("a path with a space: error %v", err)
	}
	if err := os.Remove(filepath.Join(r.root, "sp ace.go")); err != nil {
		t.Fatal(err)
	}
	r.write("testdata/impl.go", "package m\n")
	if err := os.Symlink("testdata/impl.go", filepath.Join(r.root, "impl.go")); err != nil {
		t.Fatal(err)
	}
	r.git("add", "impl.go", "testdata/impl.go")
	r.git("commit", "-q", "-m", "link")
	if _, err := buildLintPatch(context.Background(), r.root, base); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a symbolic link: error %v", err)
	}
}

// TestLintEnvDropsThePatchOverrideAndTargetsLinux checks the environment
// of a golangci-lint run: an inherited patch override is dropped, PWD
// names the root, and a host other than Linux analyzes the Linux build.
func TestLintEnvDropsThePatchOverrideAndTargetsLinux(t *testing.T) {
	in := []string{"HOME=/home/alice", "GOLANGCI_DIFF_PROCESSOR_PATCH=x", "PWD=/elsewhere", "GOOS=darwin", "CGO_ENABLED=1"}
	darwin := strings.Join(golangciLintEnv(in, "/repo", "darwin"), "\n")
	for _, want := range []string{"HOME=/home/alice", "PWD=/repo", "GOOS=linux", "CGO_ENABLED=0"} {
		if !strings.Contains(darwin, want) {
			t.Errorf("darwin environment lacks %s:\n%s", want, darwin)
		}
	}
	for _, bad := range []string{"GOLANGCI_DIFF_PROCESSOR_PATCH", "PWD=/elsewhere", "GOOS=darwin", "CGO_ENABLED=1"} {
		if strings.Contains(darwin, bad) {
			t.Errorf("darwin environment keeps %s:\n%s", bad, darwin)
		}
	}
	linux := strings.Join(golangciLintEnv(in, "/repo", "linux"), "\n")
	if !strings.Contains(linux, "GOOS=darwin") || !strings.Contains(linux, "CGO_ENABLED=1") || strings.Contains(linux, "GOOS=linux") {
		t.Errorf("a Linux host changed the build target:\n%s", linux)
	}
}

// TestGolangciLintPinsAgree checks that the gate image installs the
// golangci-lint release that scripts/lib/versions.sh pins, which the
// check compares the installed linter with.
func TestGolangciLintPinsAgree(t *testing.T) {
	root := repoRoot()
	want, err := pinnedGolangciLintVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, gateDockerfile))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`golangci-lint/v2/cmd/golangci-lint@v([0-9.]+)`).FindSubmatch(dockerfile)
	if m == nil || string(m[1]) != want {
		t.Errorf("%s installs golangci-lint %q; %s pins %s", gateDockerfile, m, golangciLintVersionFile, want)
	}
}

// fakeLint records the golangci-lint invocations of one check run and
// answers each from its table: the changed-line run is the one that
// carries --new-from-patch.
type fakeLint struct {
	calls      [][]string
	changedOut string
	changedErr error
	wholeOut   string
	wholeErr   error
}

func (f *fakeLint) exec(_ context.Context, _, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if containsArg(args, "--new-from-patch=") {
		return f.changedOut, f.changedErr
	}
	return f.wholeOut, f.wholeErr
}

// exitError returns an *exec.ExitError with the given status, as
// golangci-lint returns when it exits non-zero.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("sh exit %d returned %v", code, err)
	}
	return err
}

// stubLint returns a check over f whose merge base resolves to b, or
// fails with resolveErr, whose patch is empty, and whose linter is the
// pinned release.
func stubLint(f *fakeLint, b lintBase, resolveErr error) golangciLint {
	return golangciLint{
		root: "/repo",
		base: "main",
		bin:  "golangci-lint",
		resolve: func(context.Context, string, string) (lintBase, error) {
			return b, resolveErr
		},
		patch:   func(context.Context, string, string) (string, error) { return "", nil },
		version: func(context.Context, string, string) error { return nil },
		exec:    f.exec,
	}
}

// runLintCheck runs the check through the static tier's classifier and
// reducer and returns the tier status and the check's output.
func runLintCheck(l golangciLint) (string, string) {
	o := runStaticCheck(staticCheck{name: "golangci-lint run", run: l.run, classify: classifyGolangciLint})
	status, _ := reduceStaticOutcomes([]staticOutcome{o})
	return status, o.out
}

var testLintBase = lintBase{commit: "0123456789abcdef0123", ref: "main"}

// TestLintPassesWithNoFindingOnChangedLines checks the pass outcome: no
// finding anywhere passes the tier, and both runs name the configuration
// file and the exit status for findings on the command line.
func TestLintPassesWithNoFindingOnChangedLines(t *testing.T) {
	f := &fakeLint{}
	status, out := runLintCheck(stubLint(f, testLintBase, nil))
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass (output %q)", status, out)
	}
	if len(f.calls) != 2 {
		t.Fatalf("golangci-lint ran %d times; want the changed-line run and the whole-tree run", len(f.calls))
	}
	if !containsArg(f.calls[0], "--new-from-patch=") {
		t.Errorf("changed-line run args %v; want --new-from-patch", f.calls[0])
	}
	for _, call := range f.calls {
		if !containsArg(call, "--config=/repo/.golangci.yml") || !containsArg(call, "--issues-exit-code=1") {
			t.Errorf("run args %v do not pin the configuration file and the exit status", call)
		}
	}
	if containsArg(f.calls[1], "--new") {
		t.Errorf("whole-tree run carries a new-code flag: %v", f.calls[1])
	}
}

// TestLintFailsOnAFindingOnAChangedLine checks the fail outcome: a
// finding the changed-line run reports fails the tier and names the
// finding, and the whole-tree run still reports.
func TestLintFailsOnAFindingOnAChangedLine(t *testing.T) {
	f := &fakeLint{
		changedOut: "pkg/a/a.go:3:2: Error return value is not checked (errcheck)",
		changedErr: exitError(t, 1),
		wholeOut:   "pkg/b/b.go:9:1: func old is unused (unused)",
		wholeErr:   exitError(t, 1),
	}
	status, out := runLintCheck(stubLint(f, testLintBase, nil))
	if status != verdictstatus.Fail {
		t.Fatalf("status %q; want fail (output %q)", status, out)
	}
	for _, want := range []string{"reports findings on", "pkg/a/a.go:3:2", "pkg/b/b.go:9:1", "WARNING (non-fatal"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestLintFailsWhenTheChangedLineRunCannotRun checks that a changed-line
// run that fails to start, exits on a timeout or a configuration error,
// or has no patch fails the tier rather than passing it, and that the
// message does not call it findings.
func TestLintFailsWhenTheChangedLineRunCannotRun(t *testing.T) {
	for name, err := range map[string]error{
		"not started": exec.ErrNotFound,
		"timeout":     exitError(t, 4),
		"config":      exitError(t, 3),
	} {
		f := &fakeLint{changedOut: "level=error msg=\"Timeout exceeded\"", changedErr: err}
		status, out := runLintCheck(stubLint(f, testLintBase, nil))
		if status != verdictstatus.Fail || !strings.Contains(out, "did not finish") || strings.Contains(out, "reports findings") {
			t.Errorf("%s: status %q; want fail naming an unfinished run (output %q)", name, status, out)
		}
	}
	f := &fakeLint{}
	l := stubLint(f, testLintBase, nil)
	l.patch = func(context.Context, string, string) (string, error) {
		return "", errors.New("changed Go files the changed-line lint cannot read: impl.go: the file is a symbolic link")
	}
	if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "symbolic link") {
		t.Errorf("a refused patch: status %q; want fail (output %q)", status, out)
	}
}

// TestLintWarnsOnFindingsOutsideTheChangedLines checks the warning
// outcome: findings only in the whole-tree run pass the tier and are
// reported with the issue that tracks them.
func TestLintWarnsOnFindingsOutsideTheChangedLines(t *testing.T) {
	f := &fakeLint{wholeOut: "pkg/b/b.go:9:1: func old is unused (unused)", wholeErr: exitError(t, 1)}
	status, out := runLintCheck(stubLint(f, testLintBase, nil))
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass (output %q)", status, out)
	}
	for _, want := range []string{"WARNING (non-fatal", lintDebtIssue, "pkg/b/b.go:9:1", "no finding on the lines changed since the merge base 0123456789ab with main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestLintMarkerInLinterOutputDoesNotChangeTheStatus checks that the
// unverified marker inside a source line golangci-lint echoes leaves the
// tier at pass, because only the check's own first line carries its
// conclusion.
func TestLintMarkerInLinterOutputDoesNotChangeTheStatus(t *testing.T) {
	f := &fakeLint{wholeOut: "cmd/x/x.go:9:1: unused\n\tconst m = \"" + verdictstatus.UnverifiedMarker + " echoed\"\n", wholeErr: exitError(t, 1)}
	if status, out := runLintCheck(stubLint(f, testLintBase, nil)); status != verdictstatus.Pass {
		t.Errorf("status %q; want pass (output %q)", status, out)
	}
}

// TestLintOnTheMergeBaseNamesTheUncommittedScope checks that a run whose
// merge base is HEAD says that only uncommitted and untracked lines were
// checked.
func TestLintOnTheMergeBaseNamesTheUncommittedScope(t *testing.T) {
	f := &fakeLint{}
	b := testLintBase
	b.atHead = true
	_, out := runLintCheck(stubLint(f, b, nil))
	if !strings.Contains(out, "uncommitted and untracked lines, because HEAD is the merge base") {
		t.Errorf("output does not name the uncommitted scope:\n%s", out)
	}
}

// TestLintIsUnverifiedWhenTheBaseCannotBeResolved checks that a run with
// no resolvable merge base does not pass: the tier reports unverified,
// names the reason, skips the changed-line run, and still reports the
// whole-tree findings.
func TestLintIsUnverifiedWhenTheBaseCannotBeResolved(t *testing.T) {
	f := &fakeLint{wholeOut: "pkg/b/b.go:9:1: func old is unused (unused)", wholeErr: exitError(t, 1)}
	status, out := runLintCheck(stubLint(f, lintBase{}, annotation.ErrNoBase))
	if status != verdictstatus.Unverified {
		t.Fatalf("status %q; want unverified (output %q)", status, out)
	}
	if len(f.calls) != 1 || containsArg(f.calls[0], "--new-from-patch=") {
		t.Errorf("calls %v; want the whole-tree run only", f.calls)
	}
	if !strings.Contains(out, "merge base could not be resolved") || !strings.Contains(out, "pkg/b/b.go:9:1") {
		t.Errorf("output lacks the reason or the whole-tree findings:\n%s", out)
	}
}

// TestLintIsUnverifiedWithAnotherLinterRelease checks that a linter other
// than the pinned release leaves a clean run unverified and a finding on
// a changed line failing.
func TestLintIsUnverifiedWithAnotherLinterRelease(t *testing.T) {
	f := &fakeLint{}
	l := stubLint(f, testLintBase, nil)
	l.version = func(context.Context, string, string) error { return errors.New("golangci-lint 2.1.0 is installed") }
	if status, out := runLintCheck(l); status != verdictstatus.Unverified || !strings.Contains(out, "2.1.0") {
		t.Errorf("clean run with another release: status %q (output %q)", status, out)
	}
	f.changedErr = exitError(t, 1)
	if status, _ := runLintCheck(l); status != verdictstatus.Fail {
		t.Errorf("finding with another release: status %q; want fail", status)
	}
}

// TestLintIsUnverifiedWithoutTheLinter checks that a host without
// golangci-lint runs nothing and reports unverified rather than pass.
func TestLintIsUnverifiedWithoutTheLinter(t *testing.T) {
	f := &fakeLint{}
	l := stubLint(f, testLintBase, nil)
	l.bin = ""
	status, out := runLintCheck(l)
	if status != verdictstatus.Unverified || !strings.Contains(out, "golangci-lint not found") || len(f.calls) != 0 {
		t.Errorf("run without the linter: status %q, output %q, calls %v", status, out, f.calls)
	}
}

// lintModuleBase is a Go file with one errcheck finding, on line 6, that
// predates the branch.
const lintModuleBase = `package m

import "os"

func Old() {
	os.Remove("old")
}
`

// lintFinding returns a Go file whose errcheck finding is on line 7.
func lintFinding(name string) string {
	return "package m\n\nimport \"os\"\n\n// " + name + " removes a file.\nfunc " + name + "() {\n\tos.Remove(\"" + name + "\")\n}\n"
}

// TestLintWithTheInstalledLinterSeparatesChangedLinesFromTheRest runs the
// installed golangci-lint over a module in a temporary repository. A
// branch whose changed lines are clean passes with the pre-existing
// finding as a warning, and a finding in a committed, an uncommitted, or
// an untracked change fails with that finding named in the changed-line
// section. It pins the new-code filtering of the pinned golangci-lint
// release rather than a fake. On macOS the temporary directory is reached
// through the /var symbolic link, so the case also pins the resolution of
// the root in newGolangciLint, without which a new finding reads as old.
func TestLintWithTheInstalledLinterSeparatesChangedLinesFromTheRest(t *testing.T) {
	if resolveGoBin("golangci-lint") == "" {
		t.Skip("blocked: golangci-lint is not installed (run scripts/setup-dev.sh)")
	}
	r := newLintRepo(t, "main")
	r.commit("go.mod", "module example.com/m\n\ngo 1.22\n", "module")
	r.commit(".golangci.yml", "version: \"2\"\n", "config")
	r.commit("old.go", lintModuleBase, "pre-existing finding")
	r.git("checkout", "-q", "-b", "feature")
	t.Setenv("GOLANGCI_LINT_CACHE", t.TempDir())
	t.Setenv("GOFLAGS", "-buildvcs=false")
	l := newGolangciLint(r.root, "main")
	l.version = func(context.Context, string, string) error { return nil }

	r.commit("clean.go", "package m\n\n// Clean returns one.\nfunc Clean() int { return 1 }\n", "clean change")
	out, err := l.run()
	if err != nil {
		t.Fatalf("clean changed lines failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "old.go:6") || !strings.Contains(out, "WARNING (non-fatal") {
		t.Errorf("the pre-existing finding is not a warning:\n%s", out)
	}

	cases := []struct {
		name, file string
		apply      func()
		undo       func()
	}{
		{
			"committed", "committed.go",
			func() { r.commit("committed.go", lintFinding("Committed"), "finding") },
			func() { r.git("reset", "-q", "--hard", "HEAD~1") },
		},
		{
			"uncommitted", "clean.go",
			func() { r.write("clean.go", lintFinding("Uncommitted")) },
			func() { r.git("checkout", "-q", "--", "clean.go") },
		},
		{
			"untracked", "untracked.go",
			func() { r.write("untracked.go", lintFinding("Untracked")) },
			func() { _ = os.Remove(filepath.Join(r.root, "untracked.go")) },
		},
	}
	for _, c := range cases {
		c.apply()
		out, err = l.run()
		c.undo()
		if err == nil {
			t.Errorf("%s: a finding on a changed line passed:\n%s", c.name, out)
			continue
		}
		changed, _, _ := strings.Cut(out, "WARNING (non-fatal")
		if !strings.Contains(changed, c.file+":7") || strings.Contains(changed, "old.go") {
			t.Errorf("%s: the changed-line section does not name exactly the new finding:\n%s", c.name, changed)
		}
	}
}

// containsArg reports whether args holds an argument with the prefix.
func containsArg(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
