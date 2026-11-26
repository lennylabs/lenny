// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"strings"
	"testing"
)

// The non-Go input list is contributor tooling for test selection (change
// 0116, CS-6), so these cases carry no specification annotation.

// TestInputPatternsMatchSegmentsAndTrees pins the pattern language of the
// list: `*` and `?` stay inside one segment, and `**` crosses segments.
func TestInputPatternsMatchSegmentsAndTrees(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"charts/lenny/**", "charts/lenny/templates/x.yaml", true},
		{"charts/lenny/**", "charts/lenny", false},
		{"charts/lenny/**", "charts/lennyx/a", false},
		{"Dockerfile", "Dockerfile", true},
		{"Dockerfile", "tests/gate/Dockerfile", false},
		{"compose/*.yml", "compose/default.yml", true},
		{"compose/*.yml", "compose/redis-cluster/x.yml", false},
		{"docs/**/index.md", "docs/a/b/index.md", true},
		{"sdks/client/?y*/**", "sdks/client/python/a.py", true},
		{"a.b", "aXb", false},
	}
	for _, tc := range cases {
		if got := matchInputPattern(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchInputPattern(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
	for _, bad := range []string{"", "/abs", "./rel"} {
		if _, err := inputPatternRegexp(bad); err == nil {
			t.Errorf("pattern %q must be refused", bad)
		}
		if matchInputPattern(bad, "abs") {
			t.Errorf("a refused pattern %q matches nothing", bad)
		}
	}
}

// TestParseNonGoInputsRefusesUnknownFieldsAndHeaders pins that a misspelled
// key or a wrong header fails rather than reading as an empty list.
func TestParseNonGoInputsRefusesUnknownFieldsAndHeaders(t *testing.T) {
	good := "kind: non-go-inputs\nversion: 1\ninputs:\n  - paths: [a/**]\n    runs: [docs]\n    reason: r\n"
	n, err := parseNonGoInputs([]byte(good))
	if err != nil || len(n.Inputs) != 1 || n.Inputs[0].Runs[0] != "docs" {
		t.Fatalf("parse good = %+v, %v", n, err)
	}
	for name, body := range map[string]string{
		"unknown field": "kind: non-go-inputs\nversion: 1\ninputs:\n  - path: [a/**]\n",
		"wrong kind":    "kind: other\nversion: 1\ninputs: []\n",
		"wrong version": "kind: non-go-inputs\nversion: 2\ninputs: []\n",
		"not yaml":      "kind: [\n",
	} {
		if _, err := parseNonGoInputs([]byte(body)); err == nil {
			t.Errorf("%s: parse succeeded", name)
		}
	}
}

// TestTargetsForUnionsMatchingRowsInOrder pins that every matching row
// contributes its targets once, and that a nil list matches nothing.
func TestTargetsForUnionsMatchingRowsInOrder(t *testing.T) {
	n := &nonGoInputs{Inputs: []nonGoInput{
		{Paths: []string{"spec/**"}, Runs: []string{"docs", "tests/conformance"}},
		{Paths: []string{"docs/**", "spec/**"}, Runs: []string{"docs", "pkg/x"}},
		{Paths: []string{"charts/**"}, Runs: []string{"e2e_kind"}},
	}}
	if got := n.targetsFor("spec/05_pools.md"); !reflect.DeepEqual(got, []string{"docs", "tests/conformance", "pkg/x"}) {
		t.Errorf("targets = %v", got)
	}
	if got := n.targetsFor("pkg/a.go"); got != nil {
		t.Errorf("an unmatched path has no targets; got %v", got)
	}
	var none *nonGoInputs
	if got := none.targetsFor("spec/x.md"); got != nil {
		t.Errorf("a nil list matched %v", got)
	}
}

// TestNonGoInputProblemsNameEachDeadRow pins the validate check: a pattern
// that matches no tracked file, a package target that is not a Go package,
// a target that is neither a tier nor a path, and a row with no reason each
// fail, and a live row passes.
func TestNonGoInputProblemsNameEachDeadRow(t *testing.T) {
	tracked := []string{"charts/lenny/Chart.yaml", "docs/index.md"}
	isPackage := func(dir string) bool { return dir == "tests/component/chart" }
	n := &nonGoInputs{Inputs: []nonGoInput{
		{Paths: []string{"charts/lenny/**"}, Runs: []string{"e2e_kind", "tests/component/chart"}, Reason: "deploys it"},
		{Paths: []string{"compose/**"}, Runs: []string{"integration"}, Reason: "dead pattern"},
		{Paths: []string{"docs/**"}, Runs: []string{"tests/component/gone"}, Reason: "missing package"},
		{Paths: []string{"docs/**"}, Runs: []string{"nightly"}, Reason: "unknown tier"},
		{Paths: []string{"docs/**"}, Runs: []string{"docs"}},
		{Paths: []string{"./docs/**"}, Runs: []string{"docs"}, Reason: "bad pattern"},
	}}
	problems := nonGoInputProblems(n, tracked, pkgDirs{isPackage: isPackage, hasTests: isPackage})
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		`row 2: pattern "compose/**" matches no tracked file`,
		`row 3: target "tests/component/gone" is not a Go package directory`,
		`row 4: target "nightly" is not a tier name`,
		"row 5 needs paths, a target under runs or tests, and a reason",
		`row 6: pattern "./docs/**" is not a repo-relative path`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "row 1") {
		t.Errorf("a live row was reported:\n%s", joined)
	}
}

// TestCommittedNonGoInputListValidates pins the committed list against the
// tree: every pattern matches a tracked file and every target exists.
func TestCommittedNonGoInputListValidates(t *testing.T) {
	if res := validateNonGoInputsFile(repoRoot()); !res.ok {
		t.Fatalf("%s: %s", res.name, res.detail)
	}
}

// TestParseNonGoInputsReadsTheTestsField pins that a row may list package
// directories under `tests`, with or without `runs`.
func TestParseNonGoInputsReadsTheTestsField(t *testing.T) {
	body := "kind: non-go-inputs\nversion: 1\ninputs:\n  - paths: [a/**]\n    tests: [pkg/x]\n    reason: r\n"
	n, err := parseNonGoInputs([]byte(body))
	if err != nil || len(n.Inputs) != 1 || !reflect.DeepEqual(n.Inputs[0].Tests, []string{"pkg/x"}) || n.Inputs[0].Runs != nil {
		t.Fatalf("parse = %+v, %v", n, err)
	}
}

// TestTestsForUnionsMatchingRowsApartFromRuns pins that testsFor returns
// the `tests` targets of every matching row once, in row order, and that
// targetsFor leaves them out.
func TestTestsForUnionsMatchingRowsApartFromRuns(t *testing.T) {
	n := &nonGoInputs{Inputs: []nonGoInput{
		{Paths: []string{"spec/**"}, Runs: []string{"docs"}, Tests: []string{"pkg/a", "pkg/b"}},
		{Paths: []string{"spec/**"}, Tests: []string{"pkg/b", "pkg/c"}},
		{Paths: []string{"charts/**"}, Tests: []string{"pkg/d"}},
	}}
	if got := n.testsFor("spec/x.md"); !reflect.DeepEqual(got, []string{"pkg/a", "pkg/b", "pkg/c"}) {
		t.Errorf("tests = %v", got)
	}
	if got := n.targetsFor("spec/x.md"); !reflect.DeepEqual(got, []string{"docs"}) {
		t.Errorf("runs targets = %v; want docs alone", got)
	}
	var none *nonGoInputs
	if got := none.testsFor("spec/x.md"); got != nil {
		t.Errorf("a nil list matched %v", got)
	}
}

// TestNonGoInputProblemsRefuseMalformedTestsTargets pins the validate check
// of the `tests` form: a tier name, a pattern, a path that is not a clean
// repo-relative directory, a directory that holds no Go package, a
// package with no test file, a target listed twice, and a directory the
// row also lists under `runs` in any spelling each fail, and a row whose
// only targets are under `tests` passes.
func TestNonGoInputProblemsRefuseMalformedTestsTargets(t *testing.T) {
	tracked := []string{"spec/a.md"}
	isPackage := func(dir string) bool { return dir == "pkg/x" || dir == "pkg/y" || dir == "pkg/helper" }
	hasTests := func(dir string) bool { return dir == "pkg/x" || dir == "pkg/y" }
	row := func(runs, tests []string) nonGoInput {
		return nonGoInput{Paths: []string{"spec/**"}, Runs: runs, Tests: tests, Reason: "reads it"}
	}
	n := &nonGoInputs{Inputs: []nonGoInput{
		row(nil, []string{"pkg/x"}),
		row(nil, []string{tierDocs}),
		row(nil, []string{"pkg/x/..."}),
		row(nil, []string{"./pkg/x"}),
		row(nil, []string{"/pkg/x"}),
		row(nil, []string{"../pkg/x"}),
		row(nil, []string{"pkg/x/"}),
		row(nil, []string{"pkg/*"}),
		row(nil, []string{"pkg/gone"}),
		row([]string{"pkg/y"}, []string{"pkg/y"}),
		row(nil, []string{""}),
		row(nil, []string{"pkg/helper"}),
		row(nil, []string{"pkg/x", "pkg/x"}),
		row([]string{"./pkg/y/..."}, []string{"pkg/y"}),
	}}
	joined := strings.Join(nonGoInputProblems(n, tracked, pkgDirs{isPackage: isPackage, hasTests: hasTests}), "\n")
	for _, want := range []string{
		`row 2: tests target "docs" is a tier name; list a tier under runs`,
		`row 3: tests target "pkg/x/..." is not a clean repo-relative package directory`,
		`row 4: tests target "./pkg/x" is not a clean repo-relative package directory`,
		`row 5: tests target "/pkg/x" is not a clean repo-relative package directory`,
		`row 6: tests target "../pkg/x" is not a clean repo-relative package directory`,
		`row 7: tests target "pkg/x/" is not a clean repo-relative package directory`,
		`row 8: tests target "pkg/*" is not a clean repo-relative package directory`,
		`row 9: tests target "pkg/gone" is not a Go package directory`,
		`row 10: target "pkg/y" is under both runs and tests`,
		`row 11: tests target "" is not a clean repo-relative package directory`,
		`row 12: tests target "pkg/helper" holds no test file, so it selects no test`,
		`row 13: tests target "pkg/x" is listed twice`,
		`row 14: target "pkg/y" is under both runs and tests`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "row 1:") || strings.Contains(joined, "row 1 ") {
		t.Errorf("a row whose only target is under tests was reported:\n%s", joined)
	}
}

// TestCommittedRowsSelectTheTiersThatReadTheRunbooksAndTheReleaseWorkflow
// pins the two rows change 0172 decision 17 completes: a runbook edit
// selects the chaos, security, and load_kind tiers, which run the image
// that copies docs/runbooks, beside the e2e_kind tier, and an edit of the
// release workflow selects the security tier, whose SBOM test reads it,
// beside the harness's tests.
func TestCommittedRowsSelectTheTiersThatReadTheRunbooksAndTheReleaseWorkflow(t *testing.T) {
	inputs, err := readNonGoInputs(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	runbook := selectChanged([]string{"docs/runbooks/index.md"}, nil, inputs, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth})
	for _, tier := range []string{tierE2EKind, tierChaos, tierSecurity, tierLoadKind, tierIntegration} {
		if s := runbook.scopes[tier]; s == nil || !s.whole {
			t.Errorf("a runbook edit does not select the %s tier whole: %+v", tier, s)
		}
	}
	release := selectChanged([]string{".github/workflows/release.yml"}, nil, inputs, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth})
	if s := release.scopes[tierSecurity]; s == nil || !s.whole {
		t.Errorf("a release workflow edit does not select the security tier: %+v", s)
	}
	if s := release.scopes[tierUnit]; s == nil || !s.pkgs["cmd/lenny-test"] {
		t.Errorf("a release workflow edit no longer selects the harness's tests: %+v", s)
	}
}
