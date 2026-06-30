// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

// These tests exercise the driver orchestration (git mv, tree rewrite, JSON
// rewrite, audit) against a throwaway temp git repo, with the go-build/vet/
// validate-maps gates skipped (the temp tree is not the real module, so those
// gates do not apply; the pure-rewrite correctness is pinned in the rewrite
// package's unit tests). They are tier-1 unit tests over the driver's wiring.
//
// spec: §4.1 (the gateway regroup is a behavior-preserving directory move plus
// boundary-anchored path rewrites; the driver executes it reproducibly).

// initTempRepo lays out a minimal repo: a moved package directory, a Go file
// importing it (and reading it by runtime path), and the two JSON maps naming
// it. It returns the repo root.
func initTempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustRun(t, root, "git", "init", "-q")
	mustRun(t, root, "git", "config", "user.email", "test@example.com")
	mustRun(t, root, "git", "config", "user.name", "Refactor Test")

	writeFile(t, filepath.Join(root, "pkg", "gateway", "playground", "token.go"),
		"package playground\n\nvar Token = \"x\"\n")

	// A consumer importing the moved package and reading it by runtime path
	// (the split path-segment form and the slash-joined literal). The module
	// path is the real module prefix because RepoRel trims it to derive the
	// repo-relative directory git mv operates on; the tree need not build,
	// since the gates are skipped in these wiring tests.
	consumer := `package consumer

import (
	_ "github.com/lennylabs/lenny/pkg/gateway/playground"
)

const slashPath = "pkg/gateway/playground/token.go"

func read(parts ...string) {}

func init() {
	read("pkg", "gateway", "playground", "token.go")
}
`
	writeFile(t, filepath.Join(root, "pkg", "consumer", "consumer.go"), consumer)

	writeFile(t, filepath.Join(root, "tests", "spec-map.json"),
		"{\n  \"packages\": [\n    \"pkg/gateway/playground/...\",\n    \"pkg/gateway/playground/token_test.go\"\n  ]\n}\n")
	writeFile(t, filepath.Join(root, "tests", "change-graph.json"),
		"{\n  \"globs\": {\n    \"pkg/gateway/playground\": {\n      \"unit\": [\"pkg/gateway/playground/...\"]\n    }\n  }\n}\n")

	// A prose file that names the OLD path (should warn) and one that names only
	// the NEW path (must not warn, guarding the boundary-aware prose check).
	writeFile(t, filepath.Join(root, "docs", "old.md"),
		"The pkg/gateway/playground package owns the token.\n")

	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-q", "-m", "init", "--no-verify")
	return root
}

func testModuleMove() rewrite.Move {
	return rewrite.Move{
		Old: "github.com/lennylabs/lenny/pkg/gateway/playground",
		New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground",
	}
}

func TestDriver_GitMoveAndRewrite(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{
		root:  root,
		moves: []rewrite.Move{testModuleMove()},
		cfg:   config{skipGates: true, skipAudit: false},
	}
	if err := d.execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Directory moved.
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "mcpfabric", "playground", "token.go")); err != nil {
		t.Fatalf("moved directory missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "playground")); !os.IsNotExist(err) {
		t.Fatalf("old directory should be gone; stat err=%v", err)
	}

	// Import literal and runtime forms rewritten.
	consumer := readFile(t, filepath.Join(root, "pkg", "consumer", "consumer.go"))
	for _, want := range []string{
		`"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"`,
		`"pkg/gateway/mcpfabric/playground/token.go"`,
		`read("pkg", "gateway", "mcpfabric", "playground", "token.go")`,
	} {
		if !strings.Contains(consumer, want) {
			t.Errorf("consumer.go missing rewritten form %q:\n%s", want, consumer)
		}
	}

	// JSON maps rewritten.
	specMap := readFile(t, filepath.Join(root, "tests", "spec-map.json"))
	if !strings.Contains(specMap, `"pkg/gateway/mcpfabric/playground/..."`) {
		t.Errorf("spec-map.json glob not rewritten:\n%s", specMap)
	}
	if !strings.Contains(specMap, `"pkg/gateway/mcpfabric/playground/token_test.go"`) {
		t.Errorf("spec-map.json file ref not rewritten:\n%s", specMap)
	}
	changeGraph := readFile(t, filepath.Join(root, "tests", "change-graph.json"))
	if !strings.Contains(changeGraph, `"pkg/gateway/mcpfabric/playground"`) {
		t.Errorf("change-graph.json key not rewritten:\n%s", changeGraph)
	}
}

// The audit must abort when a driver-rewritable token survives (here: a Go file
// the driver failed to rewrite, simulated by writing the file after the move).
func TestDriver_AuditAbortsOnSurvivingGoLiteral(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: true}}
	if err := d.execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Inject a stale import literal that the driver "missed".
	stale := filepath.Join(root, "pkg", "stale", "stale.go")
	writeFile(t, stale, "package stale\n\nimport _ \"github.com/lennylabs/lenny/pkg/gateway/playground\"\n")

	if err := d.audit(); err == nil {
		t.Fatal("audit must abort on a surviving driver-rewritable Go literal")
	} else if !strings.Contains(err.Error(), "survived") {
		t.Fatalf("unexpected audit error: %v", err)
	}
}

// rollback: a gate failure must roll the move back, restoring the working tree
// to the pre-move (committed) state, rather than leaving the staged git mv and
// the *.go/JSON rewrites in place for the operator to undo by hand (proposal §2:
// a failed gate "aborts and rolls back that one move"; §6: "A move that fails any
// check is rolled back"). The temp repo has no go.mod, so the go build gate fails
// deterministically; after execute returns the error, the moved directory must
// be gone, the original directory must be back, and git status must be clean.
// This is constructed to FAIL against the pre-fix driver, which only returned the
// error and left the tree dirty.
func TestDriver_GateFailureRollsBackTheMove(t *testing.T) {
	root := initTempRepo(t)
	// Gates ON: the temp tree is not a Go module, so go build ./... fails.
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: false, skipAudit: true}}

	err := d.execute()
	if err == nil {
		t.Fatal("execute must fail when the go build gate fails")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("error must report the rollback; got %v", err)
	}

	// The move was reverted: the new directory is gone, the original is back.
	if _, statErr := os.Stat(filepath.Join(root, "pkg", "gateway", "mcpfabric", "playground")); !os.IsNotExist(statErr) {
		t.Fatalf("rollback should have removed the moved directory; stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "pkg", "gateway", "playground", "token.go")); statErr != nil {
		t.Fatalf("rollback should have restored the original directory: %v", statErr)
	}
	// The consumer's import literal must be back to the pre-move path.
	consumer := readFile(t, filepath.Join(root, "pkg", "consumer", "consumer.go"))
	if !strings.Contains(consumer, `"github.com/lennylabs/lenny/pkg/gateway/playground"`) {
		t.Fatalf("rollback should have restored the original import literal; got:\n%s", consumer)
	}
	// The working tree must be clean again.
	out, statusErr := d.git("status", "--porcelain")
	if statusErr != nil {
		t.Fatalf("git status: %v (%s)", statusErr, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("working tree must be clean after rollback; git status:\n%s", out)
	}
}

// rollback: an audit abort must restore the tree as a gate failure does. The
// move and its rewrite are applied, then a surviving driver-rewritable import
// literal is injected so the audit aborts; rollback must restore the original
// directory, the rewritten consumer, and remove the injected untracked file via
// git clean, leaving a clean tree (proposal §2, §6).
func TestDriver_AuditFailureRollsBackTheMove(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: false}}

	if err := d.gitMove(testModuleMove()); err != nil {
		t.Fatalf("gitMove: %v", err)
	}
	if err := d.rewriteTree(); err != nil {
		t.Fatalf("rewriteTree: %v", err)
	}
	// Inject a surviving Abort token the rewrite already ran past, forcing the
	// audit to abort.
	survivor := filepath.Join(root, "pkg", "survivor", "v.go")
	writeFile(t, survivor,
		"package survivor\n\nimport _ \"github.com/lennylabs/lenny/pkg/gateway/playground\"\n")
	if err := d.audit(); err == nil {
		t.Fatal("audit must abort on the surviving literal")
	}

	if err := d.rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "pkg", "gateway", "playground", "token.go")); statErr != nil {
		t.Fatalf("rollback should restore the original directory: %v", statErr)
	}
	if _, statErr := os.Stat(survivor); !os.IsNotExist(statErr) {
		t.Fatalf("rollback (git clean) should remove the injected untracked survivor; stat err=%v", statErr)
	}
	out, statusErr := d.git("status", "--porcelain")
	if statusErr != nil {
		t.Fatalf("git status: %v (%s)", statusErr, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("tree must be clean after rollback; git status:\n%s", out)
	}
}

// requireCleanTree fails closed: execute refuses to start on a dirty working
// tree, so the automatic rollback can revert exactly the driver's own changes.
func TestDriver_RefusesDirtyTree(t *testing.T) {
	root := initTempRepo(t)
	// Leave an untracked file so the tree is dirty.
	writeFile(t, filepath.Join(root, "dirty.txt"), "uncommitted\n")
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: true}}
	err := d.execute()
	if err == nil {
		t.Fatal("execute must refuse to start on a dirty working tree")
	}
	if !strings.Contains(err.Error(), "not clean") {
		t.Fatalf("error must name the dirty tree; got %v", err)
	}
	// The move must NOT have been applied.
	if _, statErr := os.Stat(filepath.Join(root, "pkg", "gateway", "mcpfabric", "playground")); !os.IsNotExist(statErr) {
		t.Fatalf("execute must not move the directory when the tree is dirty; stat err=%v", statErr)
	}
}

// The audit must abort when a JSON map still names a moved path.
func TestDriver_AuditAbortsOnSurvivingJSONToken(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: true}}
	// Do NOT run the rewrite; the JSON still carries the old token. git-move
	// only, then audit.
	if err := d.gitMove(testModuleMove()); err != nil {
		t.Fatalf("gitMove: %v", err)
	}
	if err := d.audit(); err == nil {
		t.Fatal("audit must abort on a surviving JSON token")
	}
}

// The JSON audit must record an informational-string occurrence of a moved path
// inside a "notes" value as a warning (not abort, not silently drop it), the
// same way the *.go and prose surfaces are handled (proposal 0020 §4 C4). The
// driver's JSONTokens rewrites only quote/slash-bounded tokens, so a path inside
// a "notes" sentence (bounded by a space or '.') survives by design and must be
// surfaced for the manual sweep. This is constructed to FAIL against the pre-fix
// auditJSONMaps, which collected only Abort results and dropped the warning.
func TestDriver_AuditJSONMapsWarnsOnInformationalNote(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: true}}

	// Rewrite the maps cleanly first so the quote/slash-bounded tokens are gone
	// and only an informational "notes" reference remains stale. Append the note
	// after the move+rewrite, then assert auditJSONMaps warns and does not abort.
	if err := d.gitMove(testModuleMove()); err != nil {
		t.Fatalf("gitMove: %v", err)
	}
	if err := d.rewriteTree(); err != nil {
		t.Fatalf("rewriteTree: %v", err)
	}

	// Inject a stale informational reference into spec-map.json's content. The
	// token sits inside a sentence bounded by a space and a period, the form the
	// driver provably cannot rewrite.
	specMapPath := filepath.Join(root, "tests", "spec-map.json")
	note := "{\n  \"notes\": \"the writer pkg/gateway/playground persists the row.\"\n}\n"
	writeFile(t, specMapPath, note)

	aborts, warns, err := d.auditJSONMaps()
	if err != nil {
		t.Fatalf("auditJSONMaps: %v", err)
	}
	if len(aborts) != 0 {
		t.Fatalf("informational JSON note must not abort; aborts=%v", aborts)
	}
	var sawWarn bool
	for _, w := range warns {
		if strings.HasPrefix(w, "tests/spec-map.json") && strings.Contains(w, "pkg/gateway/playground") {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Fatalf("auditJSONMaps must warn on the stale informational note; warns=%v", warns)
	}

	// The full audit must surface the warning and still pass (no abort).
	if err := d.audit(); err != nil {
		t.Fatalf("audit must pass with only an informational JSON warning; got %v", err)
	}
}

// The audit must NOT abort on a comment-form occurrence, and must record it as a
// warning. A clean post-rewrite tree with a stale comment passes the audit.
func TestDriver_AuditWarnsButDoesNotAbortOnComment(t *testing.T) {
	root := initTempRepo(t)
	// Add a Go file naming the old path only in a // diagnosis: comment.
	writeFile(t, filepath.Join(root, "pkg", "diag", "diag.go"),
		"package diag\n\n// diagnosis: a failure means pkg/gateway/playground lost a row.\nvar X = 1\n")
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-q", "-m", "diag", "--no-verify")

	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: false}}
	if err := d.execute(); err != nil {
		t.Fatalf("execute (comment must warn, not abort): %v", err)
	}
}

// The prose audit must warn on a surviving old path but must not false-positive
// on the new path (which contains the old path as a prefix substring).
func TestDriver_ProseAuditBoundaryAware(t *testing.T) {
	root := initTempRepo(t)
	// A markdown file naming ONLY the new path; must not warn.
	writeFile(t, filepath.Join(root, "docs", "new.md"),
		"The pkg/gateway/mcpfabric/playground package owns the token.\n")

	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}}
	warns, err := d.auditProseFiles()
	if err != nil {
		t.Fatalf("auditProseFiles: %v", err)
	}
	// old.md (from initTempRepo) names the OLD path -> one warn.
	// new.md names only the NEW path -> no warn.
	var oldWarned, newWarned bool
	for _, w := range warns {
		if strings.HasPrefix(w, "docs/old.md") {
			oldWarned = true
		}
		if strings.HasPrefix(w, "docs/new.md") {
			newWarned = true
		}
	}
	if !oldWarned {
		t.Errorf("prose audit should warn on docs/old.md (names old path); warns=%v", warns)
	}
	if newWarned {
		t.Errorf("prose audit must not warn on docs/new.md (names only new path); warns=%v", warns)
	}
}

func TestDriver_DryRunTouchesNothing(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{dryRun: true}}
	if err := d.execute(); err != nil {
		t.Fatalf("dry-run execute: %v", err)
	}
	// Directory unchanged.
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "playground", "token.go")); err != nil {
		t.Fatalf("dry-run moved the directory: %v", err)
	}
}

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
