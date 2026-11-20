// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
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
	darwin := strings.Join(golangciLintEnv(in, "/repo", "darwin", "/cache"), "\n")
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
	linux := strings.Join(golangciLintEnv(in, "/repo", "linux", "/cache"), "\n")
	if !strings.Contains(linux, "GOOS=darwin") || !strings.Contains(linux, "CGO_ENABLED=1") || strings.Contains(linux, "GOOS=linux") {
		t.Errorf("a Linux host changed the build target:\n%s", linux)
	}
}

// TestLintRunsSetEveryTierBuildTag checks that every golangci-lint run
// sets the build tag of every tier and the smoke tag, so the tagged test
// files are read, and never sets the ignore tag.
func TestLintRunsSetEveryTierBuildTag(t *testing.T) {
	var tags string
	for _, a := range golangciLintArgs("/repo") {
		if v, ok := strings.CutPrefix(a, "--build-tags="); ok {
			tags = v
		}
	}
	set := map[string]bool{}
	for _, tag := range strings.Split(tags, ",") {
		set[tag] = true
	}
	for _, want := range append(tierBuildTags(), "integration", "component", "contract", "smoke") {
		if !set[want] {
			t.Errorf("--build-tags=%s lacks %s", tags, want)
		}
	}
	if set["ignore"] || set[""] {
		t.Errorf("--build-tags=%s sets ignore or an empty tag", tags)
	}
}

// TestLintSourceScanPassesOnTheTree checks that the tree carries no
// construct the source scan refuses, so the static tier's check passes on
// main.
func TestLintSourceScanPassesOnTheTree(t *testing.T) {
	found, err := scanLintSources(context.Background(), repoRoot())
	if err != nil {
		t.Skipf("blocked: the source scan cannot list the tree: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("the source scan refuses:\n  %s", strings.Join(found, "\n  "))
	}
}

// TestLintSourceScanRefusesBuildTagsTheLintDoesNotRead checks that a
// build tag the lint does not set and a negated tag it does set are
// refused, that operating-system tags and their negations pass, and that
// a constraint quoted after the package clause is not read.
func TestLintSourceScanRefusesBuildTagsTheLintDoesNotRead(t *testing.T) {
	tags := map[string]bool{}
	for _, tag := range lintBuildTags() {
		tags[tag] = true
	}
	for name, c := range map[string]struct {
		src  string
		want string
	}{
		"unknown tag":       {"//go:build newtag\n\npackage m\n", "build tag newtag is not one the lint sets"},
		"negated tier tag":  {"//go:build !integration\n\npackage m\n", "negates integration"},
		"negated compound":  {"//go:build !(component && contract)\n\npackage m\n", "negates component"},
		"negated smoke":     {"//go:build linux && !smoke\n\npackage m\n", "negates smoke"},
		"double negation":   {"//go:build !(!integration)\n\npackage m\n", ""},
		"tier tag":          {"//go:build integration\n\npackage m\n", ""},
		"negated os":        {"//go:build !linux\n\npackage m\n", ""},
		"tier and os":       {"//go:build integration && !linux\n\npackage m\n", ""},
		"quoted constraint": {"package m\n\nconst s = `\n//go:build newtag\n`\n", ""},
	} {
		got := strings.Join(buildTagProblems("m.go", []byte(c.src), tags), "\n")
		if c.want == "" && got != "" {
			t.Errorf("%s: refused %q", name, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: problems %q; want %q", name, got, c.want)
		}
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
// carries --new-from-patch. The whole-tree run writes wholeJSON, or a
// report with no finding when it is empty, to the path its
// --output.json.path argument names.
type fakeLint struct {
	calls      [][]string
	changedOut string
	changedErr error
	wholeJSON  string
	wholeOut   string
	wholeErr   error
}

func (f *fakeLint) exec(_ context.Context, _, _ string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if containsArg(args, "--new-from-patch=") {
		return f.changedOut, f.changedErr
	}
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "--output.json.path="); ok {
			body := f.wholeJSON
			if body == "" {
				body = `{"Issues":null}`
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				return "", err
			}
		}
	}
	return f.wholeOut, f.wholeErr
}

// lintJSON returns a golangci-lint JSON report holding one finding per
// entry, each written as "<file>:<line>:<linter>".
func lintJSON(t *testing.T, findings ...string) string {
	t.Helper()
	type pos struct {
		Filename string
		Line     int
		Column   int
	}
	type issue struct {
		FromLinter string
		Text       string
		Pos        pos
	}
	var issues []issue
	for _, f := range findings {
		parts := strings.Split(f, ":")
		if len(parts) != 3 {
			t.Fatalf("finding %q is not <file>:<line>:<linter>", f)
		}
		line, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue{FromLinter: parts[2], Text: "finding in " + parts[0], Pos: pos{Filename: parts[0], Line: line, Column: 1}})
	}
	body, err := json.Marshal(map[string]any{"Issues": issues})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// writeTestLintRegister writes the register under root with the counts
// given as "<package> <linter>" keys.
func writeTestLintRegister(t *testing.T, root string, counts map[string]int) {
	t.Helper()
	m := map[lintKey]int{}
	for k, n := range counts {
		pkg, linter, _ := strings.Cut(k, " ")
		m[lintKey{pkg: pkg, linter: linter}] = n
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(golangciLintRegister)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLintRegister(filepath.Join(root, golangciLintRegister), m); err != nil {
		t.Fatal(err)
	}
}

// readTestLintRegister returns the register under root as
// "<package> <linter>" keys.
func readTestLintRegister(t *testing.T, root string) map[string]int {
	t.Helper()
	m, err := loadLintRegister(filepath.Join(root, golangciLintRegister))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for k, n := range m {
		out[k.String()] = n
	}
	return out
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

// stubLint returns a check over f in a temporary root whose register
// carries the single count `pkg/b unused: 1`, whose merge base resolves
// to b, or fails with resolveErr, whose patch is empty, and whose linter
// is the pinned release.
func stubLint(t *testing.T, f *fakeLint, b lintBase, resolveErr error) golangciLint {
	t.Helper()
	root := t.TempDir()
	writeTestLintRegister(t, root, map[string]int{"pkg/b unused": 1})
	return golangciLint{
		root: root,
		base: "main",
		bin:  "golangci-lint",
		resolve: func(context.Context, string, string) (lintBase, error) {
			return b, resolveErr
		},
		patch:   func(context.Context, string, string) (string, error) { return "", nil },
		version: func(context.Context, string, string) error { return nil },
		exec:    f.exec,
		scan:    func(context.Context, string) ([]string, error) { return nil, nil },
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
// finding on a changed line and a whole-tree count that matches the
// register pass the tier, both runs name the configuration file and the
// exit status for findings on the command line, and the whole-tree run
// lifts golangci-lint's caps and writes its JSON report.
func TestLintPassesWithNoFindingOnChangedLines(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	status, out := runLintCheck(l)
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
		if !containsArg(call, "--config="+filepath.Join(l.root, ".golangci.yml")) || !containsArg(call, "--issues-exit-code=1") {
			t.Errorf("run args %v do not pin the configuration file and the exit status", call)
		}
	}
	whole := f.calls[1]
	if containsArg(whole, "--new") {
		t.Errorf("whole-tree run carries a new-code flag: %v", whole)
	}
	for _, want := range []string{"--max-issues-per-linter=0", "--max-same-issues=0", "--output.json.path="} {
		if !containsArg(whole, want) {
			t.Errorf("whole-tree run args %v lack %s", whole, want)
		}
	}
	if !strings.Contains(out, "Every count matches "+golangciLintRegister) {
		t.Errorf("output does not report the held register:\n%s", out)
	}
}

// TestLintFailsOnAFindingOnAChangedLine checks the fail outcome: a
// finding the changed-line run reports fails the tier and names the
// finding, and the whole-tree run still reports.
func TestLintFailsOnAFindingOnAChangedLine(t *testing.T) {
	f := &fakeLint{
		changedOut: "pkg/a/a.go:3:2: Error return value is not checked (errcheck)",
		changedErr: exitError(t, 1),
		wholeJSON:  lintJSON(t, "pkg/b/b.go:9:unused"),
		wholeErr:   exitError(t, 1),
	}
	status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
	if status != verdictstatus.Fail {
		t.Fatalf("status %q; want fail (output %q)", status, out)
	}
	for _, want := range []string{"reports findings on", "pkg/a/a.go:3:2", "WARNING (non-fatal", "1 finding(s) in the whole tree: unused 1"} {
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
		f := &fakeLint{changedOut: "level=error msg=\"Timeout exceeded\"", changedErr: err, wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused")}
		status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
		if status != verdictstatus.Fail || !strings.Contains(out, "did not finish") || strings.Contains(out, "reports findings") {
			t.Errorf("%s: status %q; want fail naming an unfinished run (output %q)", name, status, out)
		}
	}
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused")}
	l := stubLint(t, f, testLintBase, nil)
	l.patch = func(context.Context, string, string) (string, error) {
		return "", errors.New("changed Go files the changed-line lint cannot read: impl.go: the file is a symbolic link")
	}
	if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "symbolic link") {
		t.Errorf("a refused patch: status %q; want fail (output %q)", status, out)
	}
}

// TestLintWarnsOnFindingsOutsideTheChangedLines checks the warning
// outcome: whole-tree findings within the register pass the tier and are
// reported with the issue that tracks them.
func TestLintWarnsOnFindingsOutsideTheChangedLines(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
	if status != verdictstatus.Pass {
		t.Fatalf("status %q; want pass (output %q)", status, out)
	}
	for _, want := range []string{"WARNING (non-fatal", lintDebtIssue, "unused 1", "no finding on the lines changed since the merge base 0123456789ab with main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestLintFailsWhenAWholeTreeCountRises checks that a finding off the
// changed lines that raises a registered count fails the tier, names the
// count and the findings behind it, and leaves the register as it was.
func TestLintFailsWhenAWholeTreeCountRises(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused", "pkg/b/c.go:4:unused"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	status, out := runLintCheck(l)
	if status != verdictstatus.Fail {
		t.Fatalf("status %q; want fail (output %q)", status, out)
	}
	for _, want := range []string{"pkg/b unused: 2 finding(s) where " + golangciLintRegister + " carries 1", "pkg/b/b.go:9:1", "pkg/b/c.go:4:1", "rather than raising the counts"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := readTestLintRegister(t, l.root); len(got) != 1 || got["pkg/b unused"] != 1 {
		t.Errorf("register %v; want it unchanged", got)
	}
}

// TestLintFailsOnAFindingTheRegisterDoesNotCarry checks that a finding in
// a package, or from a linter, that the register carries no count for
// fails the tier.
func TestLintFailsOnAFindingTheRegisterDoesNotCarry(t *testing.T) {
	for name, finding := range map[string]string{
		"package": "pkg/c/c.go:2:unused",
		"linter":  "pkg/b/b.go:2:errcheck",
	} {
		f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused", finding), wholeErr: exitError(t, 1)}
		status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
		if status != verdictstatus.Fail || !strings.Contains(out, "carries no count") {
			t.Errorf("%s: status %q; want fail naming the missing count (output %q)", name, status, out)
		}
	}
}

// TestLintLowersAFallenCountAndFails checks that a count that falls, or
// that falls to zero, is written back to the register and fails the run,
// so the author commits the lowered register, and that a count that rose
// in the same run stays at its registered value.
func TestLintLowersAFallenCountAndFails(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused", "pkg/c/c.go:1:errcheck", "pkg/c/c.go:2:errcheck"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	writeTestLintRegister(t, l.root, map[string]int{"pkg/b unused": 3, "pkg/b staticcheck": 1, "pkg/c errcheck": 1, "pkg/gone misspell": 2})
	status, out := runLintCheck(l)
	if status != verdictstatus.Fail {
		t.Fatalf("status %q; want fail (output %q)", status, out)
	}
	for _, want := range []string{
		"commit the lowered register",
		"pkg/b unused: 1 where the register carried 3",
		"pkg/b staticcheck: 0 where the register carried 1",
		"pkg/gone misspell: 0 where the register carried 2",
		"pkg/c errcheck: 2 finding(s) where",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	got := readTestLintRegister(t, l.root)
	if len(got) != 2 || got["pkg/b unused"] != 1 || got["pkg/c errcheck"] != 1 {
		t.Errorf("register %v; want pkg/b unused lowered to 1 and pkg/c errcheck kept at 1", got)
	}
}

// TestLintPassesWithNoFindingAndAnEmptyRegister checks that a tree with
// no finding passes against a register that carries no count.
func TestLintPassesWithNoFindingAndAnEmptyRegister(t *testing.T) {
	f := &fakeLint{}
	l := stubLint(t, f, testLintBase, nil)
	writeTestLintRegister(t, l.root, nil)
	status, out := runLintCheck(l)
	if status != verdictstatus.Pass || !strings.Contains(out, "no finding in the whole tree") {
		t.Errorf("status %q; want pass with no finding (output %q)", status, out)
	}
}

// TestLintFailsWhenTheWholeTreeRunCannotFinish checks that a whole-tree
// run that exits on a timeout or a configuration error, or whose report
// does not parse, fails the tier rather than passing an unmeasured tree.
func TestLintFailsWhenTheWholeTreeRunCannotFinish(t *testing.T) {
	for name, f := range map[string]*fakeLint{
		"timeout":   {wholeOut: "level=error msg=\"Timeout exceeded\"", wholeErr: exitError(t, 4)},
		"not JSON":  {wholeJSON: "not json", wholeErr: exitError(t, 1)},
		"no linter": {wholeJSON: `{"Issues":[{"FromLinter":"","Pos":{"Filename":"pkg/b/b.go","Line":1}}]}`, wholeErr: exitError(t, 1)},
	} {
		status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
		if status != verdictstatus.Fail {
			t.Errorf("%s: status %q; want fail (output %q)", name, status, out)
		}
	}
}

// TestLintRefusesAFindingOutsideTheCheckout checks that a finding whose
// path leaves the checkout, which a cache shared with another checkout
// returns, fails the tier and names the cache, rather than being counted
// under a package of another tree.
func TestLintRefusesAFindingOutsideTheCheckout(t *testing.T) {
	for _, name := range []string{"../other/pkg/b/b.go", "/elsewhere/pkg/b/b.go"} {
		f := &fakeLint{wholeJSON: lintJSON(t, name+":9:unused"), wholeErr: exitError(t, 1)}
		status, out := runLintCheck(stubLint(t, f, testLintBase, nil))
		if status != verdictstatus.Fail || !strings.Contains(out, "golangci-lint cache clean") {
			t.Errorf("%s: status %q; want fail naming the cache (output %q)", name, status, out)
		}
	}
}

// TestLintReadsAnAbsolutePathInsideTheCheckout checks that a finding
// reported under an absolute path inside the checkout counts under its
// package relative to the root.
func TestLintReadsAnAbsolutePathInsideTheCheckout(t *testing.T) {
	root := t.TempDir()
	got, err := parseLintReport(root, []byte(lintJSON(t, filepath.Join(root, "pkg", "b", "b.go")+":9:unused")))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].key() != (lintKey{pkg: "pkg/b", linter: "unused"}) {
		t.Errorf("findings %v; want one under pkg/b", got)
	}
}

// TestLintRefusesAMalformedRegister checks that a register the check
// cannot read in full fails the tier rather than degrading to no count
// or an unbounded one.
func TestLintRefusesAMalformedRegister(t *testing.T) {
	const head = "kind: golangci-lint-finding-count-baseline\nversion: 1\n"
	for name, body := range map[string]string{
		"missing":        "",
		"kind":           "kind: other\nversion: 1\npackages: []\n",
		"version":        "kind: golangci-lint-finding-count-baseline\nversion: 2\npackages: []\n",
		"no packages":    head,
		"no package":     head + "packages:\n  - linters: {unused: 1}\n",
		"twice":          head + "packages:\n  - package: pkg/b\n    linters: {unused: 1}\n  - package: pkg/b\n    linters: {errcheck: 1}\n",
		"no linters":     head + "packages:\n  - package: pkg/b\n",
		"zero count":     head + "packages:\n  - package: pkg/b\n    linters: {unused: 0}\n",
		"no linter":      head + "packages:\n  - package: pkg/b\n    linters: {\"\": 1}\n",
		"does not parse": "kind: [\n",
	} {
		f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
		l := stubLint(t, f, testLintBase, nil)
		file := filepath.Join(l.root, golangciLintRegister)
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "register") {
			t.Errorf("%s: status %q; want fail naming the register (output %q)", name, status, out)
		}
	}
}

// TestLintRegisterRoundTrips checks that the register the check writes
// loads back with the same counts and drops a count of zero.
func TestLintRegisterRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeTestLintRegister(t, root, map[string]int{"pkg/b unused": 2, "pkg/b errcheck": 1, "cmd/x staticcheck": 5, "pkg/z misspell": 0})
	got := readTestLintRegister(t, root)
	if len(got) != 3 || got["pkg/b unused"] != 2 || got["pkg/b errcheck"] != 1 || got["cmd/x staticcheck"] != 5 {
		t.Errorf("register %v; want the three counts above zero", got)
	}
}

// TestLintMarkerInLinterOutputDoesNotChangeTheStatus checks that the
// unverified marker inside a source line golangci-lint echoes leaves a
// failing run at fail, because only the check's own first line carries
// its conclusion.
func TestLintMarkerInLinterOutputDoesNotChangeTheStatus(t *testing.T) {
	f := &fakeLint{
		changedOut: "cmd/x/x.go:9:1: unused\n\tconst m = \"" + verdictstatus.UnverifiedMarker + " echoed\"\n",
		changedErr: exitError(t, 1),
		wholeJSON:  lintJSON(t, "pkg/b/b.go:9:unused"),
		wholeErr:   exitError(t, 1),
	}
	if status, out := runLintCheck(stubLint(t, f, testLintBase, nil)); status != verdictstatus.Fail {
		t.Errorf("status %q; want fail (output %q)", status, out)
	}
}

// TestLintOnTheMergeBaseNamesTheUncommittedScope checks that a run whose
// merge base is HEAD says that only uncommitted and untracked lines were
// checked.
func TestLintOnTheMergeBaseNamesTheUncommittedScope(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused")}
	b := testLintBase
	b.atHead = true
	_, out := runLintCheck(stubLint(t, f, b, nil))
	if !strings.Contains(out, "uncommitted and untracked lines, because HEAD is the merge base") {
		t.Errorf("output does not name the uncommitted scope:\n%s", out)
	}
}

// TestLintIsUnverifiedWhenTheBaseCannotBeResolved checks that a run with
// no resolvable merge base does not pass: the tier reports unverified,
// names the reason, skips the changed-line run, and still reports the
// whole-tree findings.
func TestLintIsUnverifiedWhenTheBaseCannotBeResolved(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	status, out := runLintCheck(stubLint(t, f, lintBase{}, annotation.ErrNoBase))
	if status != verdictstatus.Unverified {
		t.Fatalf("status %q; want unverified (output %q)", status, out)
	}
	if len(f.calls) != 1 || containsArg(f.calls[0], "--new-from-patch=") {
		t.Errorf("calls %v; want the whole-tree run only", f.calls)
	}
	if !strings.Contains(out, "merge base could not be resolved") || !strings.Contains(out, "unused 1") {
		t.Errorf("output lacks the reason or the whole-tree findings:\n%s", out)
	}
}

// TestLintIsUnverifiedWithAnotherLinterRelease checks that a linter other
// than the pinned release leaves a clean run unverified and a finding on
// a changed line failing, and that its whole-tree counts are reported
// without being compared with the register, which stays as it was.
func TestLintIsUnverifiedWithAnotherLinterRelease(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/c/c.go:9:errcheck"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	l.version = func(context.Context, string, string) error { return errors.New("golangci-lint 2.1.0 is installed") }
	status, out := runLintCheck(l)
	if status != verdictstatus.Unverified || !strings.Contains(out, "2.1.0") || !strings.Contains(out, "not compared") {
		t.Errorf("clean run with another release: status %q (output %q)", status, out)
	}
	if got := readTestLintRegister(t, l.root); len(got) != 1 || got["pkg/b unused"] != 1 {
		t.Errorf("register %v; want it unchanged", got)
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
	l := stubLint(t, f, testLintBase, nil)
	l.bin = ""
	status, out := runLintCheck(l)
	if status != verdictstatus.Unverified || !strings.Contains(out, "golangci-lint not found") || len(f.calls) != 0 {
		t.Errorf("run without the linter: status %q, output %q, calls %v", status, out, f.calls)
	}
}

// TestLintRefusesARegisterThatIsNotARegularFile checks that a register
// reached through a symbolic link fails the tier and is not rewritten
// through the link.
func TestLintRefusesARegisterThatIsNotARegularFile(t *testing.T) {
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	file := filepath.Join(l.root, golangciLintRegister)
	target := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.Rename(file, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "not a regular file") {
		t.Errorf("status %q; want fail naming the link (output %q)", status, out)
	}
	if err := writeLintRegister(file, map[lintKey]int{{pkg: "pkg/b", linter: "unused"}: 1}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("write through a link: error %v", err)
	}
}

// TestLintFindsNolintDirectivesThatListAll checks that a //nolint
// directive listing `all`, alone or after another linter, is found on
// its line, and that a directive naming other linters, a mention of a
// directive inside another comment, and a string are not.
func TestLintFindsNolintDirectivesThatListAll(t *testing.T) {
	src := "package m\n\n" +
		"func a() {} //nolint:all // reason\n" +
		"func b() {} //nolint:gosec,all // reason\n" +
		"func c() {} // nolint:errcheck,all\n" +
		"func d() {} //nolint:allx // reason\n" +
		"func e() {} //nolint:errcheck // reason\n" +
		"// A directive such as `//nolint:all` is refused.\n" +
		"var s = \"//nolint:all\"\n"
	got := nolintAllLines("m.go", []byte(src))
	if len(got) != 3 || !strings.HasPrefix(got[0], "m.go:3:") || !strings.HasPrefix(got[1], "m.go:4:") || !strings.HasPrefix(got[2], "m.go:5:") {
		t.Errorf("found %q; want m.go:3 m.go:4 m.go:5", got)
	}
}

// TestLintScansTrackedAndUntrackedFilesForNolintAll checks that the scan
// reads tracked and untracked Go files, leaves testdata out, and that a
// directive it finds fails the tier.
func TestLintScansTrackedAndUntrackedFilesForNolintAll(t *testing.T) {
	r := newLintRepo(t, "main")
	r.commit("tracked.go", "package m\n\nfunc T() {} //nolint:all // reason\n", "tracked")
	r.write("untracked.go", "package m\n\nfunc U() {} //nolint:gosec,all // reason\n")
	r.write("testdata/fixture.go", "package m\n\nfunc F() {} //nolint:all // fixture\n")
	got, err := scanLintSources(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "tracked.go:3:") || !strings.HasPrefix(got[1], "untracked.go:3:") {
		t.Errorf("found %v; want tracked.go:3 and untracked.go:3", got)
	}
	f := &fakeLint{wholeJSON: lintJSON(t, "pkg/b/b.go:9:unused"), wholeErr: exitError(t, 1)}
	l := stubLint(t, f, testLintBase, nil)
	l.scan = func(context.Context, string) ([]string, error) { return got, nil }
	if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "untracked.go:3") {
		t.Errorf("status %q; want fail naming the directive (output %q)", status, out)
	}
	l.scan = func(context.Context, string) ([]string, error) { return nil, errors.New("git ls-files failed") }
	if status, out := runLintCheck(l); status != verdictstatus.Fail || !strings.Contains(out, "did not finish") {
		t.Errorf("a scan that fails: status %q; want fail (output %q)", status, out)
	}
}

// TestLintCacheIsPerCheckout checks that each checkout gets a cache
// directory of its own, under the inherited GOLANGCI_LINT_CACHE or under
// golangci-lint's default directory, so a finding cached by one checkout
// is never returned under another checkout's paths.
func TestLintCacheIsPerCheckout(t *testing.T) {
	cacheOf := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "GOLANGCI_LINT_CACHE="); ok {
				out = append(out, v)
			}
		}
		return out
	}
	inherited := []string{"GOLANGCI_LINT_CACHE=/shared"}
	a := cacheOf(golangciLintEnv(inherited, "/work/a", "linux", "/home/alice/.cache"))
	b := cacheOf(golangciLintEnv(inherited, "/work/b", "linux", "/home/alice/.cache"))
	if len(a) != 1 || len(b) != 1 || a[0] == b[0] || filepath.Dir(a[0]) != "/shared" || filepath.Dir(b[0]) != "/shared" {
		t.Errorf("caches %v and %v; want one directory per checkout under /shared", a, b)
	}
	if again := cacheOf(golangciLintEnv(inherited, "/work/a", "linux", "")); len(again) != 1 || again[0] != a[0] {
		t.Errorf("cache %v; want the same directory %s for the same checkout", again, a[0])
	}
	def := cacheOf(golangciLintEnv(nil, "/work/a", "darwin", "/home/alice/.cache"))
	if len(def) != 1 || filepath.Dir(def[0]) != "/home/alice/.cache/golangci-lint" {
		t.Errorf("cache %v; want a directory under golangci-lint's default", def)
	}
	if none := cacheOf(golangciLintEnv(nil, "/work/a", "linux", "")); len(none) != 0 {
		t.Errorf("cache %v; want none when no cache directory is known", none)
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

// lintFindingFile returns a Go file whose errcheck finding is on line 7.
func lintFindingFile(name string) string {
	return "package m\n\nimport \"os\"\n\n// " + name + " removes a file.\nfunc " + name + "() {\n\tos.Remove(\"" + name + "\")\n}\n"
}

// TestLintWithTheInstalledLinterSeparatesChangedLinesFromTheRest runs the
// installed golangci-lint over a module in a temporary repository. A
// branch whose changed lines are clean passes with the pre-existing
// finding, which the register carries, as a warning, and a finding in a
// committed, an uncommitted, or an untracked change fails with that
// finding named in the changed-line section. A branch that deletes the
// only caller of a function adds a finding on a line it does not change,
// which the changed-line run does not report and the register comparison
// fails. It pins the new-code filtering and the JSON report of the pinned
// golangci-lint release rather than a fake. On macOS the temporary directory is reached
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
	r.commit("u.go", lintModuleCaller, "a function and its caller")
	writeTestLintRegister(t, r.root, map[string]int{". errcheck": 1})
	r.git("add", golangciLintRegister)
	r.git("commit", "-q", "-m", "register")
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
	if !strings.Contains(out, "errcheck 1") || !strings.Contains(out, "WARNING (non-fatal") || !strings.Contains(out, "Every count matches") {
		t.Errorf("the pre-existing finding is not a warning within the register:\n%s", out)
	}

	cases := []struct {
		name, file string
		apply      func()
		undo       func()
	}{
		{
			"committed", "committed.go",
			func() { r.commit("committed.go", lintFindingFile("Committed"), "finding") },
			func() { r.git("reset", "-q", "--hard", "HEAD~1") },
		},
		{
			"uncommitted", "clean.go",
			func() { r.write("clean.go", lintFindingFile("Uncommitted")) },
			func() { r.git("checkout", "-q", "--", "clean.go") },
		},
		{
			"untracked", "untracked.go",
			func() { r.write("untracked.go", lintFindingFile("Untracked")) },
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

	r.commit("u.go", "package m\n\nfunc helper() int { return 1 }\n", "delete the caller")
	out, err = l.run()
	if err == nil {
		t.Fatalf("a finding off the changed lines passed:\n%s", out)
	}
	for _, want := range []string{"golangci-lint: no finding on the lines changed", ". unused: 1 finding(s), and " + golangciLintRegister + " carries no count", "u.go:3:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// lintModuleCaller is a Go file whose unexported function has one caller.
// Deleting the caller makes the function unused on a line the deletion
// does not change.
const lintModuleCaller = `package m

func helper() int { return 1 }

// Caller returns helper's value.
func Caller() int { return helper() }
`

// containsArg reports whether args holds an argument with the prefix.
func containsArg(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}
