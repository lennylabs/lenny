// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// These tests hold the move manifest of change 0117 to the gateway layout
// register. They cover the move driver's input rather than a platform
// behavior, so they carry no specification annotation. They replace the
// layout tests of proposal 0020, whose rule the layout gate now holds.

// repoRootFromTest returns the repository root from the driver package
// directory (scripts/refactor), where go test runs.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", root, err)
	}
	return root
}

// TestManifestAgreesWithTheLayoutRegister checks the committed manifest
// against tests/registers/gateway-layout.yaml.
func TestManifestAgreesWithTheLayoutRegister(t *testing.T) {
	root := repoRootFromTest(t)
	f, err := os.Open(filepath.Join(root, "scripts", "refactor", "manifest"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	moves, err := rewrite.ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := gatewaylayout.LoadRegister(filepath.Join(root, "tests", "registers", "gateway-layout.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	exists := func(rel string) bool {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil && info.IsDir()
	}
	for _, problem := range manifestProblems(moves, reg, gatewaylayout.Layout, exists, carriedRemovals) {
		t.Error(problem)
	}
}

// carriedRemovals lists each package a member change removes that moves
// with the directory it is nested in, with the step whose move carries it.
// steps.md records each one on its step: the step deletes the package's
// register row, and the package belongs to its parent's part until the
// change removes it.
var carriedRemovals = map[string]string{
	// The go:generate tool of mcptools, which 0138 removes. It moves with
	// mcptools because mcptools runs it by a relative path.
	"pkg/gateway/mcpfabric/mcptools/internal/genmcpschemas": "0117.S7",
}

// manifestProblems returns every disagreement between the manifest and the
// register:
//
//   - a move whose package is still in place must have a register row that
//     names the same step, a part other than removed, and a destination in
//     that part's directory (or, for a package leaving the gateway, a
//     concern directory under pkg/ outside pkg/gateway);
//   - a move whose package is no longer in place must have run, so its
//     destination exists;
//   - every register row of a package that moves is covered by a move of
//     the package or of the directory it is nested in, at the same step;
//   - no two moves share a destination;
//   - a package a change removes is not nested in a directory that moves,
//     unless carried lists it at the step of that move.
func manifestProblems(moves []rewrite.Move, reg gatewaylayout.Register, table []gatewaylayout.Dir, exists func(string) bool, carried map[string]string) []string {
	var out []string
	rows := map[string]gatewaylayout.PackageRow{}
	for _, row := range reg.Packages {
		rows[row.Package] = row
	}
	byOld := map[string]rewrite.Move{}
	dest := map[string]string{}
	for _, m := range moves {
		oldRel, newRel := rewrite.RepoRel(m.Old), rewrite.RepoRel(m.New)
		byOld[oldRel] = m
		if prev, dup := dest[newRel]; dup {
			out = append(out, "moves of "+prev+" and "+oldRel+" share the destination "+newRel)
		}
		dest[newRel] = oldRel
		if !exists(oldRel) {
			if !exists(newRel) {
				out = append(out, "move of "+oldRel+": neither its source nor its destination "+newRel+" exists")
			}
			continue
		}
		row, ok := rows[oldRel]
		switch {
		case !ok:
			out = append(out, "move of "+oldRel+": the package is in place and has no register row")
		case row.Part == gatewaylayout.PartRemoved:
			out = append(out, "move of "+oldRel+": the register lists it as removed by "+row.RemovedBy)
		case row.Step != m.Step:
			out = append(out, "move of "+oldRel+": the manifest names step "+m.Step+" and the register "+row.Step)
		default:
			if msg := destinationProblem(row.Part, newRel, table); msg != "" {
				out = append(out, "move of "+oldRel+": "+msg)
			}
		}
	}
	for _, row := range reg.Packages {
		if row.Part == gatewaylayout.PartRemoved {
			if m, ok := coveringMove(row.Package, byOld); ok && rewrite.RepoRel(m.Old) != row.Package && carried[row.Package] != m.Step {
				out = append(out, "register row "+row.Package+": removed by "+row.RemovedBy+", but the move of "+rewrite.RepoRel(m.Old)+" at "+m.Step+" carries it; record it on that step and in carriedRemovals, or exclude it")
			}
			continue
		}
		if m, ok := coveringMove(row.Package, byOld); !ok {
			out = append(out, "register row "+row.Package+": no manifest move carries it")
		} else if m.Step != row.Step {
			out = append(out, "register row "+row.Package+": carried by a move at step "+m.Step+", registered at "+row.Step)
		}
	}
	return out
}

// destinationProblem checks that a destination lies in the directory of the
// part a register row names.
func destinationProblem(part, newRel string, table []gatewaylayout.Dir) string {
	if part == gatewaylayout.PartDomain {
		if strings.HasPrefix(newRel, "pkg/") && !strings.HasPrefix(newRel+"/", gatewaylayout.GatewayRoot+"/") {
			return ""
		}
		return "a package leaving the gateway moves to " + newRel + ", which is not a concern directory under pkg/"
	}
	for _, d := range table {
		if d.Part == part {
			if newRel == d.Path || strings.HasPrefix(newRel, d.Path+"/") {
				return ""
			}
			return "the destination " + newRel + " is outside " + d.Path + ", the directory of part " + part
		}
	}
	return "unknown part " + part
}

// coveringMove returns the move of a package or of the nearest directory
// above it that the manifest moves.
func coveringMove(pkg string, byOld map[string]rewrite.Move) (rewrite.Move, bool) {
	for p := pkg; strings.Contains(p, "/"); p = p[:strings.LastIndex(p, "/")] {
		if m, ok := byOld[p]; ok {
			return m, true
		}
	}
	return rewrite.Move{}, false
}

// TestManifestProblemsReportsEachDisagreement pins each check of
// manifestProblems on a fixed manifest and register.
func TestManifestProblemsReportsEachDisagreement(t *testing.T) {
	const mod = "github.com/lennylabs/lenny/"
	mv := func(old, new, step string) rewrite.Move {
		return rewrite.Move{Old: mod + old, New: mod + new, Step: step}
	}
	moves := []rewrite.Move{
		mv("pkg/gateway/llmproxy/llmproxy", "pkg/gateway/meteringproxy/llmproxy", "0117.S3"),
		mv("pkg/gateway/audit/auditstore", "pkg/audit/auditstore", "0117.S9"),
		mv("pkg/gateway/quota/quotastore", "pkg/gateway/quota2/quotastore", "0117.S9"),
		mv("pkg/gateway/session/sessionevents", "pkg/gateway/core/sessionevents", "0117.S6"),
		mv("pkg/gateway/x/norow", "pkg/gateway/core/norow", "0117.S11"),
		mv("pkg/gateway/x/wrongstep", "pkg/gateway/core/wrongstep", "0117.S11"),
		mv("pkg/gateway/connectors/conn", "pkg/gateway/core/conn", "0117.S11"),
		mv("pkg/gateway/done/moved", "pkg/gateway/core/moved", "0117.S11"),
		mv("pkg/gateway/gone/lost", "pkg/gateway/core/lost", "0117.S11"),
		mv("pkg/gateway/x/dup", "pkg/gateway/core/norow", "0117.S11"),
		mv("pkg/gateway/x/badpart", "pkg/gateway/core/badpart", "0117.S11"),
	}
	reg := gatewaylayout.Register{Version: 1, Packages: []gatewaylayout.PackageRow{
		{Package: "pkg/gateway/llmproxy/llmproxy", Part: "metering-proxy", Step: "0117.S3"},
		{Package: "pkg/gateway/audit/auditstore", Part: gatewaylayout.PartDomain, Step: "0117.S9"},
		{Package: "pkg/gateway/audit/auditstore/auditbatch", Part: gatewaylayout.PartDomain, Step: "0117.S9"},
		{Package: "pkg/gateway/audit/auditstore/late", Part: gatewaylayout.PartDomain, Step: "0117.S10"},
		{Package: "pkg/gateway/quota/quotastore", Part: gatewaylayout.PartDomain, Step: "0117.S9"},
		{Package: "pkg/gateway/session/sessionevents", Part: "stream-proxy", Step: "0117.S6"},
		{Package: "pkg/gateway/x/wrongstep", Part: "core", Step: "0117.S7"},
		{Package: "pkg/gateway/connectors/conn", Part: gatewaylayout.PartRemoved, RemovedBy: "0124"},
		{Package: "pkg/gateway/audit/auditstore/legacy", Part: gatewaylayout.PartRemoved, RemovedBy: "0124"},
		{Package: "pkg/gateway/audit/auditstore/gen", Part: gatewaylayout.PartRemoved, RemovedBy: "0138"},
		{Package: "pkg/gateway/orphan/pkg", Part: "core", Step: "0117.S11"},
		{Package: "pkg/gateway/x/badpart", Part: "nowhere", Step: "0117.S11"},
		{Package: "pkg/gateway/x/dup", Part: "core", Step: "0117.S11"},
	}}
	present := map[string]bool{"pkg/gateway/core/moved": true}
	exists := func(rel string) bool {
		if strings.HasPrefix(rel, "pkg/gateway/done/") || strings.HasPrefix(rel, "pkg/gateway/gone/") {
			return false
		}
		return present[rel] || !strings.HasPrefix(rel, "pkg/gateway/core/") && !strings.HasPrefix(rel, "pkg/gateway/meteringproxy/") && !strings.HasPrefix(rel, "pkg/audit/") && !strings.HasPrefix(rel, "pkg/gateway/quota2/")
	}
	carried := map[string]string{"pkg/gateway/audit/auditstore/gen": "0117.S9"}
	got := strings.Join(manifestProblems(moves, reg, gatewaylayout.Layout, exists, carried), "\n")
	for _, want := range []string{
		"move of pkg/gateway/quota/quotastore: a package leaving the gateway moves to pkg/gateway/quota2/quotastore",
		"move of pkg/gateway/session/sessionevents: the destination pkg/gateway/core/sessionevents is outside pkg/gateway/streamproxy",
		"move of pkg/gateway/x/norow: the package is in place and has no register row",
		"move of pkg/gateway/x/wrongstep: the manifest names step 0117.S11 and the register 0117.S7",
		"move of pkg/gateway/connectors/conn: the register lists it as removed by 0124",
		"move of pkg/gateway/gone/lost: neither its source nor its destination pkg/gateway/core/lost exists",
		"moves of pkg/gateway/x/norow and pkg/gateway/x/dup share the destination pkg/gateway/core/norow",
		"move of pkg/gateway/x/badpart: unknown part nowhere",
		"register row pkg/gateway/orphan/pkg: no manifest move carries it",
		"register row pkg/gateway/audit/auditstore/late: carried by a move at step 0117.S9, registered at 0117.S10",
		"register row pkg/gateway/audit/auditstore/legacy: removed by 0124, but the move of pkg/gateway/audit/auditstore at 0117.S9 carries it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("problems lack %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"llmproxy", "auditstore:", "auditbatch", "done/moved", "auditstore/gen"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("problems report the agreeing entry %q:\n%s", unwanted, got)
		}
	}
}
