// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// The golangci-lint check of the static tier. It runs the linter twice
// over the same configuration. The first run reports only the findings on
// the lines the branch adds or changes relative to its merge base with
// the integration branch, and any such finding fails the tier. The second
// run counts the findings of the whole tree, which include the findings
// that predate the check, per package and linter, and compares the counts
// with the register of finding counts (lint_register.go): a count above the
// register fails the tier, and the findings within it are a warning. Each
// count the branch's register carries above the register at the merge
// base is a hand edit, listed for the merge-gate review. The
// whole-tree run becomes fatal on any finding when the pre-existing
// findings that GATE-I1 lists reach zero. The check is contributor
// tooling, so this file carries no specification annotation.

// lintDebtIssue tracks the pre-existing golangci-lint findings that keep
// the whole-tree run a warning.
const lintDebtIssue = "GATE-I1"

// golangciLintTimeout bounds the analysis of each golangci-lint run. A run
// that exceeds it exits non-zero, which fails the changed-line run rather
// than passing it. A cold run of the tree for linux takes about four and a
// half minutes on a development host.
const golangciLintTimeout = "10m"

// golangciLintWallLimit bounds each golangci-lint invocation as a whole.
// --allow-serial-runners makes golangci-lint wait for another instance's
// lock before its own --timeout starts, so this limit also bounds that
// wait. An invocation killed at the limit fails like any other run that
// exits non-zero.
const golangciLintWallLimit = 25 * time.Minute

// golangciLintExitIssues is the status the check asks golangci-lint to
// exit with when it reports findings (--issues-exit-code). Every other
// non-zero status is a run that did not finish, such as a timeout or a
// configuration error.
const golangciLintExitIssues = 1

// golangciLintConfig is the configuration file the check names on the
// command line, so a second configuration file that golangci-lint would
// otherwise find first, such as .golangci.yaml, plays no part.
const golangciLintConfig = ".golangci.yml"

// golangciLintVersionFile and golangciLintVersionVar name the pin of the
// golangci-lint release, which tests/gate/Dockerfile installs and
// scripts/setup-dev.sh installs on a development host.
const (
	golangciLintVersionFile = "scripts/lib/versions.sh"
	golangciLintVersionVar  = "LENNY_VERSION_GOLANGCI_LINT"
)

// lintSafePath is the form of a Go file path the changed-line run accepts.
// The diff golangci-lint reads names each file in a `+++ b/<path>` header
// and matches it to a finding's path byte for byte; git quotes a path with
// a byte outside this set or ends its header with a tab, and such a path
// would match no finding.
var lintSafePath = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// lintBase is the commit the changed-line run compares the working tree
// with.
type lintBase struct {
	// commit is the merge base of HEAD with ref.
	commit string
	// ref names the integration ref the merge base was taken against.
	ref string
	// atHead reports that the merge base is HEAD itself, as on the
	// integration branch, so only uncommitted and untracked changes count
	// as changed lines.
	atHead bool
}

// resolveLintBase returns the merge base the changed-line run compares
// with. With base empty, it takes the first integration ref the
// repository carries, main and then origin/main (annotation.MergeBase),
// which is the resolution the static tier's other changed-line lints use.
// With base set, as the merge gate's --base sets it, it takes the merge
// base of base with HEAD, as --since reads a plain ref (resolveDiffSides).
// A range is accepted only when its head side is HEAD, because the linter
// reads the working tree. Any failure, including a repository that
// carries none of the refs, is returned as an error.
func resolveLintBase(ctx context.Context, root, base string) (lintBase, error) {
	head, err := commitOf(ctx, root, "HEAD")
	if err != nil {
		return lintBase{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	var b lintBase
	if base == "" {
		commit, ref, err := annotation.MergeBase(ctx, root)
		if err != nil {
			return lintBase{}, err
		}
		b = lintBase{commit: commit, ref: ref}
	} else {
		sides, err := resolveDiffSides(ctx, root, base)
		if err != nil {
			return lintBase{}, fmt.Errorf("find the merge base with %s: %w", base, err)
		}
		if sides.head != head {
			return lintBase{}, fmt.Errorf("the range %s ends at %s rather than at HEAD %s, and golangci-lint reads the working tree", base, shortCommit(sides.head), shortCommit(head))
		}
		b = lintBase{commit: sides.base, ref: base}
	}
	b.atHead = b.commit == head
	return b, nil
}

// buildLintPatch returns the diff of the working tree against base for
// the Go files, in the unified format golangci-lint reads with
// --new-from-patch, with every untracked Go file as a wholly added file.
//
// The harness writes the diff itself rather than letting golangci-lint
// run git, so that nothing in the branch or the host changes which lines
// count as changed. --text keeps the hunks of a file that a
// .gitattributes entry marks binary or -diff, which git otherwise prints
// as "Binary files differ" with no lines at all. --no-textconv and
// --no-ext-diff keep a configured filter out of the output, the explicit
// prefixes override diff.noprefix and the other prefix settings, and -M
// sets rename detection whatever diff.renames says, so a moved file
// counts only its edited lines. A changed Go file whose path git would
// quote, or that is a symbolic link (whose diff is the link text rather
// than the code the compiler reads), is refused with an error.
func buildLintPatch(ctx context.Context, root, base string) (string, error) {
	gitArgs := []string{"-c", "core.quotePath=false", "-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false"}
	names, err := gitOutput(ctx, root, append(gitArgs, "diff", "--name-only", "-z", "-M", "--diff-filter=AMR", base, "--", "*.go")...)
	if err != nil {
		return "", err
	}
	untracked, err := gitOutput(ctx, root, "ls-files", "-z", "--others", "--exclude-standard", "--", "*.go")
	if err != nil {
		return "", err
	}
	newFiles := splitNUL(untracked)
	if err := checkLintPaths(root, append(splitNUL(names), newFiles...)); err != nil {
		return "", err
	}
	patch, err := gitOutput(ctx, root, append(gitArgs, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--text", "-M",
		"--src-prefix=a/", "--dst-prefix=b/", base, "--", "*.go")...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(patch)
	b.WriteString("\n")
	for _, p := range newFiles {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return "", fmt.Errorf("read the untracked file %s: %w", p, err)
		}
		writeAddedFilePatch(&b, p, body)
	}
	return b.String(), nil
}

// checkLintPaths refuses a changed Go file the diff cannot name exactly.
func checkLintPaths(root string, paths []string) error {
	var refused []string
	for _, p := range paths {
		if !lintSafePath.MatchString(p) {
			refused = append(refused, fmt.Sprintf("%q: the path holds a character outside [A-Za-z0-9._/-]", p))
			continue
		}
		if fi, err := os.Lstat(filepath.Join(root, p)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			refused = append(refused, fmt.Sprintf("%s: the file is a symbolic link", p))
		}
	}
	if len(refused) > 0 {
		return fmt.Errorf("changed Go files the changed-line lint cannot read: %s", strings.Join(refused, "; "))
	}
	return nil
}

// writeAddedFilePatch writes a unified diff that adds the file p with
// body.
func writeAddedFilePatch(b *strings.Builder, p string, body []byte) {
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(body) == 0 {
		lines = nil
	}
	fmt.Fprintf(b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n", p, p, p)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
}

// splitNUL splits git's NUL-terminated output.
func splitNUL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// golangciLint is the static tier's golangci-lint check. Its fields are
// injected so a test runs the check's control flow without the linter or
// a repository.
type golangciLint struct {
	// root is the checkout the linter runs in.
	root string
	// base is the ref the branch merges into, or empty for the default
	// integration refs (see resolveLintBase).
	base string
	// bin is the golangci-lint binary, or empty when none is installed.
	bin string
	// resolve finds the merge base.
	resolve func(ctx context.Context, root, base string) (lintBase, error)
	// patch writes the diff of the working tree against a commit.
	patch func(ctx context.Context, root, base string) (string, error)
	// version reports why the installed golangci-lint is not the pinned
	// release, or nil when it is.
	version func(ctx context.Context, root, bin string) error
	// exec runs bin with args in root and returns the combined output.
	exec func(ctx context.Context, bin, root string, args ...string) (string, error)
	// scan lists the Go source problems in root that hide code from the
	// linter (lint_sources.go).
	scan func(ctx context.Context, root string) ([]string, error)
	// baseRegister reads the register as a commit holds it, reporting
	// false when the commit carries none (loadBaseLintRegister).
	baseRegister func(ctx context.Context, root, commit string) (map[lintKey]int, bool, error)
	// deps lists the module's packages that the product binaries import
	// and that ./... does not match (lint_deps.go).
	deps func(ctx context.Context, root string) ([]string, error)
}

// newGolangciLint returns the check wired to the installed linter, git,
// and the process runner.
//
// The linter runs in root with its symbolic links resolved. golangci-lint
// matches each finding to the diff by the finding's path relative to its
// working directory. When the working directory is reached through a
// symbolic link (on macOS, /var is a link to /private/var), the
// relative path it computes climbs out to the root and names no file in
// the diff, so every finding reads as old and the changed-line run passes
// whatever it finds.
func newGolangciLint(root, base string) golangciLint {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return golangciLint{
		root:    root,
		base:    base,
		bin:     resolveGoBin("golangci-lint"),
		resolve: resolveLintBase,
		patch:   buildLintPatch,
		version: checkGolangciLintVersion,
		exec:    execGolangciLint,
		scan:    scanLintSources,

		baseRegister: loadBaseLintRegister,
		deps: func(ctx context.Context, root string) ([]string, error) {
			return productPackagesOutsideLint(ctx, root, execGoList)
		},
	}
}

// checkGolangciLintVersion compares the installed golangci-lint with the
// release the repository pins, and reports a difference or a failure to
// read either side.
func checkGolangciLintVersion(ctx context.Context, root, bin string) error {
	want, err := pinnedGolangciLintVersion(root)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, "version", "--short").Output()
	if err != nil {
		return fmt.Errorf("read the golangci-lint version: %w", err)
	}
	if got := strings.TrimPrefix(strings.TrimSpace(string(out)), "v"); got != want {
		return fmt.Errorf("golangci-lint %s is installed and %s pins %s (run scripts/setup-dev.sh)", got, golangciLintVersionFile, want)
	}
	return nil
}

// pinnedGolangciLintVersion reads the pinned golangci-lint release.
func pinnedGolangciLintVersion(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, golangciLintVersionFile))
	if err != nil {
		return "", fmt.Errorf("read the golangci-lint pin: %w", err)
	}
	m := regexp.MustCompile(`(?m)^` + golangciLintVersionVar + `="([^"]+)"`).FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("%s does not set %s", golangciLintVersionFile, golangciLintVersionVar)
	}
	return string(m[1]), nil
}

// execGolangciLint runs golangci-lint in root, bounded by
// golangciLintWallLimit, in the environment golangciLintEnv returns.
func execGolangciLint(ctx context.Context, bin, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, golangciLintWallLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	userCache, _ := os.UserCacheDir()
	cmd.Env = golangciLintEnv(os.Environ(), root, runtime.GOOS, userCache)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// golangciLintEnv returns the environment of a golangci-lint run, built
// from the harness's own environment env on a host whose operating system
// is goos and whose user cache directory is userCache. It is the build
// target of lintTargetEnv, with these settings of the run's own:
//
//   - GOLANGCI_DIFF_PROCESSOR_PATCH replaces the diff golangci-lint
//     filters by, so an inherited value is dropped.
//   - PWD names root. os/exec sets it only when the environment is
//     inherited, and golangci-lint reads its working directory from it.
//   - GOLANGCI_LINT_CACHE names a directory of its own for root, under
//     the inherited GOLANGCI_LINT_CACHE or under golangci-lint's default
//     directory in userCache (golangciLintCache). golangci-lint keys a
//     cached package by its content and returns the cached findings with
//     the file paths of the checkout that first reported them, so a cache
//     shared by two checkouts of the same code, such as two worktrees or
//     the gate's cache volume, reports one checkout's findings under the
//     other's paths. The changed-line run then matches no finding to the
//     diff and passes, and the whole-tree counts name no package of the
//     tree.
func golangciLintEnv(env []string, root, goos, userCache string) []string {
	drop := map[string]bool{"GOLANGCI_DIFF_PROCESSOR_PATCH": true, "PWD": true, "GOLANGCI_LINT_CACHE": true}
	cacheBase := ""
	target := lintTargetEnv(env, goos)
	out := make([]string, 0, len(target)+2)
	for _, kv := range target {
		name, value, _ := strings.Cut(kv, "=")
		if name == "GOLANGCI_LINT_CACHE" {
			cacheBase = value
		}
		if !drop[name] {
			out = append(out, kv)
		}
	}
	out = append(out, "PWD="+root)
	if cache := golangciLintCache(cacheBase, userCache, root); cache != "" {
		out = append(out, "GOLANGCI_LINT_CACHE="+cache)
	}
	return out
}

// lintTargetEnv returns env set to the build the lint reads, for both
// golangci-lint runs and the listing of the product packages
// (lint_deps.go), on a host whose operating system is goos.
//
//   - GOWORK is off, so a go.work file in the checkout or above it cannot
//     add modules to the build or replace the module's own requirements
//     (owner's answer to GT-10, 2026-11-20).
//   - On a host other than Linux the build is the Linux one, as the merge
//     gate's container reads, so a package that builds only on Linux
//     does not report a type-checking error that the gate never sees.
//     cgo is off for that cross-target analysis.
func lintTargetEnv(env []string, goos string) []string {
	drop := map[string]bool{"GOWORK": true}
	if goos != "linux" {
		drop["GOOS"], drop["CGO_ENABLED"] = true, true
	}
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			out = append(out, kv)
		}
	}
	out = append(out, "GOWORK=off")
	if goos != "linux" {
		out = append(out, "GOOS=linux", "CGO_ENABLED=0")
	}
	return out
}

// golangciLintCache returns the cache directory of the checkout at root:
// a directory named for a digest of root under base, or, when base is
// empty, under golangci-lint's default directory in userCache. It returns
// "" when neither is known, and golangci-lint then picks its own.
func golangciLintCache(base, userCache, root string) string {
	if base == "" {
		if userCache == "" {
			return ""
		}
		base = filepath.Join(userCache, "golangci-lint")
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(base, "checkout-"+hex.EncodeToString(sum[:8]))
}

// run runs the check. A finding on a changed line, a changed Go file the
// diff cannot name, or a changed-line run that fails for any other reason
// returns an error, which fails the tier, and so does a whole-tree count
// the register does not allow (wholeTree), a source that hides code from
// the linter (sourceSection), and a package of the product binaries that
// neither run reads (depsSection). When no golangci-lint is installed,
// when the installed one is not the pinned release, or when the merge
// base cannot be resolved, the output opens with the unverified
// marker, so the tier reports that the check proved nothing rather than
// passing; without a resolved merge base the changed-line run does not
// happen and the register's raises are not checked. The whole-tree run
// happens whenever the linter is installed.
func (l golangciLint) run() (string, error) {
	if l.bin == "" {
		return verdictstatus.UnverifiedMarker + " golangci-lint not found, so the lint did not run (run scripts/setup-dev.sh; the gate image installs it)", nil
	}
	ctx := context.Background()
	var unverified, sections []string
	var failure error
	versionErr := l.version(ctx, l.root, l.bin)
	if versionErr != nil {
		unverified = append(unverified, versionErr.Error())
	}
	var base *lintBase
	b, err := l.resolve(ctx, l.root, l.base)
	if err != nil {
		unverified = append(unverified, fmt.Sprintf("golangci-lint did not run on the changed lines, and raises of %s were not checked: the merge base could not be resolved (%v); fetch the integration branch or pass --base", golangciLintRegister, err))
	} else {
		base = &b
		section, runErr := l.changedLines(ctx, b)
		sections = append(sections, section)
		failure = runErr
	}
	section, treeErr := l.wholeTree(ctx, versionErr == nil, base)
	sections = append(sections, section)
	failure = errors.Join(failure, treeErr)
	section, scanErr := l.sourceSection(ctx)
	sections = append(sections, section)
	failure = errors.Join(failure, scanErr)
	section, depsErr := l.depsSection(ctx)
	sections = append(sections, section)
	failure = errors.Join(failure, depsErr)
	if len(unverified) > 0 {
		sections = append([]string{verdictstatus.UnverifiedMarker + " " + strings.Join(unverified, "; ")}, sections...)
	}
	return strings.Join(sections, "\n"), failure
}

// classifyGolangciLint reads the check's output as unverified only when
// the output opens with the unverified marker, which the check writes as
// its first line. The rest of the output is the linter's, which echoes
// source lines, so a marker inside it is not the check's conclusion.
//
// Either way the detail names each raise of the register the output lists
// on a line that opens with lintRaiseMarker, so the static tier records
// the raise in the verdict even when the check passes. A line of the
// linter's own output that opens with the marker can add a false entry
// but cannot remove one the check wrote.
func classifyGolangciLint(out string) (string, string) {
	first, _, _ := strings.Cut(out, "\n")
	status, detail := verdictstatus.Pass, ""
	if strings.HasPrefix(first, verdictstatus.UnverifiedMarker) {
		status, detail = verdictstatus.Unverified, strings.TrimSpace(strings.TrimPrefix(first, verdictstatus.UnverifiedMarker))
	}
	var raises []string
	for _, line := range strings.Split(out, "\n") {
		if r, ok := strings.CutPrefix(line, lintRaiseMarker); ok {
			raises = append(raises, strings.TrimSpace(r))
		}
	}
	if len(raises) > 0 {
		note := fmt.Sprintf("%d raise(s) of %s by hand, each needing the merge-gate review that records its reason: %s", len(raises), golangciLintRegister, strings.Join(raises, "; "))
		detail = strings.TrimPrefix(detail+"; "+note, "; ")
	}
	return status, detail
}

// changedLines runs golangci-lint with --new-from-patch over the diff
// buildLintPatch writes against the merge base. The linter then reports
// a finding only on a line the diff adds, and it reports a type-checking
// error wherever it occurs. A patch given on the command line takes
// precedence over every new-code setting in the configuration file.
func (l golangciLint) changedLines(ctx context.Context, b lintBase) (string, error) {
	scope := fmt.Sprintf("the lines changed since the merge base %s with %s", shortCommit(b.commit), b.ref)
	if b.atHead {
		scope = fmt.Sprintf("the uncommitted and untracked lines, because HEAD is the merge base %s with %s", shortCommit(b.commit), b.ref)
	}
	patch, err := l.patch(ctx, l.root, b.commit)
	if err != nil {
		return fmt.Sprintf("golangci-lint did not run on %s: %v", scope, err), fmt.Errorf("diff for the changed-line lint: %w", err)
	}
	f, err := os.CreateTemp("", "lenny-lint-*.patch")
	if err != nil {
		return fmt.Sprintf("golangci-lint did not run on %s: %v", scope, err), fmt.Errorf("write the changed-line patch: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.WriteString(patch)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Sprintf("golangci-lint did not run on %s: %v", scope, werr), fmt.Errorf("write the changed-line patch: %w", werr)
	}
	out, err := l.exec(ctx, l.bin, l.root, golangciLintArgs(l.root, "--new-from-patch="+f.Name())...)
	if err == nil {
		return fmt.Sprintf("golangci-lint: no finding on %s", scope), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == golangciLintExitIssues {
		return fmt.Sprintf("golangci-lint reports findings on %s, or type-checking errors anywhere:\n%s", scope, out), fmt.Errorf("golangci-lint on the changed lines: %w", err)
	}
	return fmt.Sprintf("golangci-lint did not finish its run on %s (%v):\n%s", scope, err, out), fmt.Errorf("golangci-lint on the changed lines did not finish: %w", err)
}

// wholeTree runs golangci-lint over the whole tree with no cap on the
// findings it reports, counts them per package and linter, and compares
// the counts with the register. The findings within the register are a
// warning. A count that rises above the register, a count the register
// lacks, and a count that fell, which the run writes back to the register,
// return an error, as does a run that does not finish or a report the
// check cannot read. When comparable is false, because the installed
// golangci-lint is not the pinned release, the counts are reported and not
// compared, since another release counts other findings; the check is
// then unverified rather than passing. When base is set, the register is
// also compared with the register at the merge base (registerRaises), and
// each count the branch raised by hand is listed for the merge-gate
// review without failing the tier.
func (l golangciLint) wholeTree(ctx context.Context, comparable bool, base *lintBase) (string, error) {
	report, err := os.CreateTemp("", "lenny-lint-*.json")
	if err != nil {
		return fmt.Sprintf("golangci-lint did not run on the whole tree: %v", err), fmt.Errorf("create the whole-tree report: %w", err)
	}
	name := report.Name()
	_ = report.Close()
	defer func() { _ = os.Remove(name) }()
	out, err := l.exec(ctx, l.bin, l.root, golangciLintArgs(l.root,
		"--max-issues-per-linter=0", "--max-same-issues=0", "--show-stats=false", "--output.json.path="+name)...)
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != golangciLintExitIssues) {
		return fmt.Sprintf("golangci-lint did not finish its run on the whole tree (%v):\n%s", err, out), fmt.Errorf("golangci-lint on the whole tree did not finish: %w", err)
	}
	body, err := os.ReadFile(name)
	if err != nil {
		return fmt.Sprintf("golangci-lint wrote no report of the whole tree: %v\n%s", err, out), fmt.Errorf("read the whole-tree report: %w", err)
	}
	findings, err := parseLintReport(l.root, body)
	if err != nil {
		return fmt.Sprintf("golangci-lint whole-tree report: %v", err), err
	}
	counted := countLintFindings(findings)
	summary := lintSummary(findings)
	if !comparable {
		return summary + fmt.Sprintf("\nThe counts were not compared with %s, because the installed golangci-lint is not the pinned release.", golangciLintRegister), nil
	}
	carried, err := loadLintRegister(filepath.Join(l.root, golangciLintRegister))
	if err != nil {
		return summary + "\n" + err.Error(), err
	}
	raised, err := l.registerRaises(ctx, base, carried)
	if err != nil {
		return summary + "\n" + err.Error(), err
	}
	c := compareLintCounts(counted, carried)
	if len(c.lowered) > 0 {
		if err := writeLintRegister(filepath.Join(l.root, golangciLintRegister), c.kept); err != nil {
			return summary + "\n" + err.Error() + raised, err
		}
	}
	return lintRegisterSection(summary, c, findings) + raised, lintRegisterError(c)
}

// registerRaises compares the branch's register, carried, with the
// register at the merge base and returns a section that lists each count
// the branch raised (GT-8, 2026-11-20). A raise does not fail the tier,
// because a count may rise through a hand edit that the merge-gate review
// covers (GT-7), and each one is written on a line that opens with
// lintRaiseMarker, which the classifier carries into the verdict. With no
// merge base, the section says that raises were not checked; the run
// already opens with the unverified marker. A register the merge base
// holds but the check cannot read is an error.
func (l golangciLint) registerRaises(ctx context.Context, base *lintBase, carried map[lintKey]int) (string, error) {
	if base == nil {
		return fmt.Sprintf("\nRaises of %s were not checked, because the merge base could not be resolved.", golangciLintRegister), nil
	}
	baseCounts, present, err := l.baseRegister(ctx, l.root, base.commit)
	if err != nil {
		return "", err
	}
	raises := lintRaises(baseCounts, carried)
	if len(raises) == 0 {
		return fmt.Sprintf("\nNo count of %s is above the register at the merge base %s.", golangciLintRegister, shortCommit(base.commit)), nil
	}
	var b strings.Builder
	if !present {
		fmt.Fprintf(&b, "\nThe merge base %s carries no %s, so each count of the branch's register is a raise:", shortCommit(base.commit), golangciLintRegister)
	} else {
		fmt.Fprintf(&b, "\nCounts of %s above the register at the merge base %s, which a hand edit raised and the merge-gate review covers:", golangciLintRegister, shortCommit(base.commit))
	}
	for _, r := range raises {
		b.WriteString("\n" + lintRaiseMarker + " " + r.String())
	}
	return b.String(), nil
}

// lintSummary states how many findings the whole tree carries, per
// linter, and the issue that tracks them.
func lintSummary(findings []lintFinding) string {
	if len(findings) == 0 {
		return "golangci-lint: no finding in the whole tree"
	}
	perLinter := map[string]int{}
	for _, f := range findings {
		perLinter[f.linter]++
	}
	linters := make([]string, 0, len(perLinter))
	for name := range perLinter {
		linters = append(linters, name)
	}
	sort.Strings(linters)
	parts := make([]string, 0, len(linters))
	for _, name := range linters {
		parts = append(parts, fmt.Sprintf("%s %d", name, perLinter[name]))
	}
	return fmt.Sprintf("WARNING (non-fatal while each count stays within %s, until the pre-existing findings %s lists reach zero; see .golangci.yml): golangci-lint reports %d finding(s) in the whole tree: %s",
		golangciLintRegister, lintDebtIssue, len(findings), strings.Join(parts, ", "))
}

// lintRegisterSection renders the comparison: each count that rose with
// the findings behind it, and each count the run lowered.
func lintRegisterSection(summary string, c lintComparison, findings []lintFinding) string {
	var b strings.Builder
	b.WriteString(summary)
	if len(c.risen) > 0 {
		risen := map[lintKey]bool{}
		fmt.Fprintf(&b, "\ngolangci-lint finding counts above %s; fix the findings (a count rises only through a hand edit of the register, which the merge-gate review covers):", golangciLintRegister)
		for _, r := range c.risen {
			risen[r.key] = true
			b.WriteString("\n  " + r.String())
		}
		b.WriteString("\nThe findings in those packages and linters:")
		for _, f := range findings {
			if risen[f.key()] {
				b.WriteString("\n  " + f.String())
			}
		}
	}
	if len(c.lowered) > 0 {
		fmt.Fprintf(&b, "\ngolangci-lint finding counts fell, and the run lowered them in %s; commit the lowered register with the change:", golangciLintRegister)
		for _, r := range c.lowered {
			fmt.Fprintf(&b, "\n  %s: %d where the register carried %d", r.key, r.counted, r.registered)
		}
	}
	if len(c.risen) == 0 && len(c.lowered) == 0 {
		fmt.Fprintf(&b, "\nEvery count matches %s (%d count(s)).", golangciLintRegister, c.held)
	}
	return b.String()
}

// lintRegisterError returns the error that fails the tier when a count
// rose or fell, or nil when every count held.
func lintRegisterError(c lintComparison) error {
	switch {
	case len(c.risen) > 0 && len(c.lowered) > 0:
		return fmt.Errorf("golangci-lint: %d count(s) above %s and %d count(s) lowered in it", len(c.risen), golangciLintRegister, len(c.lowered))
	case len(c.risen) > 0:
		return fmt.Errorf("golangci-lint: %d count(s) above %s", len(c.risen), golangciLintRegister)
	case len(c.lowered) > 0:
		return fmt.Errorf("golangci-lint: %d count(s) lowered in %s; commit the register", len(c.lowered), golangciLintRegister)
	}
	return nil
}

// lintExtraBuildTags are the build tags the lint sets beyond the tiers'
// own (tierBuildTags). smoke marks the Embedded Mode and Source Mode smoke
// tests under tests/integration, which no tier selects by tag. The
// `ignore` tag is never set: it marks standalone programs that `go run`
// builds one file at a time.
var lintExtraBuildTags = []string{"smoke"}

// lintBuildTags returns the build tags every golangci-lint run sets: the
// build tag of every tier and lintExtraBuildTags, sorted. Without them the
// linter reads only the default build and skips the test files of the
// component, contract, integration, and later tiers (owner's answer to
// GT-4, 2026-11-19). The source scan (lint_sources.go) refuses a build
// tag the runs do not set and a negated tag they do set, so setting every
// tag at once leaves no file out of both runs.
func lintBuildTags() []string {
	tags := append(tierBuildTags(), lintExtraBuildTags...)
	sort.Strings(tags)
	return tags
}

// golangciLintArgs returns the arguments of one golangci-lint run. The
// configuration file, the exit status for findings, and the build tags
// are named on the command line, which takes precedence over the
// configuration, so the configuration cannot make a run with findings
// exit 0 or drop the tagged files.
// golangci-lint holds a lock file in the temporary directory and, by
// default, exits with an error when another instance holds it, which
// would fail the changed-line run whenever a lint in another worktree on
// the same host overlaps it. --allow-serial-runners makes it wait for the
// lock instead, within golangciLintWallLimit.
func golangciLintArgs(root string, extra ...string) []string {
	return append([]string{
		"run",
		"--config=" + filepath.Join(root, golangciLintConfig),
		fmt.Sprintf("--issues-exit-code=%d", golangciLintExitIssues),
		"--build-tags=" + strings.Join(lintBuildTags(), ","),
		"--timeout=" + golangciLintTimeout,
		"--allow-serial-runners",
	}, extra...)
}

// shortCommit abbreviates a commit hash for a message.
func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
