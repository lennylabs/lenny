// SPDX-License-Identifier: MIT

package pagegate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
)

// These cases cover specification tooling rather than a platform behavior,
// so they carry no specification annotation. Each gate has a page that
// passes it and pages that fail it, and every finding names its file and
// line.

// writeTree writes files (repo-relative path to content) under a fresh
// root and returns the root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// loadPages writes a tree and returns its pages.
func loadPages(t *testing.T, files map[string]string) []*Page {
	t.Helper()
	root := writeTree(t, files)
	tr, err := defs.LoadTree(root)
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	pages, err := Pages(root, tr)
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	return pages
}

// page returns a page with the given front matter fields and body. The
// front matter is four lines plus the fields, so with no extra field the
// body's first line is line 6.
func page(code, typ, extra, body string) string {
	return "---\ncode: " + code + "\ntype: " + typ + "\nsummary: A fixture page.\n" + extra + "---\n" + body
}

// checkOne runs the text gates over one page at spec/x/page.md.
func checkOne(t *testing.T, content string) []Problem {
	t.Helper()
	return Check(loadPages(t, map[string]string{"spec/x/page.md": content}))
}

func assertProblem(t *testing.T, problems []Problem, line int, want string) {
	t.Helper()
	for _, p := range problems {
		if (line < 0 || p.Line == line) && strings.Contains(p.Message, want) {
			return
		}
	}
	t.Errorf("no problem at line %d containing %q; got:\n%s", line, want, joinProblems(problems))
}

func assertNone(t *testing.T, problems []Problem) {
	t.Helper()
	if len(problems) > 0 {
		t.Errorf("want no problem, got:\n%s", joinProblems(problems))
	}
}

func joinProblems(problems []Problem) string {
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, "  "+p.String())
	}
	return strings.Join(lines, "\n")
}

const termsTable = `# Pod claims

## Terms {#terms}

| Term | Definition |
| :-- | :-- |
| <a id="claim"></a>Claim | The occupancy of one pod for 10 minutes, which MUST hold. |

| Term | Gloss |
| :-- | :-- |
`

func TestGlossesPassWhenEachHoldsOneLinkAndNoValueOrRule(t *testing.T) {
	t.Parallel()
	body := termsTable +
		"| Pool | A warmable deployment target for one runtime ([POOL](../pools/warm-pools.md#pool)). |\n" +
		"| `key16` | The short form of a pod-set key, written `a|b` in examples ([Revisions](revisions.md)). |\n" +
		"| Pod hash | The digest of a revision ([REV-R4](revisions.md#rev-r4)), an HTTP/2 detail. |\n" +
		"\n## Overview\n\n| Outside | Two [a](a.md) [b](b.md) links and 10 values. |\n"
	assertNone(t, checkOne(t, page("CLAIM", "capability", "", body)))
}

func TestGlossesFailOnLinksNumbersAndNormativeKeywords(t *testing.T) {
	t.Parallel()
	// The body opens on line 6; the borrowed rows start on line 16.
	body := termsTable +
		"| No link | A pool with no owner link. |\n" +
		"| Two links | A pool ([POOL](a.md#pool)) and a claim ([CLAIM](b.md#claim)). |\n" +
		"| Value | A pool of 300 pods ([POOL](a.md#pool)). |\n" +
		"| Code value | A pool that holds `10s` of slack ([POOL](a.md#pool)). |\n" +
		"| Upper | A pool that MAY drain ([POOL](a.md#pool)). |\n" +
		"| Lower | A pool that must drain ([POOL](a.md#pool)). |\n" +
		"| Capital | Should hold a pool ([POOL](a.md#pool)). |\n"
	problems := checkOne(t, page("CLAIM", "capability", "", body))
	assertProblem(t, problems, 16, "No link holds 0 links")
	assertProblem(t, problems, 17, "Two links holds 2 links")
	assertProblem(t, problems, 18, "holds the number 300")
	assertProblem(t, problems, 19, "holds the number 10")
	assertProblem(t, problems, 20, `normative keyword "MAY"`)
	assertProblem(t, problems, 21, `normative keyword "must"`)
	assertProblem(t, problems, 22, `normative keyword "Should"`)
	if len(problems) != 7 {
		t.Errorf("got %d problems, want one per failing row; the owned row and its header are not glosses:\n%s", len(problems), joinProblems(problems))
	}
}

func TestGlossesAreTheBorrowedRowsOfTheTermsTable(t *testing.T) {
	t.Parallel()
	pages := loadPages(t, map[string]string{"spec/x/page.md": page("CLAIM", "capability", "", termsTable+
		"| Pool | A target ([POOL](a.md#pool)). |\n\n```markdown\n| Fenced | Not a row. |\n```\n\n## Overview\n\n| Later | Not a gloss. |\n")})
	got := Glosses(pages[0])
	if len(got) != 1 || got[0].Term != "Pool" || got[0].Line != 16 || !strings.Contains(got[0].Text, "A target") {
		t.Errorf("Glosses = %+v, want the one borrowed row on line 16", got)
	}
}

func TestWalkthroughsHoldNoNormativeKeyword(t *testing.T) {
	t.Parallel()
	pass := "# Create a session\n\nThe client calls [create](x.md#create), and the gateway claims a pod. A field named `mustExist` is code.\n\n```text\nMUST in a fence is example text\n```\n"
	assertNone(t, checkOne(t, page("WTSESS", "walkthrough", "", pass)))

	fail := "# Create a session\n\nThe gateway MUST claim a pod.\n\nThe client should retry.\n\n<!-- SHALL in a comment is not prose -->\n"
	problems := checkOne(t, page("WTSESS", "walkthrough", "", fail))
	assertProblem(t, problems, 8, `uses "MUST"`)
	assertProblem(t, problems, 10, `uses "should"`)
	if len(problems) != 2 {
		t.Errorf("got %d problems, want two:\n%s", len(problems), joinProblems(problems))
	}

	// A capability states rules, so the same text passes there.
	assertNone(t, checkOne(t, page("CAP", "capability", "", fail)))

	// The summary is prose of the page too.
	problems = checkOne(t, "---\ncode: WTSESS\ntype: walkthrough\nsummary: The pod MUST be claimed.\n---\n# Walk\n")
	assertProblem(t, problems, 4, `uses "MUST"`)
}

func TestStylePassesOnPlainProse(t *testing.T) {
	t.Parallel()
	body := "# Warm pools\n\n## Settings\n\nThe controller fills the pool just-in-time. The word `robust` in code is a name.\n\n" +
		"See [the pool path](robust.md) and [4.6 Very Old Heading](../04_system-components.md#46-very-old-heading).\n\n" +
		"```text\nsimply a fenced example\n```\n\n<!-- really a comment -->\n\n## HTTP/2 settings\n"
	assertNone(t, checkOne(t, page("POOL", "capability", "", body)))
}

func TestStyleFailsOnBannedWordsAndCountHeadings(t *testing.T) {
	t.Parallel()
	body := "# Warm pools\n\n" + // lines 6-7
		"The pool is robust.\n" + // 8
		"The controller simply fills it.\n" + // 9
		"Let's start with the pool.\n" + // 10
		"Notably, the pool drains.\n" + // 11
		"That's it.\n" + // 12
		"\n## The four meta-tools\n" + // 14
		"\n## 3 deployment modes\n" // 16
	problems := checkOne(t, page("POOL", "capability", "", body))
	assertProblem(t, problems, 8, `"robust"`)
	assertProblem(t, problems, 9, `"simply"`)
	assertProblem(t, problems, 10, `"Let's"`)
	assertProblem(t, problems, 11, `"Notably"`)
	assertProblem(t, problems, 12, `"That's it"`)
	assertProblem(t, problems, 14, `names the count "four"`)
	assertProblem(t, problems, 16, `names the count "3"`)
	if len(problems) != 7 {
		t.Errorf("got %d problems, want seven:\n%s", len(problems), joinProblems(problems))
	}

	problems = checkOne(t, "---\ncode: POOL\ntype: capability\nsummary: A seamless pool.\n---\n# Pool\n")
	assertProblem(t, problems, 4, `"seamless"`)
}

func TestReviewCommentsAreRefusedOnceAPageIsWritten(t *testing.T) {
	t.Parallel()
	body := "# Pool\n\n<!-- src: §4.6 -->\nText.\n\n<!-- judgment:\nplaced here -->\n\n<!-- unresolved: the default -->\n\nThe form `<!-- src: ... -->` is quoted.\n\n```markdown\n<!-- src: fenced -->\n```\n"
	problems := checkOne(t, page("POOL", "capability", "", body))
	assertProblem(t, problems, 8, `"src" review comment`)
	assertProblem(t, problems, 11, `"judgment" review comment`)
	assertProblem(t, problems, 14, `"unresolved" review comment`)
	if len(problems) != 3 {
		t.Errorf("got %d problems, want three:\n%s", len(problems), joinProblems(problems))
	}
	for _, status := range []string{"placeholder", "partial"} {
		assertNone(t, checkOne(t, page("POOL", "capability", "status: "+status+"\n", body)))
	}
}

func TestSizesReportPagesOverTheirBudget(t *testing.T) {
	t.Parallel()
	words := func(n int) string { return strings.Repeat("word ", n) + "\n" }
	pages := loadPages(t, map[string]string{
		"spec/a/big.md":       page("BIG", "capability", "", words(6001)),
		"spec/a/small.md":     page("SMALL", "capability", "", words(5990)+"<!-- "+words(100)+"-->\n"),
		"spec/a/index.md":     page("AIDX", "index", "", words(1501)),
		"spec/b/index.md":     page("BIDX", "index", "", words(1400)),
		"spec/catalogs/a.md":  page("CATA", "catalog", "", words(9000)),
		"spec/04_numbered.md": words(9000),
	})
	got := Sizes(pages)
	var lines []string
	for _, w := range got {
		lines = append(lines, w.String())
	}
	want := []string{
		"spec/a/big.md: 6001 words, over the budget of about 6000",
		"spec/a/index.md: 1501 words, over the budget of about 1500",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("Sizes =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestNumberedFilesStayOutOfScopeUntilTheyAreStubs(t *testing.T) {
	t.Parallel()
	pages := loadPages(t, map[string]string{
		"spec/04_system-components.md": "# 4. System Components\n\nThe pool is robust.\n",
		"spec/x/page.md":               page("POOL", "capability", "", "# Pool\n"),
	})
	if len(pages) != 1 || pages[0].Path != "spec/x/page.md" {
		t.Errorf("Pages = %v, want only the page with front matter", pages)
	}
	if StubbedNumberedFile("spec/04_system-components.md", "# 4. System Components\n\nThe pool is robust.\n") {
		t.Error("a numbered file whose section holds its own text is in scope")
	}
	stub := "# 4. System Components\n\n" + ref.StubMarker + "\n\nThe content of this section has moved to these pages and identifiers:\n\n- [Pool](x/page.md)\n\n## 4.1 Gateway\n\n" + ref.StubMarker + "\n\nThe content of this section has moved to these pages and identifiers:\n\n- [Pool](x/page.md)\n"
	if !StubbedNumberedFile("spec/04_system-components.md", stub) {
		t.Error("a numbered file whose every section is a full stub is out of scope")
	}
	partial := stub + "\n## 4.2 Controllers\n\nStill here.\n"
	if StubbedNumberedFile("spec/04_system-components.md", partial) {
		t.Error("a numbered file with a section still holding its text is in scope")
	}
	if StubbedNumberedFile("spec/x/page.md", stub) {
		t.Error("a page outside the numbered files is treated as a numbered file")
	}
}

// TestStyleLeavesTheHeadingsOfANumberedStubAlone pins that a successor
// stub's heading, which keeps the wording section citations reach it by,
// is not held to the style rule, while the stub's own text still is.
func TestStyleLeavesTheHeadingsOfANumberedStubAlone(t *testing.T) {
	t.Parallel()
	lead := "\n\n" + ref.StubMarker + "\n\nThe content of this section has moved to these pages and identifiers:\n\n"
	stub := "## 14. Comprehensive Security Hardening" + lead + "- [Pool](x/page.md): the pool.\n\n" +
		"#### 2026 Open-Source Entrants Matrix" + lead + "- [Pool](x/page.md): a robust pool.\n"
	pages := loadPages(t, map[string]string{
		"spec/14_hardening.md": stub,
		"spec/x/page.md":       page("POOL", "capability", "", "# Pool\n"),
	})
	var numbered *Page
	for _, p := range pages {
		if p.Path == "spec/14_hardening.md" {
			numbered = p
		}
	}
	if numbered == nil {
		t.Fatalf("the stubbed numbered file is not among the pages: %v", pages)
	}
	problems := checkStyle(numbered)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, `"robust"`) {
		t.Errorf("checkStyle = %s, want only the banned word of the stub's own text", joinProblems(problems))
	}
}

// contribTree is a fixture tree with the unit pages GW and WPC.
func contribTree(t *testing.T, blocks string) *defs.Tree {
	t.Helper()
	root := writeTree(t, map[string]string{
		"spec/components/index.md": page("COMPS", "index", "", "# Components\n"),
		"spec/components/gw.md":    page("GW", "component", "", "# Gateway\n\n"+blocks),
		"spec/components/wpc.md":   page("WPC", "component", "", "# Controller\n"),
	})
	tr, err := defs.LoadTree(root)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func block(anchor, yaml string) string {
	return "<a id=\"" + anchor + "\"></a>\n\n```lenny-def\n" + yaml + "```\n\n"
}

func fixtureManifests(t *testing.T) []Object {
	t.Helper()
	data, err := os.ReadFile("testdata/rendered.yaml")
	if err != nil {
		t.Fatal(err)
	}
	objs, err := ParseManifests(data)
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

var fixtureUnits = UnitRegister{Units: map[string]Unit{
	"GW":  {ServiceAccount: "fixture-gateway"},
	"WPC": {ServiceAccount: "fixture-controller"},
}}

func TestContributionsThatTheChartGrantsAndAllowsPass(t *testing.T) {
	t.Parallel()
	blocks := block("grant-pods-read", "kind: grant\nname: pods read\napi-groups: [\"\"]\nresources: [pods]\nverbs: [get, watch]\nnamespace-scope: cluster\nrequirement: GW-R1\n") +
		block("rbac-grant-leases", "kind: rbac-grant\nname: leases\nunit: GW\napi-groups: [coordination.k8s.io]\nresources: [leases]\nverbs: [update]\nnamespace-scope: lenny-system\nrequirement: GW-R2\n") +
		block("rbac-grant-any-lenny-resource", "kind: rbac-grant\nname: any lenny resource\nunit: GW\napi-groups: [lenny.dev]\nresources: [podclaims]\nverbs: [get]\nnamespace-scope: cluster\nrequirement: GW-R3\n") +
		block("network-rule-to-controller", "kind: network-rule\nname: to controller\nunit: GW\ndirection: egress\npeer: WPC\nports: [8443/TCP]\nrequirement: GW-R4\n") +
		block("network-rule-to-postgres", "kind: network-rule\nname: to postgres\nunit: GW\ndirection: egress\npeer: 10.1.2.0/24\nports: [\"5432\"]\nrequirement: GW-R5\n") +
		block("network-rule-to-pods", "kind: network-rule\nname: to pods\nunit: GW\ndirection: egress\npeer: lenny.dev/managed=true\nports: [50055/TCP]\nrequirement: GW-R6\n") +
		block("network-rule-https-in", "kind: network-rule\nname: https in\nunit: GW\ndirection: ingress\npeer: 0.0.0.0/0\nports: [https]\nrequirement: GW-R7\n")
	assertNone(t, CheckContributions(contribTree(t, blocks), fixtureManifests(t), fixtureUnits))
}

func TestContributionsThatTheChartLacksFail(t *testing.T) {
	t.Parallel()
	blocks := block("grant-pods-delete", "kind: grant\nname: pods delete\napi-groups: [\"\"]\nresources: [pods]\nverbs: [get, delete]\nnamespace-scope: cluster\nrequirement: GW-R1\n") +
		block("rbac-grant-leases-cluster", "kind: rbac-grant\nname: leases cluster\nunit: GW\napi-groups: [coordination.k8s.io]\nresources: [leases]\nverbs: [get]\nnamespace-scope: cluster\nrequirement: GW-R2\n") +
		block("rbac-grant-unknown-unit", "kind: rbac-grant\nname: unknown unit\nunit: COMPS\napi-groups: [\"\"]\nresources: [pods]\nverbs: [get]\nnamespace-scope: cluster\nrequirement: GW-R3\n") +
		block("rbac-grant-missing-sa", "kind: rbac-grant\nname: missing sa\nunit: GW\nservice-account: nobody\napi-groups: [\"\"]\nresources: [pods]\nverbs: [get]\nnamespace-scope: cluster\nrequirement: GW-R3\n") +
		block("network-rule-wrong-port", "kind: network-rule\nname: wrong port\nunit: GW\ndirection: egress\npeer: WPC\nports: [9090/TCP, 8443/UDP]\nrequirement: GW-R4\n") +
		block("network-rule-wrong-direction", "kind: network-rule\nname: wrong direction\nunit: GW\ndirection: ingress\npeer: WPC\nports: [8443/TCP]\nrequirement: GW-R4\n") +
		block("network-rule-outside-cidr", "kind: network-rule\nname: outside cidr\nunit: GW\ndirection: egress\npeer: 192.168.0.0/16\nports: [\"5432\"]\nrequirement: GW-R5\n") +
		block("network-rule-bad-peer", "kind: network-rule\nname: bad peer\nunit: GW\ndirection: egress\npeer: the database\nports: [\"5432\"]\nrequirement: GW-R5\n") +
		block("network-rule-no-workload", "kind: network-rule\nname: no workload\nunit: WPC\nservice-account: fixture-gateway-only\ndirection: egress\npeer: GW\nports: [\"443\"]\nrequirement: GW-R5\n")
	problems := CheckContributions(contribTree(t, blocks), fixtureManifests(t), fixtureUnits)
	for _, want := range []string{
		`grant "pods delete": no cluster role bound to the ServiceAccount fixture-gateway allows delete on pods in the API group ""`,
		`rbac-grant "leases cluster": no cluster role bound to the ServiceAccount fixture-gateway allows get on leases`,
		`rbac-grant "unknown unit": the unit COMPS has no entry`,
		`rbac-grant "missing sa": the chart renders no ServiceAccount nobody`,
		`network-rule "wrong port": no NetworkPolicy that selects the pods of the ServiceAccount fixture-gateway allows egress 9090/TCP`,
		`network-rule "wrong port": no NetworkPolicy that selects the pods of the ServiceAccount fixture-gateway allows egress 8443/UDP`,
		`network-rule "wrong direction": no NetworkPolicy that selects the pods of the ServiceAccount fixture-gateway allows ingress 8443/TCP`,
		`network-rule "outside cidr": no NetworkPolicy`,
		`network-rule "bad peer": the peer "the database" is neither a CIDR, a unit code, nor a key=value selector`,
		`network-rule "no workload": the chart renders no ServiceAccount fixture-gateway-only`,
	} {
		assertProblem(t, problems, -1, want)
	}
	if len(problems) != 10 {
		t.Errorf("got %d problems, want ten:\n%s", len(problems), joinProblems(problems))
	}
	for _, p := range problems {
		if p.Path != "spec/components/gw.md" || p.Line == 0 {
			t.Errorf("a problem is not located at its block: %s", p)
		}
	}
}

func TestContributionsWithNoBlocksPass(t *testing.T) {
	t.Parallel()
	assertNone(t, CheckContributions(contribTree(t, ""), nil, UnitRegister{}))
}

func TestUnitRegisterNamesPagesAndRenderedServiceAccounts(t *testing.T) {
	t.Parallel()
	tr := contribTree(t, "")
	assertNone(t, CheckUnits(tr, fixtureManifests(t), fixtureUnits))
	problems := CheckUnits(tr, fixtureManifests(t), UnitRegister{Units: map[string]Unit{
		"NOPAGE": {ServiceAccount: "fixture-gateway"},
		"GW":     {ServiceAccount: "not-rendered"},
		"WPC":    {},
	}})
	assertProblem(t, problems, -1, "unit NOPAGE is not the code of a page")
	assertProblem(t, problems, -1, "unit GW names the ServiceAccount not-rendered, which the chart does not render")
	assertProblem(t, problems, -1, "unit WPC names no service-account")
	if len(problems) != 3 {
		t.Errorf("got %d problems, want three:\n%s", len(problems), joinProblems(problems))
	}
}

func TestParseManifestsSkipsEmptyDocumentsAndRefusesInvalidYAML(t *testing.T) {
	t.Parallel()
	objs, err := ParseManifests([]byte("---\n# only a comment\n---\nkind: ServiceAccount\nmetadata: {name: a, namespace: b}\n---\n"))
	if err != nil || len(objs) != 1 || objs[0].Kind() != "ServiceAccount" || objs[0].Name() != "a" || objs[0].Namespace() != "b" {
		t.Errorf("ParseManifests = %v, %v; want the one ServiceAccount", objs, err)
	}
	if _, err := ParseManifests([]byte("kind: [unclosed\n")); err == nil {
		t.Error("ParseManifests accepted invalid YAML")
	}
}

func TestRunChecksAPageTreeAndAReleaseRendering(t *testing.T) {
	t.Parallel()
	root := writeTree(t, map[string]string{
		"spec/x/index.md": page("XIDX", "index", "", "# X\n"),
		"spec/x/page.md":  page("POOL", "capability", "", "# Pool\n\nThe pool is robust.\n"),
		UnitRegisterPath:  "units:\n  POOL:\n    service-account: fixture-gateway\n",
	})
	var out bytes.Buffer
	err := Run(context.Background(), []string{"-root", root, "check"}, &out)
	if err == nil || !strings.Contains(out.String(), "spec/x/page.md:8:") {
		t.Errorf("pages check: %v\n%s", err, out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"-root", root, "contributions", "-manifests", "testdata/rendered.yaml"}, &out); err != nil {
		t.Errorf("pages contributions: %v\n%s", err, out.String())
	}

	clean := writeTree(t, map[string]string{"spec/x/index.md": page("XIDX", "index", "", "# X\n")})
	out.Reset()
	if err := Run(context.Background(), []string{"-root", clean, "check"}, &out); err != nil || !strings.Contains(out.String(), "no problems") {
		t.Errorf("pages check over a clean tree: %v\n%s", err, out.String())
	}

	for _, args := range [][]string{{"-root", root}, {"-root", root, "nope"}, {"-root", root, "check", "extra"}, {"-root", root, "contributions"}} {
		out.Reset()
		if err := Run(context.Background(), args, &out); err == nil {
			t.Errorf("Run %v succeeded", args)
		}
	}
}

func TestSelectorsAndPodPeersMatchAsKubernetesEvaluatesThem(t *testing.T) {
	t.Parallel()
	labels := map[string]string{"app": "gw", "tier": "edge"}
	expr := func(op string, values ...any) map[string]any {
		return map[string]any{"matchExpressions": []any{map[string]any{"key": "app", "operator": op, "values": values}}}
	}
	cases := []struct {
		name string
		sel  map[string]any
		want bool
	}{
		{"empty", map[string]any{}, true},
		{"labels match", map[string]any{"matchLabels": map[string]any{"app": "gw"}}, true},
		{"labels differ", map[string]any{"matchLabels": map[string]any{"app": "ops"}}, false},
		{"in", expr("In", "gw", "ops"), true},
		{"in misses", expr("In", "ops"), false},
		{"not in", expr("NotIn", "ops"), true},
		{"not in hits", expr("NotIn", "gw"), false},
		{"exists", expr("Exists"), true},
		{"does not exist", expr("DoesNotExist"), false},
		{"unknown operator", expr("Near"), false},
	}
	for _, c := range cases {
		if got := selectorMatches(c.sel, labels); got != c.want {
			t.Errorf("%s: selectorMatches = %v, want %v", c.name, got, c.want)
		}
	}

	w := workload{namespace: "lenny-system", labels: labels}
	peers := []struct {
		name string
		peer map[string]any
		want bool
	}{
		{"no selector", map[string]any{"ipBlock": map[string]any{"cidr": "10.0.0.0/8"}}, false},
		{"pod selector in the policy namespace", map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "gw"}}}, true},
		{"namespace selector alone", map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "lenny-system"}}}, true},
		{"namespace selector for another namespace", map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "lenny-agents"}}}, false},
	}
	for _, c := range peers {
		if got := podPeerMatches(c.peer, w, "lenny-system"); got != c.want {
			t.Errorf("%s: podPeerMatches = %v, want %v", c.name, got, c.want)
		}
	}
	if podPeerMatches(map[string]any{"podSelector": map[string]any{}}, w, "lenny-agents") {
		t.Error("a pod selector with no namespace selector matched a workload in another namespace")
	}
	if _, err := LoadUnits(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("LoadUnits read a register that does not exist")
	}
	bad := writeTree(t, map[string]string{"units.yaml": "units: [unclosed\n"})
	if _, err := LoadUnits(filepath.Join(bad, "units.yaml")); err == nil {
		t.Error("LoadUnits accepted invalid YAML")
	}
}
