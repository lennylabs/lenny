// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/defs"
	"github.com/lennylabs/lenny/scripts/specshift/pagegate"
	"github.com/lennylabs/lenny/tests/testinfra/helm"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// These gates hold the text of the specification tree's pages and the
// contribution blocks against the chart, through the pagegate package of
// specshift. They hold the specification's own structure rather than a
// platform behavior, so no specification section applies and they carry
// no specification annotation. The rules they hold are stated on
// spec/conventions.md under "Normative language", "Gloss rule", "Review
// comments during migration", "Page size", and "Definition blocks", and in
// .claude/rules/doc-style.md.

// loadSpecPages loads the pages of the specification tree the gates read.
func loadSpecPages(t *testing.T, root string) (*defs.Tree, []*pagegate.Page) {
	t.Helper()
	tr, err := defs.LoadTree(root)
	if err != nil {
		t.Fatalf("load the specification tree: %v", err)
	}
	pages, err := pagegate.Pages(root, tr)
	if err != nil {
		t.Fatalf("list the specification pages: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("the specification tree holds no page with front matter; a gate over no page certifies nothing")
	}
	return tr, pages
}

// diagnosis: a page of the specification tree breaks the gloss rule, a
// walkthrough holds a normative keyword, a page uses a word or a count
// heading that the documentation style rule bans, or a written page still
// carries a src, judgment, or unresolved review comment. Each finding
// names the file and the line; run `go run ./scripts/specshift pages
// check` to reproduce it.
func TestSpecPagesPassTheTextGates(t *testing.T) {
	t.Parallel()
	_, pages := loadSpecPages(t, schematest.RepoRoot(t))
	problems := pagegate.Check(pages)
	if len(problems) > 0 {
		lines := make([]string, 0, len(problems))
		for _, p := range problems {
			lines = append(lines, p.String())
		}
		t.Errorf("%d problem(s) on the specification pages:\n  %s", len(problems), strings.Join(lines, "\n  "))
	}
	t.Logf("%d page(s) read", len(pages))
}

// The page-size report warns rather than fails: the conventions give the
// budgets as approximate, and a page over one is split in review.
func TestSpecPageSizesAreReportedAsWarnings(t *testing.T) {
	t.Parallel()
	_, pages := loadSpecPages(t, schematest.RepoRoot(t))
	for _, w := range pagegate.Sizes(pages) {
		t.Logf("warning: %s", w)
	}
}

// renderChartObjects renders the chart the way the other chart tests of
// this tier do and returns its objects for the contribution gate.
func renderChartObjects(t *testing.T, root string) []pagegate.Object {
	t.Helper()
	helm.SkipUnlessAvailable(t)
	rendered := helm.Render(t, helm.Options{
		Chart:     filepath.Join(root, "charts", "lenny"),
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       []string{"coredns.clusterIP=10.96.0.10"},
	})
	objs := make([]pagegate.Object, 0, len(rendered))
	for _, m := range rendered {
		objs = append(objs, pagegate.Object(m.Raw))
	}
	return objs
}

func loadUnitRegister(t *testing.T, root string) pagegate.UnitRegister {
	t.Helper()
	units, err := pagegate.LoadUnits(filepath.Join(root, filepath.FromSlash(pagegate.UnitRegisterPath)))
	if err != nil {
		t.Fatal(err)
	}
	if len(units.Units) == 0 {
		t.Fatalf("%s names no unit", pagegate.UnitRegisterPath)
	}
	return units
}

// diagnosis: a grant, rbac-grant, or network-rule block states a
// permission or a network path that the chart's rendered Roles,
// ClusterRoles, and NetworkPolicies do not give the block's unit, or the
// unit register names a page code the tree lacks or a ServiceAccount the
// chart does not render. Fix the chart or the block, whichever is wrong,
// and keep tests/registers/spec-unit-service-accounts.yaml current.
func TestSpecContributionBlocksAgreeWithTheChart(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	objs := renderChartObjects(t, root)
	units := loadUnitRegister(t, root)
	tr, _ := loadSpecPages(t, root)
	problems := append(pagegate.CheckUnits(tr, objs, units), pagegate.CheckContributions(tr, objs, units)...)
	for _, p := range problems {
		t.Error(p.String())
	}
	blocks := 0
	for _, b := range tr.Blocks() {
		if b.Kind == defs.KindGrant || b.Kind == defs.KindRBACGrant || b.Kind == defs.KindNetworkRule {
			blocks++
		}
	}
	t.Logf("%d contribution block(s) held against %d rendered object(s) and %d unit(s)", blocks, len(objs), len(units.Units))
}

// The real tree holds no contribution block yet, so this case runs the
// gate over the rendered chart with blocks of its own: three the chart
// grants or allows, and one it does not.
func TestSpecContributionGateReadsTheRenderedChart(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	objs := renderChartObjects(t, root)
	units := loadUnitRegister(t, root)

	fixture := t.TempDir()
	page := "---\ncode: GW\ntype: component\nsummary: The gateway.\n---\n# Gateway\n\n" +
		"<a id=\"rbac-grant-sandboxclaims-create\"></a>\n\n```lenny-def\nkind: rbac-grant\nname: sandboxclaims create\nunit: GW\napi-groups: [lenny.dev]\nresources: [sandboxclaims]\nverbs: [create]\nnamespace-scope: cluster\nrequirement: GW-R1\n```\n\n" +
		"<a id=\"rbac-grant-leases-update\"></a>\n\n```lenny-def\nkind: rbac-grant\nname: leases update\nunit: GW\napi-groups: [coordination.k8s.io]\nresources: [leases]\nverbs: [update]\nnamespace-scope: lenny-system\nrequirement: GW-R2\n```\n\n" +
		"<a id=\"network-rule-to-token-service\"></a>\n\n```lenny-def\nkind: network-rule\nname: to token service\nunit: GW\ndirection: egress\npeer: TOKSVC\nports: [50052/TCP]\nrequirement: GW-R3\n```\n\n" +
		"<a id=\"rbac-grant-secrets-delete\"></a>\n\n```lenny-def\nkind: rbac-grant\nname: secrets delete\nunit: GW\napi-groups: [\"\"]\nresources: [secrets]\nverbs: [delete]\nnamespace-scope: cluster\nrequirement: GW-R4\n```\n"
	target := filepath.Join(fixture, "spec", "components", "gateway.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := defs.LoadTree(fixture)
	if err != nil {
		t.Fatal(err)
	}
	problems := pagegate.CheckContributions(tr, objs, units)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, `rbac-grant "secrets delete": no cluster role bound to the ServiceAccount lenny-gateway allows delete on secrets`) {
		lines := make([]string, 0, len(problems))
		for _, p := range problems {
			lines = append(lines, p.String())
		}
		t.Errorf("want the one grant the chart lacks, got %d problem(s):\n  %s", len(problems), strings.Join(lines, "\n  "))
	}
}
