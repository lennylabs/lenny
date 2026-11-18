// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
)

// These tests cover the per-step selection and the register rewrites that
// change 0117 added to the move driver. They cover contributor tooling
// rather than a platform behavior, so they carry no specification
// annotation.

func TestFilterMovesByStepSelectsOnlyThatStepsMoves(t *testing.T) {
	moves := []rewrite.Move{
		{Old: "a", New: "b", Step: "0117.S3"},
		{Old: "c", New: "d", Step: "0117.S4"},
		{Old: "e", New: "f", Step: "0117.S3"},
		{Old: "g", New: "h"},
	}
	got := filterMovesByStep(moves, "0117.S3")
	if len(got) != 2 || got[0].Old != "a" || got[1].Old != "e" {
		t.Fatalf("filterMovesByStep = %+v", got)
	}
	if got := filterMovesByStep(moves, "0117.S9"); len(got) != 0 {
		t.Fatalf("a step with no moves selected %+v", got)
	}
}

// TestRunStepAppliesItsMovesAndRewritesTheRegisters runs one step from a
// manifest in a throwaway repository and checks that the step's move runs,
// the other step's does not, and that a YAML register keyed by package path
// takes the new path while a carrier the repository lacks is skipped.
func TestRunStepAppliesItsMovesAndRewritesTheRegisters(t *testing.T) {
	root := initTempRepo(t)
	writeFile(t, filepath.Join(root, "pkg", "gateway", "other", "o.go"), "package other\n")
	writeFile(t, filepath.Join(root, "tests", "registers", "gateway-layout.yaml"),
		"version: 1\npackages:\n  - package: pkg/gateway/playground\n    part: playground\n    step: 0117.S4\n"+
			"imports:\n  - from: pkg/gateway/playground\n    to: pkg/gateway/other\n    rule: seam-imports\n    step: 0117.S7\n")
	writeFile(t, filepath.Join(root, "tests", "registers", "spec-section-citations.yaml"),
		"packages:\n    - package: pkg/gateway/playground\n      count: 2\n    - package: pkg/gateway/playgroundx\n      count: 1\n")
	manifestPath := filepath.Join(root, "manifest")
	writeFile(t, manifestPath,
		"github.com/lennylabs/lenny/pkg/gateway/playground\tgithub.com/lennylabs/lenny/pkg/gateway/play\t0117.S4\n"+
			"github.com/lennylabs/lenny/pkg/gateway/other\tgithub.com/lennylabs/lenny/pkg/elsewhere/other\t0117.S9\n")
	mustRun(t, root, "git", "add", "-A")
	mustRun(t, root, "git", "commit", "-q", "-m", "registers", "--no-verify")

	cfg := config{manifest: manifestPath, root: root, step: "0117.S4", skipGates: true}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "play", "token.go")); err != nil {
		t.Fatalf("the step's move did not run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg", "gateway", "other", "o.go")); err != nil {
		t.Fatalf("another step's move ran: %v", err)
	}
	layout := readFile(t, filepath.Join(root, "tests", "registers", "gateway-layout.yaml"))
	for _, want := range []string{"package: pkg/gateway/play\n", "from: pkg/gateway/play\n", "to: pkg/gateway/other\n"} {
		if !strings.Contains(layout, want) {
			t.Errorf("gateway-layout.yaml lacks %q:\n%s", want, layout)
		}
	}
	citations := readFile(t, filepath.Join(root, "tests", "registers", "spec-section-citations.yaml"))
	if !strings.Contains(citations, "package: pkg/gateway/play\n") || !strings.Contains(citations, "package: pkg/gateway/playgroundx\n") {
		t.Errorf("spec-section-citations.yaml not rewritten on a token boundary:\n%s", citations)
	}
}

func TestRunStepWithNoMovesErrors(t *testing.T) {
	root := initTempRepo(t)
	manifestPath := filepath.Join(root, "manifest")
	writeFile(t, manifestPath,
		"github.com/lennylabs/lenny/pkg/gateway/playground\tgithub.com/lennylabs/lenny/pkg/gateway/play\t0117.S4\n")
	cfg := config{manifest: manifestPath, root: root, step: "0117.S5", skipGates: true}
	if err := run(cfg); err == nil || !strings.Contains(err.Error(), `no manifest move names -step "0117.S5"`) {
		t.Fatalf("expected a -step no-match error, got %v", err)
	}
}
