// SPDX-License-Identifier: MIT

// Command refactor is the committed, reproducible driver for the pkg/gateway
// C3 regroup (proposal 0020, Part B §2, §4 C3/C4). It consumes the C1 move
// manifest (scripts/refactor/manifest) and, per move:
//
//  1. git mv the directory so rename detection preserves history;
//  2. rewrite the quote-delimited import literal "<old>" -> "<new>" and the
//     runtime repo-relative path references (the slash-joined literal and the
//     split path-segment form) across every *.go file, using the
//     boundary-anchored primitives in the rewrite subpackage so a moved path
//     that is a strict prefix of a sibling does not corrupt the sibling;
//  3. rewrite the same references in tests/spec-map.json and
//     tests/change-graph.json on a path-token boundary;
//  4. gofmt -w and goimports -w the touched Go files to regroup imports;
//  5. gate on go build ./..., go vet ./..., lenny-test validate-maps, and
//     go list ./... (zero import cycles), aborting on any failure;
//  6. regenerate the change-graph index (tests/change-graph.json is the index;
//     the in-place rewrite in step 3 is its regeneration, re-validated by the
//     step-5 validate-maps gate);
//  7. run the post-move audit: grep tests/spec-map.json, tests/change-graph.json,
//     and *.go (test files included) for a surviving pre-move path token in a
//     driver-rewritable boundary-anchored form and ABORT on it; surface
//     comment and informational-string occurrences and non-Go prose files as a
//     NON-FATAL warning.
//
// Rollback is part of the gated move (proposal §2: a failed gate "aborts and
// rolls back that one move"; §6: "A move that fails any check is rolled back").
// The driver requires a clean working tree before it starts, so on a gate or
// audit failure it restores the tree itself (git reset --hard HEAD plus git
// clean of the created group directories) rather than leaving the staged git mv
// and rewrites for the operator to undo. reset --hard targets HEAD on the
// current branch and does not switch branches.
//
// Usage:
//
//	refactor [flags]
//	  -manifest <path>   move manifest (default scripts/refactor/manifest)
//	  -root <path>       repo root (default: the git toplevel)
//	  -only <old-path>   apply only the move whose old import path equals this
//	  -group <name>      apply only the moves whose destination group equals this
//	                     (the per-group landing the proposal partitions by: the
//	                     store cluster, the session cluster, and so on, each
//	                     applied, gated green, and committed before the next)
//	  -dry-run           parse, plan, and print the moves without touching the tree
//	  -skip-gates        skip the go build/vet/validate-maps/go list gates
//	                     (for fast local iteration; never in CI)
//	  -skip-audit        skip the post-move audit (never in CI)
//
// The driver is committed so the transform is auditable and re-runnable: an
// in-flight branch reconciles by merging the pre-move base and re-running this
// driver rather than hand-resolving import conflicts (proposal §2).
//
// spec: §4.1 (the gateway is one component; the regroup preserves the subsystem
// seams and changes only import paths and the path references that name them).
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "refactor: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	manifest  string
	root      string
	only      string
	group     string
	dryRun    bool
	skipGates bool
	skipAudit bool
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.manifest, "manifest", "scripts/refactor/manifest", "move manifest path")
	flag.StringVar(&cfg.root, "root", "", "repo root (default: git toplevel)")
	flag.StringVar(&cfg.only, "only", "", "apply only the move whose old import path equals this")
	flag.StringVar(&cfg.group, "group", "", "apply only the moves whose destination group equals this")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "plan without touching the tree")
	flag.BoolVar(&cfg.skipGates, "skip-gates", false, "skip the go build/vet/validate-maps/go list gates")
	flag.BoolVar(&cfg.skipAudit, "skip-audit", false, "skip the post-move audit")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	root, err := resolveRoot(cfg.root)
	if err != nil {
		return err
	}
	manifestPath := cfg.manifest
	if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(root, manifestPath)
	}
	f, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	defer f.Close()
	moves, err := rewrite.ParseManifest(f)
	if err != nil {
		return err
	}
	if cfg.only != "" {
		moves = filterMoves(moves, cfg.only)
		if len(moves) == 0 {
			return fmt.Errorf("no manifest move matches -only %q", cfg.only)
		}
	}
	if cfg.group != "" {
		moves = filterMovesByGroup(moves, cfg.group)
		if len(moves) == 0 {
			return fmt.Errorf("no manifest move targets -group %q", cfg.group)
		}
	}

	d := &driver{root: root, moves: moves, cfg: cfg}
	return d.execute()
}

// filterMoves returns the single move whose old import path equals only, so a
// single-package landing applies one move at a time.
func filterMoves(moves []rewrite.Move, only string) []rewrite.Move {
	for _, m := range moves {
		if m.Old == only {
			return []rewrite.Move{m}
		}
	}
	return nil
}

// filterMovesByGroup returns the moves whose destination group equals group, so
// a per-group landing applies one whole group at a time. The proposal partitions
// the manifest so each group move (the store cluster, the session cluster, and so
// on) is applied, gated green, and committed before the next (§2, §4 C3). The
// group is the first path segment under pkg/gateway/ in the new import path.
func filterMovesByGroup(moves []rewrite.Move, group string) []rewrite.Move {
	var out []rewrite.Move
	for _, m := range moves {
		if groupOf(m.New) == group {
			out = append(out, m)
		}
	}
	return out
}

// groupOf returns the destination group of a move's new import path: the first
// path segment after pkg/gateway/ (for example
// .../pkg/gateway/storage/leasestore -> "storage", and
// .../pkg/gateway/middleware/circuitbreaker/breakerstore -> "middleware"). A new
// path that does not sit under pkg/gateway/ yields "".
func groupOf(newImport string) string {
	rel := rewrite.RepoRel(newImport)
	const prefix = "pkg/gateway/"
	if !strings.HasPrefix(rel, prefix) {
		return ""
	}
	tail := rel[len(prefix):]
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		return tail[:i]
	}
	return tail
}

// resolveRoot returns the repo root: the -root flag when set, else the git
// toplevel.
func resolveRoot(flagRoot string) (string, error) {
	if flagRoot != "" {
		return filepath.Abs(flagRoot)
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("resolve repo root via git: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// driver carries the resolved root, the planned moves, and the run config. It
// applies the moves in batch (every move's tree rewrite, then one set of gates
// and one audit) so a per-group landing — selected with -only or a single-group
// manifest — is one atomic, gated unit.
type driver struct {
	root  string
	moves []rewrite.Move
	cfg   config

	// touchedGo is the set of absolute *.go paths the rewrite actually modified.
	// formatTree runs gofmt/goimports over exactly this set so the format pass is
	// scoped to "the touched files" (proposal §2 step (4)) and does not reformat
	// any untouched, non-canonical file under pkg/cmd/tests (§5 non-goal: no
	// reformatting outside the change).
	touchedGo map[string]struct{}
}

// execute applies the move batch as one atomic, gated unit: it requires a clean
// working tree to start, applies every move's git mv and reference rewrite,
// formats the touched files, then gates and audits. The proposal makes rollback
// part of the gated move (§2: a failed gate "aborts and rolls back that one
// move"; §6: "A move that fails any check is rolled back"). So a gate or audit
// failure does not leave the tree dirty: the driver restores the index and
// working tree to the pre-move state itself before returning the error. The
// pre-run clean-tree requirement makes that automatic rollback unambiguous,
// because git reset --hard plus git clean of the created group directories then
// reverts exactly the driver's own changes and nothing pre-existing.
func (d *driver) execute() error {
	if d.cfg.dryRun {
		d.printPlan()
		return nil
	}
	if err := d.requireCleanTree(); err != nil {
		return err
	}
	if err := d.applyAndVerify(); err != nil {
		if rbErr := d.rollback(); rbErr != nil {
			return fmt.Errorf("%w; ALSO rollback failed (tree may be dirty, inspect with git status): %v", err, rbErr)
		}
		return fmt.Errorf("%w; rolled back the move (tree restored to pre-move state)", err)
	}
	fmt.Printf("refactor: applied %d move(s); gates and audit clean\n", len(d.moves))
	return nil
}

// applyAndVerify performs the move's mutations (git mv, rewrite, format) and the
// gates and audit. A non-nil return is the trigger for execute to roll the move
// back. It is a separate method so execute's rollback wrapper stays a single
// error-handling site.
func (d *driver) applyAndVerify() error {
	for _, m := range gitMoveOrder(d.moves) {
		if err := d.gitMove(m); err != nil {
			return fmt.Errorf("git mv %s: %w", m.Old, err)
		}
	}
	if err := d.rewriteTree(); err != nil {
		return fmt.Errorf("rewrite references: %w", err)
	}
	if err := d.formatTree(); err != nil {
		return fmt.Errorf("format touched files: %w", err)
	}
	if !d.cfg.skipGates {
		if err := d.runGates(); err != nil {
			return fmt.Errorf("gate failed (move not safe): %w", err)
		}
	}
	if !d.cfg.skipAudit {
		if err := d.audit(); err != nil {
			return fmt.Errorf("post-move audit aborted (move not safe): %w", err)
		}
	}
	return nil
}

// requireCleanTree fails closed when the working tree or index carries
// uncommitted changes before the move starts. The per-group landing model the
// proposal relies on (each move "applied, verified green, and committed before
// the next") depends on a clean starting point: it lets the automatic rollback
// restore exactly the driver's own changes with git reset --hard plus a clean of
// the created group directories, rather than entangling them with pre-existing
// edits. A failed move on a dirty tree could not distinguish the driver's
// changes from the operator's, so the driver refuses to start.
func (d *driver) requireCleanTree() error {
	out, err := d.git("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("check working tree cleanliness: %w (%s)", err, out)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("working tree is not clean; commit or stash before running the move:\n%s", out)
	}
	return nil
}

// rollback restores the index and working tree to the pre-move state after a
// failed gate or audit, so the failed move is reverted as the proposal specifies
// (§2, §6) rather than left for the operator to undo by hand. Because execute
// required a clean tree before starting, git reset --hard reverts the staged
// git mv and the *.go/JSON rewrites to HEAD, and git clean removes the empty
// group directories the move created (git mv left them in place after the reset
// re-materialized the original directory). reset --hard targets HEAD, which is
// the current commit on the current branch; it does not switch branches.
func (d *driver) rollback() error {
	if out, err := d.git("reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("git reset --hard HEAD: %w (%s)", err, out)
	}
	// Remove any now-untracked files and the empty group directories the git mv
	// created. -d removes directories, -f forces, -q quiets; the reset already
	// restored the tracked files, so this clears only the move's residue.
	if out, err := d.git("clean", "-fdq"); err != nil {
		return fmt.Errorf("git clean -fdq: %w (%s)", err, out)
	}
	return nil
}

func (d *driver) printPlan() {
	fmt.Printf("refactor: %d planned move(s) under %s\n", len(d.moves), d.root)
	for _, m := range d.moves {
		fmt.Printf("  %s -> %s\n", rewrite.RepoRel(m.Old), rewrite.RepoRel(m.New))
	}
}

// gitMoveOrder returns the moves ordered so every self-prefix move (oldRel is a
// prefix of newRel, for example llmproxy -> llmproxy/llmproxy) runs its git mv
// before any other move. A self-prefix move renames pkg/gateway/<old> to a temp
// and back into pkg/gateway/<old>/<leaf>; if a sibling (credrouter ->
// llmproxy/credrouter) had already created pkg/gateway/<old>/ by moving in
// first, the self-prefix step-1 rename would sweep that sibling into the temp
// directory and land it at the wrong depth (llmproxy/llmproxy/credrouter).
// Running the self-prefix moves first means the group directory holds only the
// self-prefix package's own files when it nests, and siblings then move into the
// freshly created group dir at the correct depth. Order within each partition is
// preserved (the manifest's longest-first order).
func gitMoveOrder(moves []rewrite.Move) []rewrite.Move {
	ordered := make([]rewrite.Move, 0, len(moves))
	var rest []rewrite.Move
	for _, m := range moves {
		if isSelfPrefixMove(rewrite.RepoRel(m.Old), rewrite.RepoRel(m.New)) {
			ordered = append(ordered, m)
			continue
		}
		rest = append(rest, m)
	}
	return append(ordered, rest...)
}

// gitMove moves the package directory from its old repo-relative location to
// the new one, creating the intermediate group directory first. git mv
// preserves rename detection so history follows the file.
//
// A self-prefix move (oldRel is a path prefix of newRel, for example
// policy -> policy/policy, where the package becomes a same-named group's leaf)
// cannot be a direct git mv: git refuses to move a directory into itself. The
// move routes through a sibling temp directory instead, so git never sees the
// destination nested under the live source.
func (d *driver) gitMove(m rewrite.Move) error {
	oldRel := rewrite.RepoRel(m.Old)
	newRel := rewrite.RepoRel(m.New)
	if oldRel == newRel {
		return nil
	}
	oldDir := filepath.Join(d.root, filepath.FromSlash(oldRel))
	if _, err := os.Stat(oldDir); err != nil {
		return fmt.Errorf("source directory %s missing: %w", oldRel, err)
	}
	if isSelfPrefixMove(oldRel, newRel) {
		return d.gitMoveSelfPrefix(oldRel, newRel)
	}
	return d.gitMoveDirect(oldRel, newRel)
}

// isSelfPrefixMove reports whether newRel nests under oldRel (oldRel + "/" is a
// prefix of newRel), the case git mv cannot perform directly.
func isSelfPrefixMove(oldRel, newRel string) bool {
	return strings.HasPrefix(newRel, oldRel+"/")
}

// gitMoveDirect performs the ordinary git mv after creating the destination's
// parent group directory.
func (d *driver) gitMoveDirect(oldRel, newRel string) error {
	newDir := filepath.Join(d.root, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return fmt.Errorf("create group directory: %w", err)
	}
	if out, err := d.git("mv", oldRel, newRel); err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

// gitMoveSelfPrefix moves a package whose destination nests under its own
// source (oldRel -> oldRel/.../leaf) by routing through a sibling temp
// directory: git mv <old> <old>__refactor_tmp, then git mv the temp into the
// final nested location. git never moves a directory into itself this way, and
// both steps preserve rename detection. The temp name carries a suffix that no
// package uses, so it cannot collide with a real sibling.
func (d *driver) gitMoveSelfPrefix(oldRel, newRel string) error {
	tmpRel := oldRel + "__refactor_tmp"
	tmpDir := filepath.Join(d.root, filepath.FromSlash(tmpRel))
	if _, err := os.Stat(tmpDir); err == nil {
		return fmt.Errorf("self-prefix temp directory %s already exists", tmpRel)
	}
	if out, err := d.git("mv", oldRel, tmpRel); err != nil {
		return fmt.Errorf("self-prefix step 1 (git mv %s %s): %s: %s", oldRel, tmpRel, err, out)
	}
	newDir := filepath.Join(d.root, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		return fmt.Errorf("self-prefix create group directory: %w", err)
	}
	if out, err := d.git("mv", tmpRel, newRel); err != nil {
		return fmt.Errorf("self-prefix step 2 (git mv %s %s): %s: %s", tmpRel, newRel, err, out)
	}
	return nil
}

// rewriteTree applies the import-literal, runtime-path, and JSON-token rewrites
// across the tree for every move in this batch. It walks once and rewrites each
// file against all moves, so a file importing several moved packages is fixed in
// a single pass.
func (d *driver) rewriteTree() error {
	if err := d.rewriteGoFiles(); err != nil {
		return err
	}
	return d.rewriteJSONMaps()
}

// rewriteGoFiles rewrites every *.go file under the repo root, skipping the
// vendor and .git trees. Each file's content is passed through the
// import-literal and runtime-path primitives for the full move set, and only a
// changed file is written back.
func (d *driver) rewriteGoFiles() error {
	return filepath.WalkDir(d.root, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if dirEntry.IsDir() {
			if d.skipWalkDir(path, dirEntry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return d.rewriteFile(path, func(content string) string {
			content = rewrite.ImportLiterals(content, d.moves)
			content = rewrite.RuntimePaths(content, d.moves)
			return content
		})
	})
}

// rewriteJSONMaps rewrites the two committed JSON maps in place. This is the
// "regenerate the change-graph index" step: the maps are the index, and the
// boundary-anchored token rewrite regenerates them, re-validated by the
// validate-maps gate.
func (d *driver) rewriteJSONMaps() error {
	for _, rel := range []string{"tests/spec-map.json", "tests/change-graph.json"} {
		path := filepath.Join(d.root, filepath.FromSlash(rel))
		if err := d.rewriteFile(path, func(content string) string {
			return rewrite.JSONTokens(content, d.moves)
		}); err != nil {
			return fmt.Errorf("rewrite %s: %w", rel, err)
		}
	}
	return nil
}

// rewriteFile reads path, applies fn, and writes the result back only when it
// changed, preserving the file mode. A changed *.go file is recorded in
// touchedGo so the format pass can be scoped to exactly the files the rewrite
// modified (proposal §2 step (4)).
func (d *driver) rewriteFile(path string, fn func(string) string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := fn(string(raw))
	if out == string(raw) {
		return nil
	}
	if strings.HasSuffix(path, ".go") {
		d.recordTouchedGo(path)
	}
	return os.WriteFile(path, []byte(out), info.Mode())
}

// recordTouchedGo adds an absolute *.go path to the set the format pass scopes
// to, lazily initializing the set.
func (d *driver) recordTouchedGo(path string) {
	if d.touchedGo == nil {
		d.touchedGo = make(map[string]struct{})
	}
	d.touchedGo[path] = struct{}{}
}

// formatTree runs gofmt -w then goimports -w over exactly the *.go files the
// rewrite modified, so the format pass regroups imports only in the files the
// move already touched (proposal §2 step (4), §5 non-goal: no reformatting
// outside the change). goimports is resolved by PATH then GOPATH/bin and skipped
// with a warning when absent (matching the lenny-test tier-0 behavior), because
// gofmt alone leaves correct, compilable code and the import regrouping is
// cosmetic; the build gate still proves correctness.
func (d *driver) formatTree() error {
	targets := d.touchedGoFiles()
	if len(targets) == 0 {
		return nil
	}
	if out, err := d.runIn(d.root, "gofmt", append([]string{"-w"}, targets...)...); err != nil {
		return fmt.Errorf("gofmt: %s: %w", out, err)
	}
	goimports := resolveGoBin("goimports")
	if goimports == "" {
		fmt.Fprintln(os.Stderr, "refactor: goimports not on PATH or GOPATH/bin; skipping import regrouping (gofmt applied; build gate still enforced)")
		return nil
	}
	args := append([]string{"-w", "-local", "github.com/lennylabs/lenny"}, targets...)
	if out, err := d.runIn(d.root, goimports, args...); err != nil {
		return fmt.Errorf("goimports: %s: %w", out, err)
	}
	return nil
}

// touchedGoFiles returns the sorted absolute paths the rewrite modified, the set
// the format pass is scoped to. Sorting makes the gofmt/goimports invocation
// deterministic for an auditable, reproducible run.
func (d *driver) touchedGoFiles() []string {
	out := make([]string, 0, len(d.touchedGo))
	for p := range d.touchedGo {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// runGates runs the tier-0 gate the proposal mandates: go build ./..., go vet
// ./..., go list ./... (zero import cycles), and lenny-test validate-maps. Any
// failure aborts; the caller reverts the move with git.
func (d *driver) runGates() error {
	if err := d.runGoGates(); err != nil {
		return err
	}
	return d.runValidateMaps()
}

// runGoGates runs the go-toolchain gates: build, vet, and list (the import-cycle
// check). go list fails on an import cycle, so it doubles as the cycle gate the
// proposal names.
func (d *driver) runGoGates() error {
	gates := []struct {
		name string
		args []string
	}{
		{"go build ./...", []string{"go", "build", "./..."}},
		{"go vet ./...", []string{"go", "vet", "./..."}},
		{"go list ./... (import cycle check)", []string{"go", "list", "./..."}},
	}
	for _, g := range gates {
		if out, err := d.runIn(d.root, g.args[0], g.args[1:]...); err != nil {
			return fmt.Errorf("%s failed:\n%s", g.name, out)
		}
	}
	return nil
}

// runValidateMaps runs lenny-test validate-maps via go run, so the gate works
// without a prebuilt binary on PATH.
func (d *driver) runValidateMaps() error {
	if out, err := d.runIn(d.root, "go", "run", "./cmd/lenny-test", "validate-maps"); err != nil {
		return fmt.Errorf("lenny-test validate-maps failed:\n%s", out)
	}
	return nil
}

func (d *driver) git(args ...string) (string, error) {
	return d.runIn(d.root, "git", args...)
}

// runIn runs a command in dir and returns its combined output.
func (d *driver) runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// skipDir reports whether a directory should be skipped during the *.go walk by
// its basename (the always-skipped infrastructure trees).
func skipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", ".beads":
		return true
	default:
		return false
	}
}

// skipWalkDir reports whether a directory should be skipped during the rewrite
// and audit walks. It skips the always-skipped infrastructure trees by basename
// and, critically, the driver's own scripts/refactor subtree by path. The driver
// package carries manifest path strings as test fixtures (the rewrite and audit
// unit tests assert against literal "github.com/lennylabs/lenny/pkg/gateway/<pkg>"
// strings and JSON token fixtures). A tree-wide rewrite that descended into the
// driver's own sources would corrupt those fixtures (turning an Old->New move
// fixture into a no-op Old==New pair) and the audit would then flag the driver's
// test data as a stale reference. The driver imports no moved gateway package, so
// skipping its subtree changes no real import and keeps the tooling's tests
// stable across every group move (proposal §2: the transform is auditable and
// re-runnable).
func (d *driver) skipWalkDir(path, name string) bool {
	if skipDir(name) {
		return true
	}
	return path == d.selfDir()
}

// selfDir returns the absolute path of the driver's own package directory
// (scripts/refactor under the repo root), the subtree the walks skip.
func (d *driver) selfDir() string {
	return filepath.Join(d.root, "scripts", "refactor")
}

// resolveGoBin resolves a go-installed binary by PATH then GOPATH/bin,
// returning "" when neither resolves. It mirrors the lenny-test tier-0 helper so
// the driver's goimports invocation behaves identically.
func resolveGoBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	gopath, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(strings.TrimSpace(string(gopath)), "bin", name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}
