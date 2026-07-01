// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

// spec: §4.1 (the driver executes the gateway regroup reproducibly).

func TestFilterMoves(t *testing.T) {
	moves := []rewrite.Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/mcp", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/playground", New: "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"},
	}
	got := filterMoves(moves, "github.com/lennylabs/lenny/pkg/gateway/playground")
	if len(got) != 1 || got[0].Old != "github.com/lennylabs/lenny/pkg/gateway/playground" {
		t.Fatalf("filterMoves returned %+v", got)
	}
	if filtered := filterMoves(moves, "github.com/lennylabs/lenny/pkg/gateway/missing"); len(filtered) != 0 {
		t.Fatalf("filterMoves on missing path returned %+v, want empty", filtered)
	}
}

// filterMovesByGroup selects exactly the moves whose destination group matches,
// the per-group landing unit the proposal partitions the manifest by (§2, §4 C3:
// "each group move is applied, verified green, and committed before the next").
// A nested destination (breakerstore -> middleware/circuitbreaker/breakerstore)
// groups under its first segment ("middleware"), not its leaf.
func TestFilterMovesByGroupAndGroupOf(t *testing.T) {
	moves := []rewrite.Move{
		{Old: "github.com/lennylabs/lenny/pkg/gateway/leasestore", New: "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/sqlitestore", New: "github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/sessionstore", New: "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"},
		{Old: "github.com/lennylabs/lenny/pkg/gateway/breakerstore", New: "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"},
	}

	storage := filterMovesByGroup(moves, "storage")
	if len(storage) != 2 {
		t.Fatalf("storage group should have 2 moves, got %d: %+v", len(storage), storage)
	}
	for _, m := range storage {
		if groupOf(m.New) != "storage" {
			t.Errorf("move %s grouped as %q, want storage", m.Old, groupOf(m.New))
		}
	}

	if got := filterMovesByGroup(moves, "session"); len(got) != 1 || got[0].Old != "github.com/lennylabs/lenny/pkg/gateway/sessionstore" {
		t.Fatalf("session group filter returned %+v", got)
	}

	// A nested destination groups under its first segment, not its leaf.
	if g := groupOf("github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker/breakerstore"); g != "middleware" {
		t.Fatalf("groupOf(breakerstore dest) = %q, want middleware", g)
	}
	if got := filterMovesByGroup(moves, "middleware"); len(got) != 1 {
		t.Fatalf("middleware group filter returned %+v", got)
	}

	// An unknown group selects nothing, so run reports the no-match error.
	if got := filterMovesByGroup(moves, "nope"); len(got) != 0 {
		t.Fatalf("unknown group should select nothing, got %+v", got)
	}
	// A non-gateway destination yields the empty group.
	if g := groupOf("github.com/lennylabs/lenny/pkg/events"); g != "" {
		t.Fatalf("groupOf(non-gateway) = %q, want empty", g)
	}
}

func TestResolveRoot_ExplicitFlag(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveRoot(dir)
	if err != nil {
		t.Fatalf("resolveRoot: %v", err)
	}
	// On macOS the temp dir resolves through /private; compare the base.
	if filepath.Base(got) != filepath.Base(dir) {
		t.Fatalf("resolveRoot(%q) = %q", dir, got)
	}
}

// run wires manifest parsing, the -only filter, and execute together. Drive it
// against the temp repo with gates skipped via a committed single-line manifest.
func TestRun_OnlyFilterAndExecute(t *testing.T) {
	root := initTempRepo(t)
	manifestPath := filepath.Join(root, "manifest")
	writeFile(t, manifestPath,
		"# test manifest\n"+
			"github.com/lennylabs/lenny/pkg/gateway/playground\tgithub.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground\n")
	// Commit the manifest so the working tree is clean when execute runs; the
	// driver refuses to start on a dirty tree (requireCleanTree) so the
	// automatic rollback can revert exactly its own changes.
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-q", "-m", "manifest", "--no-verify")

	cfg := config{
		manifest:  manifestPath,
		root:      root,
		only:      "github.com/lennylabs/lenny/pkg/gateway/playground",
		skipGates: true,
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "mcpfabric", "playground", "token.go")); err != nil {
		t.Fatalf("run did not apply the move: %v", err)
	}
}

func TestRun_OnlyNoMatchErrors(t *testing.T) {
	root := initTempRepo(t)
	manifestPath := filepath.Join(root, "manifest")
	writeFile(t, manifestPath,
		"github.com/lennylabs/lenny/pkg/gateway/playground\tgithub.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground\n")
	cfg := config{manifest: manifestPath, root: root, only: "github.com/lennylabs/lenny/pkg/gateway/nope", skipGates: true}
	if err := run(cfg); err == nil || !strings.Contains(err.Error(), "no manifest move matches") {
		t.Fatalf("expected -only no-match error, got %v", err)
	}
}

func TestRun_GroupNoMatchErrors(t *testing.T) {
	root := initTempRepo(t)
	manifestPath := filepath.Join(root, "manifest")
	writeFile(t, manifestPath,
		"github.com/lennylabs/lenny/pkg/gateway/playground\tgithub.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground\n")
	cfg := config{manifest: manifestPath, root: root, group: "nope", skipGates: true, skipAudit: true}
	if err := run(cfg); err == nil || !strings.Contains(err.Error(), "no manifest move targets") {
		t.Fatalf("expected -group no-match error, got %v", err)
	}
}

func TestRun_MissingManifestErrors(t *testing.T) {
	root := t.TempDir()
	cfg := config{manifest: filepath.Join(root, "absent"), root: root, skipGates: true}
	if err := run(cfg); err == nil || !strings.Contains(err.Error(), "open manifest") {
		t.Fatalf("expected open-manifest error, got %v", err)
	}
}

// runGoGates is the tier-0 go-toolchain gate (build, vet, list). Drive it
// against a minimal valid module (passes) and a module with a compile error
// (aborts), so the gate-abort path the driver relies on is exercised.
func TestRunGoGates_PassAndFail(t *testing.T) {
	pass := t.TempDir()
	writeFile(t, filepath.Join(pass, "go.mod"), "module gatetest\n\ngo 1.25\n")
	writeFile(t, filepath.Join(pass, "main.go"), "package main\n\nfunc main() {}\n")
	if err := (&driver{root: pass}).runGoGates(); err != nil {
		t.Fatalf("runGoGates on a valid module should pass: %v", err)
	}

	fail := t.TempDir()
	writeFile(t, filepath.Join(fail, "go.mod"), "module gatefail\n\ngo 1.25\n")
	writeFile(t, filepath.Join(fail, "main.go"), "package main\n\nfunc main() { undefinedSymbol() }\n")
	if err := (&driver{root: fail}).runGoGates(); err == nil {
		t.Fatal("runGoGates on a broken module should abort")
	}
}

// The format pass is scoped to exactly the *.go files the rewrite modified
// (proposal §2 step (4), §5 non-goal: no reformatting outside the change). Drive
// the tree rewrite over a repo holding one importer of the moved package and one
// untouched file that names no moved path, then assert touchedGoFiles lists only
// the importer (and the moved package's own files when their content changed),
// never the untouched file. This pins the corrected scope so a regression back to
// formatting whole pkg/cmd/tests trees fails here.
func TestTouchedGoFilesScopedToRewrittenFiles(t *testing.T) {
	root := initTempRepo(t)
	d := &driver{root: root, moves: []rewrite.Move{testModuleMove()}, cfg: config{skipGates: true, skipAudit: true}}

	// An untouched Go file that names no moved path; it must never be formatted.
	untouched := filepath.Join(root, "pkg", "untouched", "untouched.go")
	writeFile(t, untouched, "package untouched\n\nvar Y = 2\n")
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-q", "-m", "untouched", "--no-verify")

	if err := d.gitMove(testModuleMove()); err != nil {
		t.Fatalf("gitMove: %v", err)
	}
	if err := d.rewriteTree(); err != nil {
		t.Fatalf("rewriteTree: %v", err)
	}

	touched := d.touchedGoFiles()
	consumer := filepath.Join(root, "pkg", "consumer", "consumer.go")
	var sawConsumer, sawUntouched bool
	for _, p := range touched {
		if p == consumer {
			sawConsumer = true
		}
		if p == untouched {
			sawUntouched = true
		}
	}
	if !sawConsumer {
		t.Errorf("consumer.go (rewritten) should be in the touched set; got %v", touched)
	}
	if sawUntouched {
		t.Errorf("untouched.go must not be in the format set; got %v", touched)
	}
}
