// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

// commit writes path and commits it.
func (r lintRepo) commit(path, body, msg string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.root, path), []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", path)
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// TestLintBaseIsTheMergeBaseWithMainOnABranch checks that on a branch the
// changed-line lint compares with the merge base with main, not with
// main's tip, so a commit that lands on main after the branch point does
// not count as the branch's change.
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
			t.Errorf("base %q resolved to %+v; want the fork point %s against main, not at HEAD", base, b, fork)
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

// fakeLint records the golangci-lint invocations of one check run and
// answers each from its table: the changed-line run is the one that
// carries --new-from-rev.
type fakeLint struct {
	calls      [][]string
	changedOut string
	changedErr error
	wholeOut   string
	wholeErr   error
}

func (f *fakeLint) exec(_ context.Context, _, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	for _, a := range args {
		if strings.HasPrefix(a, "--new-from-rev=") {
			return f.changedOut, f.changedErr
		}
	}
	return f.wholeOut, f.wholeErr
}

// exitError returns an *exec.ExitError with the given status, as
// golangci-lint returns when it reports findings.
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
// fails with resolveErr.
func stubLint(f *fakeLint, b lintBase, resolveErr error) golangciLint {
	return golangciLint{
		root: "/repo",
		base: "main",
		bin:  "golangci-lint",
		resolve: func(context.Context, string, string) (lintBase, error) {
			return b, resolveErr
		},
		exec: f.exec,
	}
}

// runLintCheck runs the check through the static tier's classifier and
// reducer and returns the tier status and the check's output.
func runLintCheck(l golangciLint) (string, string) {
	o := runStaticCheck(staticCheck{name: "golangci-lint run", run: l.run, classify: classifyUnverified})
	status, _ := reduceStaticOutcomes([]staticOutcome{o})
	return status, o.out
}

var testLintBase = lintBase{commit: "0123456789abcdef0123", ref: "main"}

// TestLintPassesWithNoFindingOnChangedLines checks the pass outcome: no
// finding anywhere passes the tier, and the changed-line run compares
// with the resolved merge base.
func TestLintPassesWithNoFindingOnChangedLines(t *testing.T) {
	f := &fakeLint{}
	status, out := runLintCheck(stubLint(f, testLintBase, nil))
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass (output %q)", status, out)
	}
	if len(f.calls) != 2 {
		t.Fatalf("golangci-lint ran %d times; want the changed-line run and the whole-tree run", len(f.calls))
	}
	if !containsArg(f.calls[0], "--new-from-rev="+testLintBase.commit) {
		t.Errorf("changed-line run args %v; want --new-from-rev at the merge base", f.calls[0])
	}
	for _, a := range f.calls[1] {
		if strings.HasPrefix(a, "--new") {
			t.Errorf("whole-tree run carries %s", a)
		}
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
	for _, want := range []string{"pkg/a/a.go:3:2", "pkg/b/b.go:9:1", "WARNING (non-fatal"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestLintFailsWhenTheChangedLineRunCannotRun checks that a changed-line
// run that fails to start, or exits on a timeout or a configuration
// error, fails the tier rather than passing it.
func TestLintFailsWhenTheChangedLineRunCannotRun(t *testing.T) {
	for name, err := range map[string]error{
		"not started": exec.ErrNotFound,
		"timeout":     exitError(t, 4),
	} {
		f := &fakeLint{changedOut: "level=error msg=\"Timeout exceeded\"", changedErr: err}
		if status, out := runLintCheck(stubLint(f, testLintBase, nil)); status != verdictstatus.Fail {
			t.Errorf("%s: status %q; want fail (output %q)", name, status, out)
		}
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
	if len(f.calls) != 1 || containsArg(f.calls[0], "--new-from-rev=") {
		t.Errorf("calls %v; want the whole-tree run only", f.calls)
	}
	if !strings.Contains(out, "merge base could not be resolved") || !strings.Contains(out, "pkg/b/b.go:9:1") {
		t.Errorf("output lacks the reason or the whole-tree findings:\n%s", out)
	}
}

// TestLintSkipsWithoutTheLinter checks that a host without golangci-lint
// runs nothing and says so.
func TestLintSkipsWithoutTheLinter(t *testing.T) {
	f := &fakeLint{}
	l := stubLint(f, testLintBase, nil)
	l.bin = ""
	out, err := l.run()
	if err != nil || !strings.Contains(out, "golangci-lint not found") || len(f.calls) != 0 {
		t.Errorf("run without the linter: %q, %v, calls %v", out, err, f.calls)
	}
}

// TestGatePassesItsBaseToTheStaticTier checks that the merge gate's
// --base reaches the static tier, which the changed-line lint compares
// with.
func TestGatePassesItsBaseToTheStaticTier(t *testing.T) {
	g := stubGateRunner()
	var got string
	g.static = func(base string) (string, string, *tierResult) {
		got = base
		return verdictstatus.Pass, "", &tierResult{Total: 1, Passed: 1}
	}
	g.run(gateOptions{root: ".", base: "release-1", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test"})
	if got != "release-1" {
		t.Errorf("static tier got base %q; want release-1", got)
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

// TestLintWithTheInstalledLinterSeparatesChangedLinesFromTheRest runs the
// installed golangci-lint over a module in a temporary repository. A
// branch that adds a finding fails with that finding named in the
// changed-line section, and a branch whose changed lines are clean passes
// with the pre-existing finding as a warning. It pins the new-code
// filtering of the pinned golangci-lint release rather than a fake. On
// macOS the temporary directory is reached through the /var symbolic
// link, so the case also pins the resolution of the root in
// newGolangciLint, without which the new finding reads as old.
func TestLintWithTheInstalledLinterSeparatesChangedLinesFromTheRest(t *testing.T) {
	bin := resolveGoBin("golangci-lint")
	if bin == "" {
		t.Skip("blocked: golangci-lint is not installed (run scripts/setup-dev.sh)")
	}
	r := newLintRepo(t, "main")
	r.commit("go.mod", "module example.com/m\n\ngo 1.22\n", "module")
	r.commit("old.go", lintModuleBase, "pre-existing finding")
	r.git("checkout", "-q", "-b", "feature")
	t.Setenv("GOLANGCI_LINT_CACHE", t.TempDir())
	t.Setenv("GOFLAGS", "-buildvcs=false")
	l := newGolangciLint(r.root, "main")

	r.commit("clean.go", "package m\n\n// Clean returns one.\nfunc Clean() int { return 1 }\n", "clean change")
	out, err := l.run()
	if err != nil {
		t.Fatalf("clean changed lines failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "old.go:6") || !strings.Contains(out, "WARNING (non-fatal") {
		t.Errorf("the pre-existing finding is not a warning:\n%s", out)
	}

	r.commit("new.go", "package m\n\nimport \"os\"\n\n// New removes a file.\nfunc New() {\n\tos.Remove(\"new\")\n}\n", "new finding")
	out, err = l.run()
	if err == nil {
		t.Fatalf("a finding on a changed line passed:\n%s", out)
	}
	changed, _, _ := strings.Cut(out, "WARNING (non-fatal")
	if !strings.Contains(changed, "new.go:7") || strings.Contains(changed, "old.go") {
		t.Errorf("the changed-line section does not name exactly the new finding:\n%s", changed)
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
