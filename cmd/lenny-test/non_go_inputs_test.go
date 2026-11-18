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
	problems := nonGoInputProblems(n, tracked, isPackage)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		`row 2: pattern "compose/**" matches no tracked file`,
		`row 3: target "tests/component/gone" is not a Go package directory`,
		`row 4: target "nightly" is not a tier name`,
		"row 5 needs paths, runs, and a reason",
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
