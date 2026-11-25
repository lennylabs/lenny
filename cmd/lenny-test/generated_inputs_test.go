// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/gate"
)

// The generated rows of tests/non-go-inputs.yaml are contributor tooling
// (change 0172, decision 26), so these cases carry no specification
// annotation.

// generatedFixtureInputs is a list with a broad chart row, a generated row
// for one file under it, and a generated row for a file an embedding
// package owns.
func generatedFixtureInputs() *nonGoInputs {
	return &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: []nonGoInput{
		{Paths: []string{"charts/lenny/**"}, Runs: []string{tierE2EKind, "pkg/helper"}, Tests: []string{"tests/component/leafc"}, Reason: "deploys it"},
		{Paths: []string{"charts/lenny/files/rules.yaml"}, Generated: true, Runs: []string{tierDocs}, Tests: []string{"tests/contract/midc"}, Reason: "the generator check reads it"},
		{Paths: []string{"pkg/assets/sub/x.tmpl"}, Generated: true, Tests: []string{"pkg/assets"}, Reason: "the generator check reads it"},
	}}
}

// TestAGeneratedFileSelectsOnlyItsCheck pins that a changed path a
// generated row matches selects that row's targets, and neither the
// broader row that also matches it nor the packages that own or embed it,
// while a hand-written file under the broader row keeps the broader row.
func TestAGeneratedFileSelectsOnlyItsCheck(t *testing.T) {
	plan := planOf(selectChanged([]string{"charts/lenny/files/rules.yaml"}, fixtureGraph(), generatedFixtureInputs(), specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if got, want := tierNamesOf(plan), []string{tierStatic, tierContract, tierDocs}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v", got, want)
	}
	if got := plan[tierContract].pkgs; !reflect.DeepEqual(got, []string{"./tests/contract/midc"}) {
		t.Errorf("contract scope = %v, want the generator check's package alone", got)
	}

	hand := planOf(selectChanged([]string{"charts/lenny/templates/x.yaml"}, fixtureGraph(), generatedFixtureInputs(), specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if _, ok := hand[tierE2EKind]; !ok {
		t.Errorf("a hand-written chart file lost the chart row's tiers: %v", tierNamesOf(hand))
	}
	if got := hand[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./tests/component/leafc"}) {
		t.Errorf("a hand-written chart file lost the chart row's tests target: %v", got)
	}

	// pkg/assets embeds pkg/assets/sub/x.tmpl, and pkg/assets/sub owns it
	// by directory; the generated row runs pkg/assets alone.
	embedded := selectChanged([]string{"pkg/assets/sub/x.tmpl"}, fixtureGraph(), generatedFixtureInputs(), specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth})
	if got := planOf(embedded)[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/assets"}) {
		t.Errorf("unit scope of a generated embedded file = %v, want the row's package alone", got)
	}
	if len(embedded.unowned) != 0 {
		t.Errorf("a generated file was reported as unowned: %v", embedded.unowned)
	}
}

// TestAGeneratedRowNarrowsOnlyOnceTheBaseCarriesIt pins the union with the
// base's rows: a generated row that the branch adds leaves the branch's
// own selection of the file as the base's rows make it, and narrows it
// once the base carries the row.
func TestAGeneratedRowNarrowsOnlyOnceTheBaseCarriesIt(t *testing.T) {
	head := generatedFixtureInputs()
	base := &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: head.Inputs[:1]}
	added := planOf(selectChanged([]string{"charts/lenny/files/rules.yaml"}, fixtureGraph(), head.union(base), specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if _, ok := added[tierE2EKind]; !ok {
		t.Errorf("a generated row the branch adds narrowed the branch's own selection: %v", tierNamesOf(added))
	}
	merged := planOf(selectChanged([]string{"charts/lenny/files/rules.yaml"}, fixtureGraph(), head.union(generatedFixtureInputs()), specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if _, ok := merged[tierE2EKind]; ok {
		t.Errorf("a generated row the base carries did not narrow the selection: %v", tierNamesOf(merged))
	}
}

// TestGeneratedRowProblemsRefuseRowsThatCannotStandForTheirFiles pins the
// checks `lenny-test validate` runs on a generated row: a wildcard, a file
// that is neither generated nor a register a run writes back, a package
// under runs, and a file inside a package the row does not list under
// tests are each refused, and a well-formed row is not.
func TestGeneratedRowProblemsRefuseRowsThatCannotStandForTheirFiles(t *testing.T) {
	n := &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: []nonGoInput{
		{Paths: []string{"gen/a.yaml"}, Generated: true, Runs: []string{tierStatic}, Reason: "ok"},
		{Paths: []string{"gen/*.yaml"}, Generated: true, Runs: []string{tierStatic}, Reason: "wildcard"},
		{Paths: []string{"gen/hand.yaml"}, Generated: true, Runs: []string{tierStatic}, Reason: "hand-written"},
		{Paths: []string{"gen/a.yaml"}, Generated: true, Runs: []string{"pkg/x"}, Reason: "package under runs"},
		{Paths: []string{"pkg/x/data.yaml"}, Generated: true, Runs: []string{tierDocs}, Reason: "owner missing"},
		{Paths: []string{"pkg/x/data.yaml"}, Generated: true, Tests: []string{"pkg/x"}, Reason: "owner listed"},
	}}
	tracked := []string{"gen/a.yaml", "gen/hand.yaml", "pkg/x/data.yaml", "pkg/x/x.go", "pkg/x/x_test.go"}
	isPackage := func(dir string) bool { return dir == "pkg/x" }
	generated := func(p string) (bool, error) { return p != "gen/hand.yaml", nil }
	joined := strings.Join(nonGoInputProblems(n, tracked, pkgDirs{isPackage: isPackage, hasTests: isPackage, generated: generated}), "\n")
	for _, want := range []string{
		`row 2: a generated row's pattern "gen/*.yaml" is not a literal file path`,
		`row 3: "gen/hand.yaml" is neither a generated file nor a register a run writes back`,
		`row 4: a generated row lists "pkg/x" under runs`,
		`row 5: "pkg/x/data.yaml" lies in the Go package pkg/x, which a generated row lists under tests`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	for _, row := range []string{"row 1:", "row 6:"} {
		if strings.Contains(joined, row) {
			t.Errorf("a well-formed generated row was reported (%s):\n%s", row, joined)
		}
	}
	// Without a classifier, every file of a generated row is refused.
	none := strings.Join(nonGoInputProblems(&nonGoInputs{Inputs: n.Inputs[:1]}, tracked, pkgDirs{isPackage: isPackage, hasTests: isPackage}), "\n")
	if !strings.Contains(none, "neither a generated file") {
		t.Errorf("a generated row was accepted with no classifier: %q", none)
	}
}

// TestIsGeneratedInputReadsMarkersAndTheWriteBackRegisters pins the
// classifier validate uses: a file with a generation header is generated, a
// register a static-tier check writes back is accepted, and a hand-written
// file and a missing file are not.
func TestIsGeneratedInputReadsMarkersAndTheWriteBackRegisters(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("gen/a.yaml", "# Code generated by cmd/gen. DO NOT EDIT.\nkey: 1\n")
	write("gen/hand.yaml", "# Written by hand.\nkey: 1\n")
	for p, want := range map[string]bool{"gen/a.yaml": true, "gen/hand.yaml": false, annotation.RatchetBaselinePath: true} {
		got, err := isGeneratedInput(root, p)
		if err != nil {
			t.Fatalf("isGeneratedInput(%s): %v", p, err)
		}
		if got != want {
			t.Errorf("isGeneratedInput(%s) = %v, want %v", p, got, want)
		}
	}
	if _, err := isGeneratedInput(root, "gen/missing.yaml"); err == nil {
		t.Error("a missing file was classified without an error")
	}
}

// TestTheWriteBackRegistersAreTheRatchetsOwnFiles pins the register paths
// against the constants of the checks that write them, so a moved register
// cannot leave a stale path in the list.
func TestTheWriteBackRegistersAreTheRatchetsOwnFiles(t *testing.T) {
	for _, p := range []string{annotation.RatchetBaselinePath, gate.RatchetBaselinePath, golangciLintRegister} {
		if _, ok := writeBackRegisters[p]; !ok {
			t.Errorf("writeBackRegisters lacks %s", p)
		}
	}
	if len(writeBackRegisters) != 3 {
		t.Errorf("writeBackRegisters = %v, want the three registers a static-tier check writes back", writeBackRegisters)
	}
}

// TestCommittedGeneratedRowsSelectTheirChecks pins the committed rows of
// the files decision 26 names: the alert rules select the catalog
// cross-check and the documentation tier, the embedded manifests select
// their package's own tests and the documentation tier, and the
// write-back registers select the static tier alone, with none of them
// reaching the chart's readers, the embedding package's importers, or the
// specification tooling.
func TestCommittedGeneratedRowsSelectTheirChecks(t *testing.T) {
	inputs, err := readNonGoInputs(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path      string
		tiers     []string
		component []string
		unit      []string
	}{
		{"charts/lenny/files/alerting-rules.yaml", []string{tierStatic, tierComponent, tierDocs}, []string{"tests/component/observability"}, nil},
		{"docs/alerting/rules.yaml", []string{tierStatic, tierComponent, tierDocs}, []string{"tests/component/observability"}, nil},
		{"pkg/embedded/manifests/manifests.yaml", []string{tierStatic, tierUnit, tierDocs}, nil, []string{"pkg/embedded/manifests"}},
		{"tests/registers/spec-section-citations.yaml", []string{tierStatic}, nil, nil},
		{"tests/registers/line-citations.yaml", []string{tierStatic}, nil, nil},
		{"tests/registers/golangci-lint-findings.yaml", []string{tierStatic}, nil, nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if !inputs.generated(tc.path) {
				t.Fatalf("no generated row matches %s", tc.path)
			}
			sel := selectChanged([]string{tc.path}, nil, inputs, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth})
			plan := planOf(sel)
			if got := tierNamesOf(plan); !reflect.DeepEqual(got, tc.tiers) {
				t.Errorf("tiers = %v, want %v", got, tc.tiers)
			}
			if s := sel.scopes[tierComponent]; tc.component != nil && (s == nil || !reflect.DeepEqual(sortedKeys(s.pkgs), tc.component)) {
				t.Errorf("component scope = %+v, want %v", s, tc.component)
			}
			if s := sel.scopes[tierUnit]; tc.unit != nil && (s == nil || !reflect.DeepEqual(sortedKeys(s.pkgs), tc.unit)) {
				t.Errorf("unit scope = %+v, want %v", s, tc.unit)
			}
		})
	}
}
