// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
)

// The specification model, the coverage view, and the selection by
// specification are contributor tooling over the annotation rule of
// .claude/rules/spec-annotations.md (change 0116), so these cases carry
// no specification annotation.

// sec is the section sign, joined at run time so the citation gates do
// not read the fixtures below as citations of the repository's
// specification.
const sec = "\u00a7"

// specTree is an in-memory tree for the specification model.
type specTree map[string]string

func (m specTree) list(context.Context) ([]string, error) {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	return out, nil
}

func (m specTree) read(p string) ([]byte, error) {
	s, ok := m[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(s), nil
}

// specFixture is a tree with a registry of two pages, a numbered file
// whose section 5.1 is a full stub listing POOL-R5 and whose section 5.2
// has not migrated, issue records, and annotated code and tests across
// the unit, component, and contract tiers.
func specFixture() specTree {
	registry := `{
  "version": 1,
  "pages": [
    {"code": "POOL", "path": "spec/capabilities/pools/pool.md", "type": "capability", "status": "partial", "aliases": ["POOLS"]},
    {"code": "AUDIT", "path": "spec/components/audit.md", "type": "component", "status": "placeholder"}
  ],
  "requirements": [
    {"id": "POOL-R5", "anchor": "pool-r5", "file": "spec/capabilities/pools/pool.md"},
    {"id": "POOL-R6", "anchor": "pool-r6", "file": "spec/capabilities/pools/pool.md"}
  ],
  "operations": [
    {"id": "POOL-claim", "anchor": "pool-claim", "file": "spec/capabilities/pools/pool.md"}
  ],
  "terms": [],
  "blocks": [
    {"kind": "setting", "name": "minWarm", "anchor": "setting-minwarm", "file": "spec/capabilities/pools/pool.md"}
  ],
  "retired": [],
  "moves": []
}`
	numbered := strings.Join([]string{
		"# 5. Pools",
		"",
		"## 5.1 Warm pools",
		"",
		"<!-- successor-stub -->",
		"",
		"The content of this section has moved to these pages and identifiers:",
		"",
		"- [POOL-R5](capabilities/pools/pool.md#pool-r5): Claim order",
		"- [POOL](capabilities/pools/pool.md): Pools",
		"",
		"## 5.2 Sizing",
		"",
		"Text.",
		"",
		"### 5.2.1 Floors",
		"",
		"Text.",
		"",
		"## 5.3 Unused",
		"",
		"Text.",
	}, "\n")
	return specTree{
		"spec/registry.json":              registry,
		"spec/05_pools.md":                numbered,
		"spec/capabilities/pools/pool.md": "---\ncode: POOL\ntype: capability\narea: runtimes-and-pools\nsummary: Pools.\n---\n\n# Pools\n",
		"spec/components/audit.md":        "---\ncode: AUDIT\ntype: component\nsummary: Audit.\n---\n\n# Audit\n",
		"issues/POOL/POOL-I1-floor.md":    "---\nid: POOL-I1\nstatus: open\nrequirements: [POOL-R6]\n---\n\n# Floor\n",
		"issues/POOL/POOL-I2-plan.md":     "---\nid: POOL-I2\nstatus: planned\nrequirements: [POOL-R6, POOL-R5]\n---\n",
		"issues/closed/POOL/POOL-I3.md":   "---\nid: POOL-I3\nstatus: closed\nrequirements: [POOL-R6]\n---\n",
		"issues/README.md":                "# Issues\n",
		"pkg/pool/doc.go":                 "// Package pool claims pods.\n//\n// spec: POOL\npackage pool\n",
		"pkg/pool/claim.go":               "package pool\n\n// Claim orders claims.\n//\n// spec: POOL-R5\nfunc Claim() {}\n",
		"pkg/pool/claim_test.go": "package pool\n\nimport \"testing\"\n\n" +
			"// spec: POOL-R5\nfunc TestClaimOrder(t *testing.T) {}\n\n" +
			"// spec: " + sec + "5.1 (Warm pools)\nfunc TestWarmPool(t *testing.T) {}\n\n" +
			"// spec: " + sec + "5.2.1 (Floors), " + sec + "9.9 (Nowhere)\nfunc TestFloor(t *testing.T) {}\n\n" +
			"// spec: NOPE-R1\nfunc TestUnknown(t *testing.T) {}\n\n" +
			"func TestPlain(t *testing.T) {}\n",
		"tests/component/pool/pool_test.go": "package pool\n\nimport \"testing\"\n\n// spec: POOL-R5, " + sec + "5.1\nfunc TestComponentClaim(t *testing.T) {}\n",
		"tests/contract/pool/pool_test.go":  "package pool\n\nimport \"testing\"\n\n// spec: POOL\nfunc TestPoolContract(t *testing.T) {}\n\nfunc TestBare(t *testing.T) {}\n",
	}
}

func fixtureModel(t *testing.T, tree specTree) *specModel {
	t.Helper()
	m, err := buildSpecModel(context.Background(), tree.list, tree.read)
	if err != nil {
		t.Fatalf("buildSpecModel: %v", err)
	}
	return m
}

func testNames(refs []testRef) []string {
	out := []string{}
	for _, r := range refs {
		s := r.Tier + " " + r.Test
		if r.Section != "" {
			s += " " + r.Section
		}
		out = append(out, s)
	}
	return out
}

func TestCoverageOfAnIdentifierListsDirectAndIndirectTestsCodeAndIssues(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{queries: []string{"POOL-R5"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Items) != 1 {
		t.Fatalf("items: %+v", rep.Items)
	}
	it := rep.Items[0]
	if want := []string{"unit pkg/pool/claim_test.go::TestClaimOrder", "component tests/component/pool/pool_test.go::TestComponentClaim"}; !reflect.DeepEqual(testNames(it.Tests), want) {
		t.Errorf("direct tests: got %v, want %v", testNames(it.Tests), want)
	}
	// TestComponentClaim cites both POOL-R5 and the stub; it is listed once, as direct.
	if want := []string{"unit pkg/pool/claim_test.go::TestWarmPool " + sec + "5.1"}; !reflect.DeepEqual(testNames(it.Indirect), want) {
		t.Errorf("indirect tests: got %v, want %v", testNames(it.Indirect), want)
	}
	if want := []string{"pkg/pool/claim.go:5 Claim"}; !reflect.DeepEqual(it.Code, want) {
		t.Errorf("code: got %v, want %v", it.Code, want)
	}
	if want := []string{"POOL-I2 issues/POOL/POOL-I2-plan.md (planned)"}; !reflect.DeepEqual(it.Issues, want) {
		t.Errorf("issues: got %v, want %v", it.Issues, want)
	}
	if !it.Covered || it.Page != "POOL" || it.Kind != itemRequirement {
		t.Errorf("row: %+v", it)
	}
}

func TestCoverageOfAnUncoveredIdentifierNamesItsOpenIssuesOnly(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{queries: []string{"pool-r6"}})
	if err != nil {
		t.Fatal(err)
	}
	it := rep.Items[0]
	if it.Covered || len(it.Tests) != 0 || len(it.Indirect) != 0 {
		t.Fatalf("POOL-R6 has no citing test: %+v", it)
	}
	if want := []string{"POOL-I1 issues/POOL/POOL-I1-floor.md (open)", "POOL-I2 issues/POOL/POOL-I2-plan.md (planned)"}; !reflect.DeepEqual(it.Issues, want) {
		t.Errorf("issues: got %v, want %v (a closed issue is left out)", it.Issues, want)
	}
	if rep.Rows != 1 || rep.Covered != 0 || rep.Rate != 0 {
		t.Errorf("tally: %d of %d, rate %v", rep.Covered, rep.Rows, rep.Rate)
	}
}

func TestCoverageOfASectionCountsItsSubsections(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{queries: []string{sec + "5.2", "5.3"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sections) != 2 {
		t.Fatalf("sections: %+v", rep.Sections)
	}
	if want := []string{"unit pkg/pool/claim_test.go::TestFloor"}; !reflect.DeepEqual(testNames(rep.Sections[0].Tests), want) {
		t.Errorf("5.2 tests: got %v, want %v", testNames(rep.Sections[0].Tests), want)
	}
	if rep.Sections[1].Covered {
		t.Errorf("5.3 has no citing test: %+v", rep.Sections[1])
	}
	if rep.Rows != 2 || rep.Covered != 1 || rep.Rate != 0.5 {
		t.Errorf("tally: %d of %d, rate %v", rep.Covered, rep.Rows, rep.Rate)
	}
}

func TestCoverageOfAStubbedSectionAddsTheIdentifiersItLists(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{queries: []string{sec + "5.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sections) != 1 || rep.Sections[0].Stub != "full" || !reflect.DeepEqual(rep.Sections[0].Successors, []string{"POOL-R5"}) {
		t.Fatalf("section 5.1: %+v", rep.Sections)
	}
	if len(rep.Items) != 1 || rep.Items[0].ID != "POOL-R5" {
		t.Fatalf("the stub's identifiers join the report: %+v", rep.Items)
	}
}

func TestCoverageOfAPageListsTheCodeCitationsAndItsItems(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{queries: []string{"POOLS"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Pages) != 1 {
		t.Fatalf("pages: %+v", rep.Pages)
	}
	p := rep.Pages[0]
	if p.Code != "POOL" || p.Area != "runtimes-and-pools" {
		t.Errorf("an alias resolves to its page: %+v", p)
	}
	if want := []string{"POOL-R5", "POOL-R6", "POOL-claim", "setting-minwarm"}; !reflect.DeepEqual(p.Items, want) {
		t.Errorf("page items: got %v, want %v", p.Items, want)
	}
	if want := []string{"pkg/pool/doc.go:3 package pool"}; !reflect.DeepEqual(p.Sites, want) {
		t.Errorf("code sites: got %v, want %v", p.Sites, want)
	}
	if want := []string{"contract tests/contract/pool/pool_test.go::TestPoolContract"}; !reflect.DeepEqual(testNames(p.Tests), want) {
		t.Errorf("tests citing the code: got %v, want %v", testNames(p.Tests), want)
	}
	if len(rep.Items) != 4 {
		t.Errorf("the page's items join the report: %d", len(rep.Items))
	}
}

func TestCoverageOfAnAreaSelectsItsPages(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	for _, area := range []string{"runtimes-and-pools", "POOL"} {
		rep, err := m.coverage(specCoverageOptions{area: area})
		if err != nil {
			t.Fatalf("%s: %v", area, err)
		}
		if len(rep.Pages) != 1 || rep.Pages[0].Code != "POOL" || len(rep.Sections) != 0 {
			t.Errorf("%s: pages %+v, sections %d", area, rep.Pages, len(rep.Sections))
		}
	}
	if _, err := m.coverage(specCoverageOptions{area: "nowhere"}); err == nil {
		t.Error("an unknown area is an error")
	}
	if _, err := m.coverage(specCoverageOptions{area: "AUDIT"}); err == nil {
		t.Error("a page with no area names no area")
	}
}

func TestCoverageRefusesAnUnknownQuery(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	for _, q := range []string{"POOL-R99", sec + "7.1", "nope"} {
		if _, err := m.coverage(specCoverageOptions{queries: []string{q}}); err == nil {
			t.Errorf("%q resolves to nothing and is refused", q)
		}
	}
}

func TestFullCoverageReportsUnresolvedCitationsAndUnannotatedTests(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	rep, err := m.coverage(specCoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var numbers []string
	for _, s := range rep.Sections {
		numbers = append(numbers, s.Number)
	}
	if want := []string{"5", "5.2", "5.2.1", "5.3"}; !reflect.DeepEqual(numbers, want) {
		t.Errorf("sections: got %v, want %v (the full stub 5.1 is reported through its identifiers)", numbers, want)
	}
	if len(rep.Pages) != 2 || len(rep.Items) != 4 {
		t.Errorf("pages %d, items %d", len(rep.Pages), len(rep.Items))
	}
	want := []unresolvedCitation{{Cited: sec + "9.9", Sites: []string{"pkg/pool/claim_test.go:11 TestFloor"}}}
	if !reflect.DeepEqual(rep.UnresolvedSections, want) {
		t.Errorf("unresolved sections: got %+v, want %+v", rep.UnresolvedSections, want)
	}
	want = []unresolvedCitation{{Cited: "NOPE-R1", Sites: []string{"pkg/pool/claim_test.go:14 TestUnknown"}}}
	if !reflect.DeepEqual(rep.UnresolvedIdentifiers, want) {
		t.Errorf("unresolved identifiers: got %+v, want %+v", rep.UnresolvedIdentifiers, want)
	}
	u := rep.Unannotated
	if u == nil || u.Total != 2 || u.Tests != 8 || u.ByTier["unit"] != 1 || u.ByTier["contract"] != 1 || u.ByPackage["pkg/pool"] != 1 {
		t.Errorf("unannotated: %+v", u)
	}
	// Rows: four items (POOL-R5 covered) and four sections (5, 5.2, and
	// 5.2.1 covered through TestFloor or a subsection).
	if rep.Rows != 8 || rep.Covered != 4 {
		t.Errorf("tally: %d of %d", rep.Covered, rep.Rows)
	}
}

func TestModelWithoutARegistryStillReportsNumberedSections(t *testing.T) {
	t.Parallel()
	tree := specFixture()
	delete(tree, "spec/registry.json")
	m := fixtureModel(t, tree)
	rep, err := m.coverage(specCoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Pages) != 0 || len(rep.Items) != 0 || len(rep.UnresolvedIdentifiers) != 0 || len(rep.Notes) != 1 {
		t.Errorf("no registry: pages %d, items %d, unresolved identifiers %d, notes %v", len(rep.Pages), len(rep.Items), len(rep.UnresolvedIdentifiers), rep.Notes)
	}
	if len(rep.Sections) != 4 {
		t.Errorf("sections: %d", len(rep.Sections))
	}
}

func TestModelRefusesARegistryThatDoesNotParse(t *testing.T) {
	t.Parallel()
	tree := specFixture()
	tree["spec/registry.json"] = "{"
	if _, err := buildSpecModel(context.Background(), tree.list, tree.read); err == nil {
		t.Fatal("a registry that does not parse is an error")
	}
	tree = specFixture()
	tree["issues/POOL/POOL-I9.md"] = "---\nrequirements: [unclosed\n---\n"
	if _, err := buildSpecModel(context.Background(), tree.list, tree.read); err == nil {
		t.Fatal("an issue whose front matter does not parse is an error")
	}
}

func TestWriteSpecCoverageAppliesTheThresholdAndRendersJSON(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	var out, errOut bytes.Buffer
	if code := writeSpecCoverage(&out, &errOut, m, specCoverageOptions{threshold: 0.4}); code != 0 {
		t.Fatalf("rate 0.5 meets 0.4: exit %d, %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "spec coverage: 4 of 8 rows") || !strings.Contains(out.String(), "Test functions with no annotation: 2 of 8") {
		t.Errorf("summary text:\n%s", out.String())
	}
	out.Reset()
	if code := writeSpecCoverage(&out, &errOut, m, specCoverageOptions{threshold: 0.6}); code != 1 {
		t.Fatalf("rate 0.5 misses 0.6: exit %d", code)
	}
	out.Reset()
	if code := writeSpecCoverage(&out, &errOut, m, specCoverageOptions{queries: []string{"POOL-R5"}, jsonOut: true}); code != 0 {
		t.Fatalf("json: exit %d", code)
	}
	var doc coverageReport
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("json output does not parse: %v\n%s", err, out.String())
	}
	if len(doc.Items) != 1 || doc.Items[0].ID != "POOL-R5" || len(doc.Items[0].Indirect) != 1 {
		t.Errorf("json items: %+v", doc.Items)
	}
	out.Reset()
	if code := writeSpecCoverage(&out, &errOut, m, specCoverageOptions{queries: []string{"POOL-R5", sec + "5.1", "POOL"}}); code != 0 {
		t.Fatalf("detail: exit %d", code)
	}
	for _, want := range []string{"Tests reaching it through a successor stub:", "(through " + sec + "5.1)", "A full successor stub.", "POOL (page)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("detail text lacks %q:\n%s", want, out.String())
		}
	}
	if code := writeSpecCoverage(&out, &errOut, m, specCoverageOptions{queries: []string{"nope"}}); code != 2 {
		t.Fatalf("an unknown query exits 2, got %d", code)
	}
}

func TestTestsForResolvesIdentifiersCodesAndSections(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	cases := map[string][]string{
		"POOL-R5": {
			"pkg/pool/claim_test.go::TestClaimOrder",
			"pkg/pool/claim_test.go::TestWarmPool",
			"tests/component/pool/pool_test.go::TestComponentClaim",
		},
		"POOL": {
			"pkg/pool/claim_test.go::TestClaimOrder",
			"pkg/pool/claim_test.go::TestWarmPool",
			"tests/component/pool/pool_test.go::TestComponentClaim",
			"tests/contract/pool/pool_test.go::TestPoolContract",
		},
		sec + "5.1": {
			"pkg/pool/claim_test.go::TestClaimOrder",
			"pkg/pool/claim_test.go::TestWarmPool",
			"tests/component/pool/pool_test.go::TestComponentClaim",
		},
		"5.2":   {"pkg/pool/claim_test.go::TestFloor"},
		"5.3":   {},
		"AUDIT": {},
	}
	for q, want := range cases {
		got, ok := m.testsFor(q)
		if !ok {
			t.Errorf("%s does not resolve", q)
			continue
		}
		names := []string{}
		for _, tf := range got {
			names = append(names, tf.String())
		}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("testsFor(%s):\n got %v\nwant %v", q, names, want)
		}
	}
	for _, q := range []string{"POOL-R99", sec + "9.9", sec, "5.x", ""} {
		if _, ok := m.testsFor(q); ok {
			t.Errorf("%q resolves to nothing", q)
		}
	}
}

func TestQueryNamesListsCodesIdentifiersAndSections(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	want := []string{"AUDIT", "POOL", "POOL-R5", "POOL-R6", "POOL-claim", "setting-minwarm", sec + "5", sec + "5.1", sec + "5.2", sec + "5.2.1", sec + "5.3"}
	if got := m.queryNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("queryNames:\n got %v\nwant %v", got, want)
	}
}

func TestSpecsPlanRunsTheTiersOfTheResolvedTests(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	plan, err := specsPlanIn(m, []string{"POOL"})
	if err != nil {
		t.Fatal(err)
	}
	var tiers []string
	for _, p := range plan {
		tiers = append(tiers, p.name)
	}
	if want := []string{"unit", "component", "contract"}; !reflect.DeepEqual(tiers, want) {
		t.Fatalf("tiers: got %v, want %v", tiers, want)
	}
	if !reflect.DeepEqual(plan[0].pkgs, []string{"./pkg/pool"}) || len(plan[0].tests) != 2 {
		t.Errorf("unit scope: pkgs %v, tests %v", plan[0].pkgs, plan[0].tests)
	}
	if !reflect.DeepEqual(plan[2].tests, []string{"tests/contract/pool/pool_test.go::TestPoolContract"}) {
		t.Errorf("contract tests: %v", plan[2].tests)
	}
	plan, err = specsPlanIn(m, []string{"5.3"})
	if err != nil || len(plan) != 1 || plan[0].name != tierUnit || plan[0].pkgs != nil {
		t.Errorf("a query with no test runs the whole unit tier: %+v, %v", plan, err)
	}
}

func TestSpecsPlanRefusesAnUnknownQuery(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	if _, err := specsPlanIn(m, []string{"POOL", "POOL-R99", "nope"}); err == nil || !strings.Contains(err.Error(), "POOL-R99, nope") {
		t.Fatalf("unknown queries fail the selection: %v", err)
	}
}

// TestSpecSelectionIgnoresARetiredSpecMap pins that list --spec and
// run --spec read the annotation index alone since change 0116 retired
// tests/spec-map.json (CS-9): a stray map file in the tree adds no test,
// selects no tier, keeps the unit tier's package scope, and makes no
// section number known that names no numbered heading.
func TestSpecSelectionIgnoresARetiredSpecMap(t *testing.T) {
	t.Parallel()
	tree := specFixture()
	tree["tests/spec-map.json"] = `{"version": 1, "sections": {
  "5.2": {"tests": ["tests/security/pool_test.go::TestFloorIsolation", "pkg/pool/floor_test.go::TestFloorMath"]},
  "6.5": {"tests": ["tests/component/other/other_test.go::TestOther"]}
}}`
	m := fixtureModel(t, tree)
	plan, err := specsPlanIn(m, []string{sec + "5.2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].name != tierUnit || !reflect.DeepEqual(plan[0].pkgs, []string{"./pkg/pool"}) {
		t.Errorf("--spec 5.2 = %+v; want the unit tier scoped to ./pkg/pool", plan)
	}
	if _, err := specsPlanIn(m, []string{"6.5"}); err == nil {
		t.Errorf("a section number with no numbered heading resolved through the retired map")
	}
	listed, err := testsForSpecIn(m, []string{"5.2"})
	if err != nil || !reflect.DeepEqual(listed, []string{"pkg/pool/claim_test.go::TestFloor"}) {
		t.Errorf("list --spec 5.2 = %v, %v", listed, err)
	}
	tree["tests/spec-map.json"] = "{"
	if _, err := buildSpecModel(context.Background(), tree.list, tree.read); err != nil {
		t.Errorf("a map file that does not parse fails the model, which no longer reads it: %v", err)
	}
}

func TestTestsForSpecInRefusesUnknownQueries(t *testing.T) {
	t.Parallel()
	m := fixtureModel(t, specFixture())
	got, err := testsForSpecIn(m, []string{"5.2"})
	if err != nil || !reflect.DeepEqual(got, []string{"pkg/pool/claim_test.go::TestFloor"}) {
		t.Errorf("tests: %v, %v", got, err)
	}
	if _, err := testsForSpecIn(m, []string{"5.2", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown query is an error: %v", err)
	}
}

func TestIndirectCoverageNeedsTheStubsOwnSection(t *testing.T) {
	t.Parallel()
	tree := specFixture()
	tree["tests/component/sub/sub_test.go"] = "package sub\n\nimport \"testing\"\n\n// spec: " + sec + "5.1.4 (A subsection)\nfunc TestSubsection(t *testing.T) {}\n"
	m := fixtureModel(t, tree)
	rep, err := m.coverage(specCoverageOptions{queries: []string{"POOL-R5"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"unit pkg/pool/claim_test.go::TestWarmPool " + sec + "5.1"}; !reflect.DeepEqual(testNames(rep.Items[0].Indirect), want) {
		t.Errorf("a test citing only a subsection of the stub does not reach its identifiers: %v", testNames(rep.Items[0].Indirect))
	}
}

func TestCoverageNotesAFileTheIndexCouldNotRead(t *testing.T) {
	t.Parallel()
	tree := specFixture()
	m, err := buildSpecModel(context.Background(), func(ctx context.Context) ([]string, error) {
		all, err := tree.list(ctx)
		return append(all, "pkg/pool/gone.go"), err
	}, tree.read)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := m.coverage(specCoverageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range rep.Notes {
		found = found || strings.Contains(n, "left out pkg/pool/gone.go")
	}
	if !found {
		t.Errorf("notes: %v", rep.Notes)
	}
}

func TestSectionQueryAcceptsBothForms(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]string{sec + "4.7": "4.7", "4.7": "4.7", " " + sec + " 12 ": "12"} {
		if got, ok := sectionQuery(q); !ok || got != want {
			t.Errorf("sectionQuery(%q) = %q, %v; want %q", q, got, ok, want)
		}
	}
	for _, q := range []string{"POOL", "4.", ".4", "4..7", sec, ""} {
		if _, ok := sectionQuery(q); ok {
			t.Errorf("sectionQuery(%q) accepts a value that is not a section number", q)
		}
	}
}
